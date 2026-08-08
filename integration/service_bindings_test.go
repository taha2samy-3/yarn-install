package integration_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/paketo-buildpacks/occam"
	"github.com/sclevine/spec"

	. "github.com/onsi/gomega"
)

func testServiceBindings(t *testing.T, context spec.G, it spec.S) {
	var (
		Expect       = NewWithT(t).Expect
		Consistently = NewWithT(t).Consistently

		pack   occam.Pack
		docker occam.Docker

		image     occam.Image
		container occam.Container

		name   string
		source string

		pullPolicy = "never"
	)

	it.Before(func() {
		pack = occam.NewPack().WithVerbose().WithNoColor()
		docker = occam.NewDocker()

		if settings.Extensions.UbiNodejsExtension.Online != "" {
			pullPolicy = "always"
		}
	})

	context("when service bindings are provided", func() {
		var binding string

		it.Before(func() {

			pack = occam.NewPack().WithNoColor()
			docker = occam.NewDocker()

			var err error
			name, err = occam.RandomName()
			Expect(err).NotTo(HaveOccurred())

			binding, err = os.MkdirTemp("", "binding")
			Expect(err).NotTo(HaveOccurred())
			Expect(os.Chmod(binding, os.ModePerm)).To(Succeed())
		})

		it.After(func() {
			Expect(os.RemoveAll(binding)).To(Succeed())

			if container.ID != "" {
				Expect(docker.Container.Remove.Execute(container.ID)).To(Succeed())
			}
			if image.ID != "" {
				Expect(docker.Image.Remove.Execute(image.ID)).To(Succeed())
			}
			Expect(docker.Volume.Remove.Execute(occam.CacheVolumeNames(name))).To(Succeed())
			Expect(os.RemoveAll(source)).To(Succeed())
		})

		context("when the binding has type npmrc", func() {
			var (
				server *httptest.Server
				build  occam.PackBuild
			)
			it.Before(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					auth := false
					for name, headers := range req.Header {
						for _, h := range headers {
							if name == "Authorization" && h == "Bearer whatever" {
								auth = true
								break
							}
						}
					}
					if auth {
						w.WriteHeader(http.StatusOK)
						_, err := fmt.Fprintln(w, "Authenticated!")
						Expect(err).NotTo(HaveOccurred())

					} else {
						w.WriteHeader(http.StatusUnauthorized)
						_, err := fmt.Fprintln(w, "Auth token not provided")
						Expect(err).NotTo(HaveOccurred())

					}
				}))
				uri, err := url.Parse(server.URL)
				Expect(err).NotTo(HaveOccurred())

				source, err = occam.Source(filepath.Join("testdata", "service_bindings_app"))
				Expect(err).NotTo(HaveOccurred())

				var serverURI string
				switch os := runtime.GOOS; os {
				case "darwin":
					serverURI = fmt.Sprintf("http://host.docker.internal:%s", uri.Port())
				case "windows":
					serverURI = fmt.Sprintf("http://host.docker.internal:%s", uri.Port())
				case "linux":
					// host.docker.internal is not supported on linux; use host's address
					// and build WithNetwork("host")
					serverURI = fmt.Sprintf("http://127.0.0.1:%s", uri.Port())
				default:
					t.Fatal("unrecognized runtime.GOOS: " + runtime.GOOS)
				}

				Expect(os.WriteFile(filepath.Join(source, "pnpm-lock.yaml"), []byte(fmt.Sprintf(`lockfileVersion: '9.0'

settings:
  autoInstallPeers: true
  excludeLinksFromLockfile: false

importers:

  .:
    dependencies:
      leftpad:
        specifier: ~0.0.1
        version: '%[1]s/leftpad/-/leftpad-0.0.1.tgz'

packages:

  'leftpad@%[1]s/leftpad/-/leftpad-0.0.1.tgz':
    resolution: {tarball: '%[1]s/leftpad/-/leftpad-0.0.1.tgz'}

snapshots:

  'leftpad@%[1]s/leftpad/-/leftpad-0.0.1.tgz': {}
`, serverURI)), os.ModePerm)).To(Succeed())

				Expect(os.WriteFile(filepath.Join(binding, "type"), []byte("npmrc"), os.ModePerm)).To(Succeed())
				Expect(os.WriteFile(filepath.Join(binding, ".npmrc"), []byte(fmt.Sprintf(`always-auth=true
registry=%[1]s/
//%[2]s/:_authToken=whatever
`, serverURI, uri.Host)), os.ModePerm)).To(Succeed())

				build = pack.Build.
					WithExtensions(
						settings.Extensions.UbiNodejsExtension.Online,
					).
					WithBuildpacks(nodeURI, pnpmURI, buildpackURI, buildPlanURI).
					WithPullPolicy(pullPolicy).
					WithEnv(map[string]string{
						"SERVICE_BINDING_ROOT": "/bindings",
					}).
					WithVolumes(fmt.Sprintf("%s:/bindings/npmrc", binding))

				if runtime.GOOS == "linux" {
					build = build.WithNetwork("host") // this allows the container to reach 127.0.0.1 on the host
				}
			})

			it.After(func() {
				server.Close()
			})

			it("successfully uses the npmrc to authenticate and fails due to invalid pnpm package", func() {
				var err error
				image, _, err = build.Execute(name, source)

				Expect(err).NotTo(MatchError(ContainSubstring("401 Unauthorized")))
				Expect(err).To(MatchError(Or(ContainSubstring("Unexpected end of data"), ContainSubstring("Invalid checksum for TAR header"))))
			})
		})

		context("when binding has type pnpmrc", func() {
			it.Before(func() {
				Expect(os.WriteFile(filepath.Join(binding, "type"), []byte("pnpmrc"), os.ModePerm)).To(Succeed())
				Expect(os.WriteFile(filepath.Join(binding, ".pnpmrc"), []byte(`ignore-scripts=true`), os.ModePerm)).To(Succeed())
			})

			it("uses the pnpmrc and does not run the postinstall script", func() {
				var err error
				source, err = occam.Source(filepath.Join("testdata", "service_bindings_app"))
				Expect(err).NotTo(HaveOccurred())

				image, _, err = pack.Build.
					WithExtensions(
						settings.Extensions.UbiNodejsExtension.Online,
					).
					WithBuildpacks(nodeURI, pnpmURI, buildpackURI, buildPlanURI).
					WithPullPolicy(pullPolicy).
					WithEnv(map[string]string{
						"SERVICE_BINDING_ROOT": "/bindings",
					}).
					WithVolumes(fmt.Sprintf("%s:/bindings/pnpmrc", binding)).
					Execute(name, source)
				Expect(err).NotTo(HaveOccurred())

				container, err = docker.Container.Run.
					WithCommand(fmt.Sprintf("ls -alR /workspace && ls -alR /layers/%s/modules/node_modules", strings.ReplaceAll(buildpackInfo.Buildpack.ID, "/", "_"))).
					Execute(image.ID)
				Expect(err).NotTo(HaveOccurred())

				cLogs := func() string {
					cLogs, err := docker.Container.Logs.Execute(container.ID)
					Expect(err).NotTo(HaveOccurred())
					return cLogs.String()
				}
				Consistently(cLogs).ShouldNot(ContainSubstring("postinstall-file"))
			})
		})
	})
}
