package pnpminstall

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/paketo-buildpacks/libnodejs"
	"github.com/paketo-buildpacks/packit/v2"
	"github.com/paketo-buildpacks/packit/v2/chronos"
	"github.com/paketo-buildpacks/packit/v2/sbom"
	"github.com/paketo-buildpacks/packit/v2/scribe"
)

// SymlinkManager defines the interface for managing symbolic links on the filesystem.
//
//go:generate faux --interface SymlinkManager --output fakes/symlink_manager.go
type SymlinkManager interface {
	Link(oldname, newname string) error
	Unlink(path string) error
}

// InstallProcess defines the interface for handling the dependencies installation logic.
//
//go:generate faux --interface InstallProcess --output fakes/install_process.go
type InstallProcess interface {
	ShouldRun(workingDir string, metadata map[string]interface{}) (run bool, sha string, err error)
	SetupModules(workingDir, currentModulesLayerPath, nextModulesLayerPath string) (string, error)
	Execute(workingDir, modulesLayerPath string, launch bool) error
}

// EntryResolver defines the interface for resolving requested buildpack plan entries.
//
//go:generate faux --interface EntryResolver --output fakes/entry_resolver.go
type EntryResolver interface {
	MergeLayerTypes(string, []packit.BuildpackPlanEntry) (launch, build bool)
}

// SBOMGenerator defines the interface for generating Software Bill of Materials (SBOM).
//
//go:generate faux --interface SBOMGenerator --output fakes/sbom_generator.go
type SBOMGenerator interface {
	Generate(dir string) (sbom.SBOM, error)
}

// ConfigurationManager defines the interface for finding binding configuration files.
//
//go:generate faux --interface ConfigurationManager --output fakes/configuration_manager.go
type ConfigurationManager interface {
	DeterminePath(typ, platformDir, entry string) (path string, err error)
}

