package integration_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/onsi/gomega/format"
	"github.com/paketo-buildpacks/occam"
	"github.com/sclevine/spec"
	"github.com/sclevine/spec/report"

	. "github.com/onsi/gomega"
)

var settings struct {
	Extensions struct {
		UbiNodejsExtension struct {
			Online string
		}
	}
}

var (
	buildpackURI        string
	buildpackOfflineURI string
	nodeURI             string
	nodeOfflineURI      string
	pnpmURI             string
	pnpmOfflineURI      string
	buildPlanURI        string
	pnpmList            string
	buildpackInfo       struct {
		Buildpack struct {
			ID   string
			Name string
		}
	}
)

func TestIntegration(t *testing.T) {
	var Expect = NewWithT(t).Expect
	format.MaxLength = 0

	fmt.Println()
	fmt.Println("==================================================")
	fmt.Println("=== [DEBUG] Starting Integration Test Diagnostics ===")
	fmt.Printf("[DEBUG] Current DOCKER_CONFIG: %s\n", os.Getenv("DOCKER_CONFIG"))
	fmt.Printf("[DEBUG] Current HOME: %s\n", os.Getenv("HOME"))

	dockerConfigPath := filepath.Join(os.Getenv("HOME"), ".docker", "config.json")
	if customConfig := os.Getenv("DOCKER_CONFIG"); customConfig != "" {
		dockerConfigPath = filepath.Join(customConfig, "config.json")
	}

	fmt.Printf("[DEBUG] Checking Docker config path at: %s\n", dockerConfigPath)
	if fileInfo, err := os.Stat(dockerConfigPath); err == nil {
		fmt.Printf("[DEBUG] config.json exists! Size: %d bytes\n", fileInfo.Size())
		content, readErr := os.ReadFile(dockerConfigPath)
		if readErr == nil {
			hasGHCR := strings.Contains(string(content), "ghcr.io")
			fmt.Printf("[DEBUG] Does config.json contain references to ghcr.io? %t\n", hasGHCR)
		} else {
			fmt.Printf("[DEBUG] Failed to read config.json: %v\n", readErr)
		}
	} else {
		fmt.Println("[DEBUG] config.json does not exist at this path.")
	}
	fmt.Println("==================================================")
	fmt.Println()

	var config struct {
		BuildPlan          string `json:"build-plan"`
		NodeEngine         string `json:"node-engine"`
		Pnpm               string `json:"pnpm"`
		UbiNodejsExtension string `json:"ubi-nodejs-extension"`
	}

	file, err := os.Open("./../integration.json")
	Expect(err).ToNot(HaveOccurred())

	Expect(json.NewDecoder(file).Decode(&config)).To(Succeed())

	fmt.Printf("[DEBUG] Configuration loaded from integration.json:\n")
	fmt.Printf("        - BuildPlan: %s\n", config.BuildPlan)
	fmt.Printf("        - NodeEngine: %s\n", config.NodeEngine)
	fmt.Printf("        - Pnpm: %s\n", config.Pnpm)
	fmt.Printf("        - UbiNodejsExtension: %s\n\n", config.UbiNodejsExtension)

	root, err := filepath.Abs("./..")
	Expect(err).NotTo(HaveOccurred())

	file, err = os.Open("../buildpack.toml")
	Expect(err).NotTo(HaveOccurred())

	_, err = toml.NewDecoder(file).Decode(&buildpackInfo)
	Expect(err).NotTo(HaveOccurred())

	buildpackStore := occam.NewBuildpackStore()
	pack := occam.NewPack()

	builder, err := pack.Builder.Inspect.Execute()
	Expect(err).NotTo(HaveOccurred())

	if builder.BuilderName == "paketobuildpacks/builder-ubi8-buildpackless-base" || builder.BuilderName == "paketobuildpacks/ubi-9-builder-buildpackless" {
		fmt.Printf("[DEBUG] Fetching UbiNodejsExtension from: %s\n", config.UbiNodejsExtension)
		settings.Extensions.UbiNodejsExtension.Online, err = buildpackStore.Get.
			Execute(config.UbiNodejsExtension)
		Expect(err).ToNot(HaveOccurred())
	}

	fmt.Println("[DEBUG] Fetching main buildpackURI...")
	buildpackURI, err = buildpackStore.Get.
		WithVersion("0.0.1").
		Execute(root)
	Expect(err).ToNot(HaveOccurred())

	fmt.Println("[DEBUG] Fetching buildpackOfflineURI...")
	buildpackOfflineURI, err = buildpackStore.Get.
		WithOfflineDependencies().
		WithVersion("0.0.1").
		Execute(root)
	Expect(err).ToNot(HaveOccurred())

	fmt.Printf("[DEBUG] Fetching NodeEngine from: %s\n", config.NodeEngine)
	nodeURI, err = buildpackStore.Get.Execute(config.NodeEngine)
	Expect(err).ToNot(HaveOccurred())

	fmt.Println("[DEBUG] Fetching NodeOfflineURI...")
	nodeOfflineURI, err = buildpackStore.Get.
		WithOfflineDependencies().
		Execute(config.NodeEngine)
	Expect(err).ToNot(HaveOccurred())

	fmt.Printf("[DEBUG] Attempting to pull PNPM buildpack from: %s\n", config.Pnpm)
	pnpmURI, err = buildpackStore.Get.Execute(config.Pnpm)
	if err != nil {
		fmt.Printf("[DEBUG] PNPM pull failed! Error: %v\n", err)
	} else {
		fmt.Printf("[DEBUG] PNPM pulled successfully! URI path: %s\n", pnpmURI)
	}
	Expect(err).ToNot(HaveOccurred())

	fmt.Println("[DEBUG] Fetching PNPM Offline...")
	pnpmOfflineURI, err = buildpackStore.Get.
		WithOfflineDependencies().
		Execute(config.Pnpm)
	Expect(err).ToNot(HaveOccurred())

	fmt.Printf("[DEBUG] Fetching BuildPlan from: %s\n", config.BuildPlan)
	buildPlanURI, err = buildpackStore.Get.
		Execute(config.BuildPlan)
	Expect(err).NotTo(HaveOccurred())

	pnpmList = filepath.Join(root, "integration", "testdata", "pnpm-list-buildpack")

	SetDefaultEventuallyTimeout(10 * time.Minute)

	suite := spec.New("Integration", spec.Parallel(), spec.Report(report.Terminal{}))
	suite("Caching", testCaching)
	suite("DevDependenciesDuringBuild", testDevDependenciesDuringBuild)
	suite("Logging", testLogging)
	suite("ModuleBinaries", testModuleBinaries)
	suite("NoHoist", testNoHoist)
	suite("PreGyp", testPreGyp)
	suite("ProjectPathApp", testProjectPathApp)
	suite("ServiceBindings", testServiceBindings)
	suite("SimpleApp", testSimpleApp)
	suite("Vendored", testVendored)
	suite("Workspaces", testWorkspaces)
	suite.Run(t)
}