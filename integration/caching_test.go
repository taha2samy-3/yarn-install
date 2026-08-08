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

func testCaching(t *testing.T, context spec.G, it spec.S) {
	var (
		Expect     = NewWithT(t).Expect
		Eventually = NewWithT(t).Eventually

		pack   occam.Pack
		docker occam.Docker

		imageIDs     map[string]struct{}
		containerIDs map[string]struct{}

		name   string
		source string

		pullPolicy       = "never"
		extenderBuildStr = ""
	)

	it.Before(func() {
		imageIDs = make(map[string]struct{})
		containerIDs = make(map[string]struct{})

		pack = occam.NewPack()
		docker = occam.NewDocker()

		var err error
		name, err = occam.RandomName()
		Expect(err).NotTo(HaveOccurred())

		if settings.Extensions.UbiNodejsExtension.Online != "" {
			pullPolicy = "always"
			extenderBuildStr = "[extender (build)] "
		}
	})

	it.After(func() {
		for id := range containerIDs {
			Expect(docker.Container.Remove.Execute(id)).To(Succeed())
		}

		for id := range imageIDs {
			Expect(docker.Image.Remove.Execute(id)).To(Succeed())
		}

		Expect(docker.Volume.Remove.Execute(occam.CacheVolumeNames(name))).To(Succeed())
		Expect(os.RemoveAll(source)).To(Succeed())
	})

	context("when NODE_ENV changes", func() {
		it("does not reuse the node_modules layer", func() {
			var err error
			var container occam.Container

			source, err = occam.Source(filepath.Join("testdata", "dev_dependencies"))
			Expect(err).NotTo(HaveOccurred())

			build := pack.WithNoColor().Build.
				WithExtensions(
					settings.Extensions.UbiNodejsExtension.Online,
				).
				WithPullPolicy(pullPolicy).
				WithEnv(map[string]string{"NODE_ENV": "development"}).
				WithBuildpacks(nodeURI, pnpmURI, buildpackURI, buildPlanURI)

			firstImage, firstLogs, err := build.Execute(name, source)
			Expect(err).NotTo(HaveOccurred(), firstLogs.String)

			imageIDs[firstImage.ID] = struct{}{}

			Expect(firstImage.Buildpacks).To(HaveLen(4))
			Expect(firstImage.Buildpacks[2].Key).To(Equal(buildpackInfo.Buildpack.ID))
			Expect(firstImage.Buildpacks[2].Layers).To(HaveKey("launch-modules"))

			container, err = docker.Container.Run.
				WithCommand(fmt.Sprintf("ls -alR /layers/%s/launch-modules/node_modules", strings.ReplaceAll(buildpackInfo.Buildpack.ID, "/", "_"))).
				Execute(firstImage.ID)
			Expect(err).NotTo(HaveOccurred())

			containerIDs[container.ID] = struct{}{}

			Eventually(func() string {
				cLogs, err := docker.Container.Logs.Execute(container.ID)
				Expect(err).NotTo(HaveOccurred())
				return cLogs.String()
			}).Should(ContainSubstring("leftpad"))

			secondImage, secondLogs, err := build.
				WithEnv(map[string]string{"NODE_ENV": "production"}).
				Execute(name, source)
			Expect(err).NotTo(HaveOccurred(), secondLogs.String)

			imageIDs[secondImage.ID] = struct{}{}

			Expect(secondImage.Buildpacks).To(HaveLen(4))
			Expect(secondImage.Buildpacks[2].Key).To(Equal(buildpackInfo.Buildpack.ID))
			Expect(secondImage.Buildpacks[2].Layers).To(HaveKey("launch-modules"))

			container, err = docker.Container.Run.
				WithCommand(fmt.Sprintf("ls -alR /layers/%s/launch-modules/node_modules", strings.ReplaceAll(buildpackInfo.Buildpack.ID, "/", "_"))).
				Execute(secondImage.ID)
			Expect(err).NotTo(HaveOccurred())

			containerIDs[container.ID] = struct{}{}

			Eventually(func() string {
				cLogs, err := docker.Container.Logs.Execute(container.ID)
				Expect(err).NotTo(HaveOccurred())
				return cLogs.String()
			}).ShouldNot(ContainSubstring("leftpad"))

			Expect(secondImage.Buildpacks[2].Layers["launch-modules"].SHA).ToNot(Equal(firstImage.Buildpacks[2].Layers["launch-modules"].SHA))
			Expect(secondImage.Buildpacks[2].Layers["launch-modules"].Metadata["cache_sha"]).ToNot(Equal(firstImage.Buildpacks[2].Layers["launch-modules"].Metadata["cache_sha"]))

			Expect(secondLogs.String()).ToNot(ContainSubstring(
				fmt.Sprintf("Reusing cached layer /layers/%s/modules",
					strings.ReplaceAll(buildpackInfo.Buildpack.ID, "/", "_"))))
		})
	})

	context("when the pnpm environment changes", func() {
		it("does not reuse the node_modules layer", func() {
			var err error
			var container occam.Container

			source, err = occam.Source(filepath.Join("testdata", "simple_app"))
			Expect(err).NotTo(HaveOccurred())

			build := pack.WithNoColor().Build.
				WithExtensions(
					settings.Extensions.UbiNodejsExtension.Online,
				).
				WithPullPolicy(pullPolicy).
				WithEnv(map[string]string{"NPM_CONFIG_IGNORE_SCRIPTS": "true"}).
				WithBuildpacks(nodeURI, pnpmURI, buildpackURI, buildPlanURI)

			firstImage, firstLogs, err := build.Execute(name, source)
			Expect(err).NotTo(HaveOccurred(), firstLogs.String)

			imageIDs[firstImage.ID] = struct{}{}

			Expect(firstImage.Buildpacks).To(HaveLen(4))
			Expect(firstImage.Buildpacks[2].Key).To(Equal(buildpackInfo.Buildpack.ID))
			Expect(firstImage.Buildpacks[2].Layers).To(HaveKey("launch-modules"))

			container, err = docker.Container.Run.
				WithCommand(fmt.Sprintf("ls -alR /layers/%s/launch-modules/node_modules", strings.ReplaceAll(buildpackInfo.Buildpack.ID, "/", "_"))).
				Execute(firstImage.ID)
			Expect(err).NotTo(HaveOccurred())

			containerIDs[container.ID] = struct{}{}

			Eventually(func() string {
				cLogs, err := docker.Container.Logs.Execute(container.ID)
				Expect(err).NotTo(HaveOccurred())
				return cLogs.String()
			}).Should(ContainSubstring("leftpad"))

			secondImage, secondLogs, err := build.
				WithEnv(map[string]string{"NPM_CONFIG_IGNORE_SCRIPTS": "false"}).
				Execute(name, source)
			Expect(err).NotTo(HaveOccurred(), secondLogs.String)

			imageIDs[secondImage.ID] = struct{}{}

			Expect(secondImage.Buildpacks).To(HaveLen(4))
			Expect(secondImage.Buildpacks[2].Key).To(Equal(buildpackInfo.Buildpack.ID))
			Expect(secondImage.Buildpacks[2].Layers).To(HaveKey("launch-modules"))

			container, err = docker.Container.Run.
				WithCommand(fmt.Sprintf("ls -alR /layers/%s/launch-modules/node_modules", strings.ReplaceAll(buildpackInfo.Buildpack.ID, "/", "_"))).
				Execute(secondImage.ID)
			Expect(err).NotTo(HaveOccurred())

			containerIDs[container.ID] = struct{}{}

			Eventually(func() string {
				cLogs, err := docker.Container.Logs.Execute(container.ID)
				Expect(err).NotTo(HaveOccurred())
				return cLogs.String()
			}).Should(ContainSubstring("leftpad"))

			Expect(secondImage.Buildpacks[2].Layers["launch-modules"].SHA).ToNot(Equal(firstImage.Buildpacks[2].Layers["launch-modules"].SHA))
			Expect(secondImage.Buildpacks[2].Layers["launch-modules"].Metadata["cache_sha"]).ToNot(Equal(firstImage.Buildpacks[2].Layers["launch-modules"].Metadata["cache_sha"]))

			Expect(secondLogs.String()).ToNot(ContainSubstring(
				fmt.Sprintf("Reusing cached layer /layers/%s/modules",
					strings.ReplaceAll(buildpackInfo.Buildpack.ID, "/", "_"))))
		})
	})

	context("a fixture is pushed twice", func() {
		context("online", func() {
			it("uses and validates the cached node_modules layer", func() {
				var err error
				var container occam.Container

				source, err = occam.Source(filepath.Join("testdata", "simple_app"))
				Expect(err).NotTo(HaveOccurred())

				build := pack.WithNoColor().Build.
					WithExtensions(
						settings.Extensions.UbiNodejsExtension.Online,
					).
					WithPullPolicy(pullPolicy).
					WithBuildpacks(nodeURI, pnpmURI, buildpackURI, buildPlanURI)

				t.Logf("[DEBUG] Executing the first build...")
				firstImage, firstLogs, err := build.Execute(name, source)
				Expect(err).NotTo(HaveOccurred(), firstLogs.String)

				imageIDs[firstImage.ID] = struct{}{}

				t.Logf("[DEBUG] First Image ID: %s", firstImage.ID)

				Expect(firstImage.Buildpacks).To(HaveLen(4))
				Expect(firstImage.Buildpacks[2].Key).To(Equal(buildpackInfo.Buildpack.ID))
				Expect(firstImage.Buildpacks[2].Layers).To(HaveKey("launch-modules"))

				firstLayer := firstImage.Buildpacks[2].Layers["launch-modules"]
				t.Logf("[DEBUG] First Build - launch-modules Layer SHA: %s", firstLayer.SHA)
				t.Logf("[DEBUG] First Build - launch-modules Layer cache_sha Metadata: %v", firstLayer.Metadata["cache_sha"])

				container, err = docker.Container.Run.
					WithCommand(fmt.Sprintf("ls -alR /layers/%s/launch-modules/node_modules", strings.ReplaceAll(buildpackInfo.Buildpack.ID, "/", "_"))).
					Execute(firstImage.ID)
				Expect(err).NotTo(HaveOccurred())

				containerIDs[container.ID] = struct{}{}

				Eventually(func() string {
					cLogs, err := docker.Container.Logs.Execute(container.ID)
					Expect(err).NotTo(HaveOccurred())
					return cLogs.String()
				}).Should(ContainSubstring("leftpad"))

				t.Logf("[DEBUG] Executing the second build (which should reuse the cache)...")
				secondImage, secondLogs, err := build.Execute(name, source)
				Expect(err).NotTo(HaveOccurred(), secondLogs.String)

				imageIDs[secondImage.ID] = struct{}{}

				t.Logf("[DEBUG] Second Image ID: %s", secondImage.ID)

				Expect(secondImage.Buildpacks).To(HaveLen(4))
				Expect(secondImage.Buildpacks[2].Key).To(Equal(buildpackInfo.Buildpack.ID))
				Expect(secondImage.Buildpacks[2].Layers).To(HaveKey("launch-modules"))

				secondLayer := secondImage.Buildpacks[2].Layers["launch-modules"]
				t.Logf("[DEBUG] Second Build - launch-modules Layer SHA: %s", secondLayer.SHA)
				t.Logf("[DEBUG] Second Build - launch-modules Layer cache_sha Metadata: %v", secondLayer.Metadata["cache_sha"])

				container, err = docker.Container.Run.
					WithCommand(fmt.Sprintf("ls -alR /layers/%s/launch-modules/node_modules", strings.ReplaceAll(buildpackInfo.Buildpack.ID, "/", "_"))).
					Execute(secondImage.ID)
				Expect(err).NotTo(HaveOccurred())

				containerIDs[container.ID] = struct{}{}

				Eventually(func() string {
					cLogs, err := docker.Container.Logs.Execute(container.ID)
					Expect(err).NotTo(HaveOccurred())
					return cLogs.String()
				}).Should(ContainSubstring("leftpad"))

				t.Logf("[DEBUG] Comparing the layer properties...")
				Expect(secondLayer.SHA).To(Equal(firstLayer.SHA))
				Expect(secondLayer.Metadata["cache_sha"]).To(Equal(firstLayer.Metadata["cache_sha"]))
				t.Logf("[DEBUG] Layer SHA and metadata cache_sha match. Cache reuse is cryptographically verified.")

				t.Logf("[DEBUG] Checking if the second build output reused the cached layer...")
				Expect(secondLogs).To(ContainLines(
					fmt.Sprintf("%s%s %s", extenderBuildStr, buildpackInfo.Buildpack.Name, "1.2.3"),
					extenderBuildStr+"  Resolving installation process",
					extenderBuildStr+"    Process inputs:",
					extenderBuildStr+"      pnpm-lock.yaml -> Found",
					extenderBuildStr+"",
					fmt.Sprintf(extenderBuildStr+"  Reusing cached layer /layers/%s/launch-modules", strings.ReplaceAll(buildpackInfo.Buildpack.ID, "/", "_")),
				))
				t.Logf("[DEBUG] Buildpack successfully skipped installation and reused the cached launch-modules layer.")
			})
		})
	})
}
