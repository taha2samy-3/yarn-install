package main

import (
	"log"
	"os"

	"github.com/paketo-buildpacks/packit/v2"
	"github.com/paketo-buildpacks/packit/v2/chronos"
	"github.com/paketo-buildpacks/packit/v2/draft"
	"github.com/paketo-buildpacks/packit/v2/fs"
	"github.com/paketo-buildpacks/packit/v2/pexec"
	"github.com/paketo-buildpacks/packit/v2/sbom"
	"github.com/paketo-buildpacks/packit/v2/scribe"
	"github.com/paketo-buildpacks/packit/v2/servicebindings"

	pnpminstall "github.com/paketo-buildpacks/pnpm-install"
)

type SBOMGenerator struct{}

func (s SBOMGenerator) Generate(path string) (sbom.SBOM, error) {
	return sbom.Generate(path)
}

func main() {
	logger := scribe.NewEmitter(os.Stdout).WithLevel(os.Getenv("BP_LOG_LEVEL"))
	installProcess := pnpminstall.NewPnpmInstallProcess(pexec.NewExecutable("pnpm"), fs.NewChecksumCalculator(), logger)
	sbomGenerator := SBOMGenerator{}
	symlinker := pnpminstall.NewSymlinker()
	packageManagerConfigurationManager := pnpminstall.NewPackageManagerConfigurationManager(servicebindings.NewResolver(), logger)
	entryResolver := draft.NewPlanner()
	home, err := os.UserHomeDir()
	tmpDir := os.TempDir()
	if err != nil {
		// not tested
		log.Fatal(err)
	}

	packit.Run(
		pnpminstall.Detect(),
		pnpminstall.Build(entryResolver,
			packageManagerConfigurationManager,
			home,
			symlinker,
			installProcess,
			sbomGenerator,
			chronos.DefaultClock,
			logger,
			tmpDir,
		),
	)
}
