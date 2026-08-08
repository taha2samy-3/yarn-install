package integration_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paketo-buildpacks/occam"
	"github.com/sclevine/spec"

	. "github.com/onsi/gomega"
	. "github.com/paketo-buildpacks/occam/matchers"
)

func testLogging(t *testing.T, context spec.G, it spec.S) {
	var (
		Expect = NewWithT(t).Expect

		pack   occam.Pack
		docker occam.Docker

		pullPolicy              = "never"
		extenderBuildStr        = ""
		extenderBuildStrEscaped = ""
	)

	it.Before(func() {
		pack = occam.NewPack()
		docker = occam.NewDocker()

		if settings.Extensions.UbiNodejsExtension.Online != "" {
			pullPolicy = "always"
			extenderBuildStr = "[extender (build)] "
			extenderBuildStrEscaped = `\[extender \(build\)\] `
		}
	})

	context("when app is NOT vendored", func() {
		var (
			image occam.Image

			name   string
			source string
		)

		it.Before(func() {
			var err error
			name, err = occam.RandomName()
			Expect(err).NotTo(HaveOccurred())
		})

		it.After(func() {
			Expect(docker.Image.Remove.Execute(image.ID)).To(Succeed())
			Expect(docker.Volume.Remove.Execute(occam.CacheVolumeNames(name))).To(Succeed())
			Expect(os.RemoveAll(source)).To(Succeed())
		})

		it("should build a working OCI image for a simple app", func() {
			var err error
			source, err = occam.Source(filepath.Join("testdata", "simple_app"))
			Expect(err).NotTo(HaveOccurred())

			t.Logf("[DEBUG] Starting non-vendored simple app build...")
			var logs fmt.Stringer
			image, logs, err = pack.WithNoColor().Build.
				WithExtensions(
					settings.Extensions.UbiNodejsExtension.Online,
				).
				WithBuildpacks(
					nodeURI,
					pnpmURI,
					buildpackURI,
					buildPlanURI,
				).
				WithEnv(map[string]string{"BP_LOG_LEVEL": "DEBUG"}).
				WithPullPolicy(pullPolicy).
				Execute(name, source)
			Expect(err).NotTo(HaveOccurred())

			t.Logf("[DEBUG] Build succeeded. Verifying installation process logs...")
			Expect(logs).To(ContainLines(
				fmt.Sprintf("%s%s %s", extenderBuildStr, buildpackInfo.Buildpack.Name, "0.0.1"),
				extenderBuildStr+"  Resolving installation process",
				extenderBuildStr+"    Process inputs:",
				extenderBuildStr+"      pnpm-lock.yaml -> Found",
				extenderBuildStr+"",
				extenderBuildStr+"    Selected default build process: 'pnpm install'",
				extenderBuildStr+"",
				extenderBuildStr+"  Executing launch environment install process",
				extenderBuildStr+"    Running 'pnpm install --frozen-lockfile --prod'",
			))

			t.Logf("[DEBUG] Verifying launch environment and /workspace SBOM logging...")
			Expect(logs).To(ContainLines(
				extenderBuildStr+"  Configuring launch environment",
				extenderBuildStr+"    NODE_PROJECT_PATH -> \"/workspace\"",
				fmt.Sprintf("%s    PATH              -> \"$PATH:/layers/%s/launch-modules/node_modules/.bin\"", extenderBuildStr, strings.ReplaceAll(buildpackInfo.Buildpack.ID, "/", "_")),
				extenderBuildStr+"",
				extenderBuildStr+"  Generating SBOM for /workspace",
				MatchRegexp(extenderBuildStrEscaped+`      Completed in (\d+)(\.\d+)?(ms|s)`),
				extenderBuildStr+"",
				extenderBuildStr+"  Writing SBOM in the following format(s):",
				extenderBuildStr+"    application/vnd.cyclonedx+json",
				extenderBuildStr+"    application/spdx+json",
				extenderBuildStr+"    application/vnd.syft+json",
			))
			t.Logf("[DEBUG] Simple app logs verified successfully.")
		})
	})

	context("when the app is vendored", func() {
		if settings.Extensions.UbiNodejsExtension.Online != "" {
			return
		}

		var (
			image occam.Image

			name   string
			source string
		)

		it.Before(func() {
			var err error
			name, err = occam.RandomName()
			Expect(err).NotTo(HaveOccurred())
		})

		it.After(func() {
			Expect(docker.Image.Remove.Execute(image.ID)).To(Succeed())
			Expect(docker.Volume.Remove.Execute(occam.CacheVolumeNames(name))).To(Succeed())
			Expect(os.RemoveAll(source)).To(Succeed())
		})

		it("should build a working OCI image for a simple app", func() {
			var err error
			source, err = occam.Source(filepath.Join("testdata", "vendored"))
			Expect(err).NotTo(HaveOccurred())

			t.Logf("[DEBUG] Starting offline vendored app build...")
			var logs fmt.Stringer
			image, logs, err = pack.WithNoColor().Build.
				WithBuildpacks(
					nodeOfflineURI,
					pnpmOfflineURI,
					buildpackOfflineURI,
					buildPlanURI,
				).
				WithNetwork("none").
				WithPullPolicy("never").
				Execute(name, source)
			Expect(err).NotTo(HaveOccurred())

			t.Logf("[DEBUG] Build succeeded. Verifying offline installation logs...")
			Expect(logs).To(ContainLines(
				fmt.Sprintf("%s %s", buildpackInfo.Buildpack.Name, "0.0.1"),
				"  Resolving installation process",
				"    Process inputs:",
				"      pnpm-lock.yaml -> Found",
				"",
				"    Selected default build process: 'pnpm install'",
				"",
				"  Executing launch environment install process",
				"    Running 'pnpm install --frozen-lockfile --offline --prod'",
			))

			t.Logf("[DEBUG] Verifying launch environment and offline /workspace SBOM logging...")
			Expect(logs).To(ContainLines(
				"  Configuring launch environment",
				"    NODE_PROJECT_PATH -> \"/workspace\"",
				fmt.Sprintf("    PATH              -> \"$PATH:/layers/%s/launch-modules/node_modules/.bin\"", strings.ReplaceAll(buildpackInfo.Buildpack.ID, "/", "_")),
				"",
				"  Generating SBOM for /workspace",
				MatchRegexp(`      Completed in (\d+)(\.\d+)?(ms|s)`),
				"",
			))
			t.Logf("[DEBUG] Offline vendored app logs verified successfully.")
		})
	})

	context("when modules are required at build time", func() {
		var (
			image occam.Image

			name   string
			source string
		)

		it.Before(func() {
			var err error
			name, err = occam.RandomName()
			Expect(err).NotTo(HaveOccurred())
		})

		it.After(func() {
			Expect(docker.Image.Remove.Execute(image.ID)).To(Succeed())
			Expect(docker.Volume.Remove.Execute(occam.CacheVolumeNames(name))).To(Succeed())
			Expect(os.RemoveAll(source)).To(Succeed())
		})

		it("should build a working OCI image for a dev dependencies app", func() {
			var err error
			source, err = occam.Source(filepath.Join("testdata", "dev_dependencies_during_build"))
			Expect(err).NotTo(HaveOccurred())

			t.Logf("[DEBUG] Starting build with modules required at build-time and launch-time...")
			var logs fmt.Stringer
			image, logs, err = pack.WithNoColor().Build.
				WithExtensions(
					settings.Extensions.UbiNodejsExtension.Online,
				).
				WithBuildpacks(
					nodeURI,
					pnpmURI,
					buildpackURI,
					buildPlanURI,
				).
				WithEnv(map[string]string{"BP_LOG_LEVEL": "DEBUG"}).
				WithPullPolicy(pullPolicy).
				Execute(name, source)
			Expect(err).NotTo(HaveOccurred())

			t.Logf("[DEBUG] Build succeeded. Verifying build environment installation logs...")
			Expect(logs).To(ContainLines(
				fmt.Sprintf("%s%s %s", extenderBuildStr, buildpackInfo.Buildpack.Name, "0.0.1"),
				extenderBuildStr+"  Resolving installation process",
				extenderBuildStr+"    Process inputs:",
				extenderBuildStr+"      pnpm-lock.yaml -> Found",
				extenderBuildStr+"",
				extenderBuildStr+"    Selected default build process: 'pnpm install'",
				extenderBuildStr+"",
				extenderBuildStr+"  Executing build environment install process",
				extenderBuildStr+"    Running 'pnpm install --frozen-lockfile'",
			))

			t.Logf("[DEBUG] Verifying build environment configuration, /workspace SBOM generation, and launch install initiation...")
			Expect(logs).To(ContainLines(
				extenderBuildStr+"  Configuring build environment",
				extenderBuildStr+`    NODE_ENV -> "development"`,
				fmt.Sprintf("%s    PATH     -> \"$PATH:/layers/%s/build-modules/node_modules/.bin\"", extenderBuildStr, strings.ReplaceAll(buildpackInfo.Buildpack.ID, "/", "_")),
				extenderBuildStr+"",
				extenderBuildStr+"  Generating SBOM for /workspace",
				MatchRegexp(extenderBuildStrEscaped+`      Completed in (\d+)(\.\d+)?(ms|s)`),
				extenderBuildStr+"",
				extenderBuildStr+"  Writing SBOM in the following format(s):",
				extenderBuildStr+"    application/vnd.cyclonedx+json",
				extenderBuildStr+"    application/spdx+json",
				extenderBuildStr+"    application/vnd.syft+json",
				extenderBuildStr+"",
				extenderBuildStr+"  Resolving installation process",
				extenderBuildStr+"    Process inputs:",
				extenderBuildStr+"      pnpm-lock.yaml -> Found",
				extenderBuildStr+"",
				extenderBuildStr+"    Selected default build process: 'pnpm install'",
				extenderBuildStr+"",
				extenderBuildStr+"  Executing launch environment install process",
				extenderBuildStr+"    Running 'pnpm install --frozen-lockfile --prod'",
			))

			t.Logf("[DEBUG] Verifying launch environment configuration and cached SBOM outputs...")
			Expect(logs).To(ContainLines(
				extenderBuildStr+"  Configuring launch environment",
				extenderBuildStr+"    NODE_PROJECT_PATH -> \"/workspace\"",
				fmt.Sprintf("%s    PATH              -> \"$PATH:/layers/%s/launch-modules/node_modules/.bin\"", extenderBuildStr, strings.ReplaceAll(buildpackInfo.Buildpack.ID, "/", "_")),
				extenderBuildStr+"",
				extenderBuildStr+"  Writing SBOM in the following format(s):",
				extenderBuildStr+"    application/vnd.cyclonedx+json",
				extenderBuildStr+"    application/spdx+json",
				extenderBuildStr+"    application/vnd.syft+json",
				extenderBuildStr+"",
			))
			t.Logf("[DEBUG] Build-time and launch-time logs verified successfully.")
		})
	})
}
