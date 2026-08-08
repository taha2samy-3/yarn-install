package pnpminstall

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"

	"github.com/paketo-buildpacks/libnodejs"
	"github.com/paketo-buildpacks/packit/v2"
	"github.com/paketo-buildpacks/packit/v2/fs"
)

type BuildPlanMetadata struct {
	Version       string `toml:"version"`
	VersionSource string `toml:"version-source"`
	Build         bool   `toml:"build"`
	Launch        bool   `toml:"launch"`
}

func Detect() packit.DetectFunc {
	return func(context packit.DetectContext) (packit.DetectResult, error) {
		projectPath, err := libnodejs.FindProjectPath(context.WorkingDir)
		if err != nil {
			return packit.DetectResult{}, err
		}

		exists, err := fs.Exists(filepath.Join(projectPath, "pnpm-lock.yaml"))
		if err != nil {
			return packit.DetectResult{}, err
		}

		if !exists {
			return packit.DetectResult{}, packit.Fail.WithMessage("no 'pnpm-lock.yaml' file found in the project path %s", projectPath)
		}

		pkg, err := libnodejs.ParsePackageJSON(projectPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return packit.DetectResult{}, packit.Fail.WithMessage("no 'package.json' found in project path %s", filepath.Join(projectPath))
			}

			return packit.DetectResult{}, err
		}
		nodeVersion := pkg.GetVersion()

		nodeRequirement := packit.BuildPlanRequirement{
			Name: PlanDependencyNode,
			Metadata: BuildPlanMetadata{
				Build: true,
			},
		}

		if nodeVersion != "" {
			nodeRequirement.Metadata = BuildPlanMetadata{
				Version:       nodeVersion,
				VersionSource: "package.json",
				Build:         true,
			}
		}

		// Evaluate runtime launch requirement for pnpm (defaults to true)
		pnpmLaunch := true
		if envLaunch := os.Getenv("BP_PNPM_IN_LAUNCH"); envLaunch != "" {
			if parsed, err := strconv.ParseBool(envLaunch); err == nil {
				pnpmLaunch = parsed
			}
		}

		return packit.DetectResult{
			Plan: packit.BuildPlan{
				Provides: []packit.BuildPlanProvision{
					{Name: PlanDependencyNodeModules},
				},
				Requires: []packit.BuildPlanRequirement{
					nodeRequirement,
					{
						Name: PlanDependencyPnpm,
						Metadata: BuildPlanMetadata{
							Build:  true,
							Launch: pnpmLaunch,
						},
					},
				},
			},
		}, nil
	}
}