// Build returns the packit.BuildFunc that executes the build phase of the buildpack.
func Build(entryResolver EntryResolver,
	configurationManager ConfigurationManager,
	homeDir string,
	symlinker SymlinkManager,
	installProcess InstallProcess,
	sbomGenerator SBOMGenerator,
	clock chronos.Clock,
	logger scribe.Emitter,
	tmpDir string) packit.BuildFunc {
	return func(context packit.BuildContext) (result packit.BuildResult, err error) {
		logger.Title("%s %s", context.BuildpackInfo.Name, context.BuildpackInfo.Version)

		// 1. Resolve the project path where package.json and pnpm-lock.yaml reside.
		projectPath, err := libnodejs.FindProjectPath(context.WorkingDir)
		if err != nil {
			return packit.BuildResult{}, err
		}

		// 2. Resolve and symlink the global .npmrc file if provided via service bindings.
		globalNpmrcPath, err := configurationManager.DeterminePath("npmrc", context.Platform.Path, ".npmrc")
		if err != nil {
			return packit.BuildResult{}, err
		}

		if globalNpmrcPath != "" {
			err = symlinker.Link(globalNpmrcPath, filepath.Join(homeDir, ".npmrc"))
			if err != nil {
				return packit.BuildResult{}, err
			}
		}
		defer func() {
			if globalNpmrcPath != "" {
				unlinkErr := symlinker.Unlink(filepath.Join(homeDir, ".npmrc"))
				if err == nil {
					err = unlinkErr
				}
			}
		}()

		// 3. Resolve and symlink the global .pnpmrc file if provided via service bindings.
		// pnpm v10+ reads configuration from .npmrc, so we also link to .npmrc when no npmrc binding exists.
		globalPnpmrcPath, err := configurationManager.DeterminePath("pnpmrc", context.Platform.Path, ".pnpmrc")
		if err != nil {
			return packit.BuildResult{}, err
		}

		if globalPnpmrcPath != "" {
			err = symlinker.Link(globalPnpmrcPath, filepath.Join(homeDir, ".pnpmrc"))
			if err != nil {
				return packit.BuildResult{}, err
			}
			if globalNpmrcPath == "" {
				err = symlinker.Link(globalPnpmrcPath, filepath.Join(homeDir, ".npmrc"))
				if err != nil {
					return packit.BuildResult{}, err
				}
			}
		}
		defer func() {
			if globalPnpmrcPath != "" {
				unlinkErr := symlinker.Unlink(filepath.Join(homeDir, ".pnpmrc"))
				if err == nil {
					err = unlinkErr
				}
				if globalNpmrcPath == "" {
					unlinkErr := symlinker.Unlink(filepath.Join(homeDir, ".npmrc"))
					if err == nil {
						err = unlinkErr
					}
				}
			}
		}()

		// 4. Merge requested build and launch layer requirements.
		launch, build := entryResolver.MergeLayerTypes(PlanDependencyNodeModules, context.Plan.Entries)

		sbomDisabled, err := checkSbomDisabled()
		if err != nil {
			return packit.BuildResult{}, err
		}

		// 5. Helper closure to generate and cache the Software Bill of Materials (SBOM).
		var projectSBOM *sbom.SBOM
		generateSBOM := func() (sbom.SBOM, error) {
			if projectSBOM != nil {
				return *projectSBOM, nil
			}
			logger.GeneratingSBOM(context.WorkingDir)
			var sbomContent sbom.SBOM
			duration, err := clock.Measure(func() error {
				var err error
				sbomContent, err = sbomGenerator.Generate(context.WorkingDir)
				return err
			})
			if err != nil {
				return sbom.SBOM{}, err
			}
			logger.Action("Completed in %s", duration.Round(time.Millisecond))
			logger.Break()
			projectSBOM = &sbomContent
			return sbomContent, nil
		}

		var layers []packit.Layer
		var currentModLayer string

		// 6. Handle build-time node_modules dependencies.
		if build {
			layer, err := context.Layers.Get("build-modules")
			if err != nil {
				return packit.BuildResult{}, err
			}

			logger.Process("Resolving installation process")

			run, sha, err := installProcess.ShouldRun(projectPath, layer.Metadata)
			if err != nil {
				return packit.BuildResult{}, err
			}

			if run {
				logger.Subprocess("Selected default build process: 'pnpm install'")
				logger.Break()
				logger.Process("Executing build environment install process")

				layer, err = layer.Reset()
				if err != nil {
					return packit.BuildResult{}, err
				}

				currentModLayer, err = installProcess.SetupModules(projectPath, currentModLayer, layer.Path)
				if err != nil {
					return packit.BuildResult{}, err
				}

				duration, err := clock.Measure(func() error {
					return installProcess.Execute(projectPath, layer.Path, false)
				})
				if err != nil {
					return packit.BuildResult{}, err
				}

				logger.Action("Completed in %s", duration.Round(time.Millisecond))
				logger.Break()

				layer.Metadata = map[string]interface{}{
					"cache_sha": sha,
				}

				err = ensureNodeModulesSymlink(projectPath, layer.Path, tmpDir)
				if err != nil {
					return packit.BuildResult{}, err
				}

				path := filepath.Join(layer.Path, "node_modules", ".bin")
				layer.BuildEnv.Append("PATH", path, string(os.PathListSeparator))
				layer.BuildEnv.Override("NODE_ENV", "development")

				logger.EnvironmentVariables(layer)

				if sbomDisabled {
					logger.Subprocess("Skipping SBOM generation for PNPM Install")
					logger.Break()

				} else {
					sbomContent, err := generateSBOM()
					if err != nil {
						return packit.BuildResult{}, err
					}

					logger.FormattingSBOM(context.BuildpackInfo.SBOMFormats...)
					layer.SBOM, err = sbomContent.InFormats(context.BuildpackInfo.SBOMFormats...)
					if err != nil {
						return packit.BuildResult{}, err
					}
				}
			} else {
				logger.Process("Reusing cached layer %s", layer.Path)

				err = ensureNodeModulesSymlink(projectPath, layer.Path, tmpDir)
				if err != nil {
					return packit.BuildResult{}, err
				}
			}

			layer.Build = true
			layer.Cache = true

			layers = append(layers, layer)
		}

		// 7. Handle runtime launch-time node_modules dependencies.
		if launch {
			layer, err := context.Layers.Get("launch-modules")
			if err != nil {
				return packit.BuildResult{}, err
			}

			logger.Process("Resolving installation process")

			run, sha, err := installProcess.ShouldRun(projectPath, layer.Metadata)
			if err != nil {
				return packit.BuildResult{}, err
			}

			if run {
				logger.Subprocess("Selected default build process: 'pnpm install'")
				logger.Break()
				logger.Process("Executing launch environment install process")

				layer, err = layer.Reset()
				if err != nil {
					return packit.BuildResult{}, err
				}

				_, err = installProcess.SetupModules(projectPath, currentModLayer, layer.Path)
				if err != nil {
					return packit.BuildResult{}, err
				}

				duration, err := clock.Measure(func() error {
					return installProcess.Execute(projectPath, layer.Path, true)
				})
				if err != nil {
					return packit.BuildResult{}, err
				}

				logger.Action("Completed in %s", duration.Round(time.Millisecond))
				logger.Break()

				err = ensureNodeModulesSymlink(projectPath, layer.Path, tmpDir)
				if err != nil {
					return packit.BuildResult{}, err
				}

				layer.Metadata = map[string]interface{}{
					"cache_sha": sha,
				}

				path := filepath.Join(layer.Path, "node_modules", ".bin")
				layer.LaunchEnv.Append("PATH", path, string(os.PathListSeparator))
				layer.LaunchEnv.Default("NODE_PROJECT_PATH", projectPath)

				logger.EnvironmentVariables(layer)

				if sbomDisabled {
					logger.Subprocess("Skipping SBOM generation for PNPM Install")
					logger.Break()

				} else {
					sbomContent, err := generateSBOM()
					if err != nil {
						return packit.BuildResult{}, err
					}

					logger.FormattingSBOM(context.BuildpackInfo.SBOMFormats...)
					layer.SBOM, err = sbomContent.InFormats(context.BuildpackInfo.SBOMFormats...)
					if err != nil {
						return packit.BuildResult{}, err
					}
				}

				layer.ExecD = []string{filepath.Join(context.CNBPath, "bin", "setup-symlinks")}

			} else {
				logger.Process("Reusing cached layer %s", layer.Path)
				err = ensureNodeModulesSymlink(projectPath, layer.Path, tmpDir)
				if err != nil {
					return packit.BuildResult{}, err
				}
				layer.ExecD = []string{filepath.Join(context.CNBPath, "bin", "setup-symlinks")}
			}

			layer.Launch = true
			layer.Cache = true // Ensures launch-modules layer is cached and restored across builds.

			layers = append(layers, layer)

		}

		return packit.BuildResult{
			Layers: layers,
		}, nil
	}
}

func checkSbomDisabled() (bool, error) {
	if disableStr, ok := os.LookupEnv("BP_DISABLE_SBOM"); ok {
		disable, err := strconv.ParseBool(disableStr)
		if err != nil {
			return false, fmt.Errorf("failed to parse BP_DISABLE_SBOM value %s: %w", disableStr, err)
		}
		return disable, nil
	}
	return false, nil
}

func ensureNodeModulesSymlink(projectDir, targetLayer, tmpDir string) error {
	projectDirNodeModules := filepath.Join(projectDir, "node_modules")
	layerNodeModules := filepath.Join(targetLayer, "node_modules")
	tmpNodeModules := filepath.Join(tmpDir, "node_modules")

	for _, d := range []string{projectDirNodeModules, tmpNodeModules} {
		err := os.RemoveAll(d)
		if err != nil {
			return err
		}
	}

	err := os.Symlink(tmpNodeModules, projectDirNodeModules)
	if err != nil {
		return err
	}

	err = os.Symlink(layerNodeModules, tmpNodeModules)
	if err != nil {
		return err
	}

	return nil
}
