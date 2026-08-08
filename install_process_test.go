package pnpminstall_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paketo-buildpacks/packit/v2/pexec"
	"github.com/paketo-buildpacks/packit/v2/scribe"
	pnpminstall "github.com/paketo-buildpacks/pnpm-install"
	"github.com/paketo-buildpacks/pnpm-install/fakes"
	"github.com/sclevine/spec"

	. "github.com/onsi/gomega"
	. "github.com/paketo-buildpacks/occam/matchers"
)

func testInstallProcess(t *testing.T, context spec.G, it spec.S) {
	var Expect = NewWithT(t).Expect

	context("ShouldRun", func() {
		var (
			workingDir     string
			executable     *fakes.Executable
			installProcess pnpminstall.PnpmInstallProcess
			summer         *fakes.Summer
			buffer         *bytes.Buffer
			execution      pexec.Execution
		)

		it.Before(func() {
			var err error
			workingDir, err = os.MkdirTemp("", "working-dir")
			Expect(err).NotTo(HaveOccurred())

			err = os.WriteFile(filepath.Join(workingDir, "config-file"), []byte("hi"), os.ModePerm)
			Expect(err).NotTo(HaveOccurred())

			executable = &fakes.Executable{}
			summer = &fakes.Summer{}
			buffer = bytes.NewBuffer(nil)

			executable.ExecuteCall.Stub = func(exec pexec.Execution) error {
				execution = exec
				_, err := fmt.Fprintln(exec.Stdout, "undefined")
				Expect(err).NotTo(HaveOccurred())
				_, err = fmt.Fprintln(exec.Stderr, "undefined")
				Expect(err).NotTo(HaveOccurred())
				return nil
			}
			installProcess = pnpminstall.NewPnpmInstallProcess(executable, summer, scribe.NewEmitter(buffer))
		})

		context("we should run pnpm install when", func() {
			context("there is no pnpm-lock.yaml file in the workingDir", func() {
				it("succeeds", func() {
					run, sha, err := installProcess.ShouldRun(workingDir, map[string]interface{}{
						"cache_sha": "some-sha",
					})

					Expect(run).To(BeTrue())
					Expect(sha).To(Equal(""))
					Expect(err).NotTo(HaveOccurred())
				})
			})

			context("when the pnpm-lock.yaml or config file has changed", func() {
				it.Before(func() {
					summer.SumCall.Stub = func(...string) (string, error) {
						return "some-other-sha", nil
					}
					Expect(os.WriteFile(filepath.Join(workingDir, "pnpm-lock.yaml"), []byte(""), os.ModePerm)).To(Succeed())
				})

				it("succeeds when sha is different", func() {
					run, sha, err := installProcess.ShouldRun(workingDir, map[string]interface{}{
						"cache_sha": "some-sha",
					})
					Expect(summer.SumCall.Receives.Paths[0]).To(Equal(filepath.Join(workingDir, "pnpm-lock.yaml")))
					Expect(summer.SumCall.Receives.Paths[1]).To(Equal(filepath.Join(workingDir, "package.json")))
					Expect(summer.SumCall.Receives.Paths[2]).To(ContainSubstring("config-file"))
					Expect(run).To(BeTrue())
					Expect(sha).To(Equal("some-other-sha"))
					Expect(err).NotTo(HaveOccurred())
					Expect(execution.Args).To(Equal([]string{
						"config",
						"list",
					}))
					Expect(execution.Dir).To(Equal(workingDir))
				})

				it("succeeds when sha is missing", func() {
					run, sha, err := installProcess.ShouldRun(workingDir, map[string]interface{}{})
					Expect(run).To(BeTrue())
					Expect(sha).To(Equal("some-other-sha"))
					Expect(err).NotTo(HaveOccurred())
				})
			})

			context("when the sha of pnpm-lock.yaml and metadata sha match", func() {
				it.Before(func() {
					summer.SumCall.Stub = func(...string) (string, error) {
						return "some-sha", nil
					}
					Expect(os.WriteFile(filepath.Join(workingDir, "pnpm-lock.yaml"), []byte(""), os.ModePerm)).To(Succeed())
				})

				it("does not run install", func() {
					run, sha, err := installProcess.ShouldRun(workingDir, map[string]interface{}{
						"cache_sha": "some-sha",
					})
					Expect(run).To(BeFalse())
					Expect(sha).To(Equal(""))
					Expect(err).NotTo(HaveOccurred())
				})
			})

			context("failure cases", func() {
				context("when working dir is un-readable", func() {
					it.Before(func() {
						Expect(os.Chmod(workingDir, 0000)).To(Succeed())
					})

					it.After(func() {
						Expect(os.Chmod(workingDir, os.ModePerm)).To(Succeed())
					})

					it("fails", func() {
						_, _, err := installProcess.ShouldRun(workingDir, map[string]interface{}{})
						Expect(err).To(MatchError(ContainSubstring("unable to read pnpm-lock.yaml file:")))
					})
				})

				context("when pnpm config list fails to execute", func() {
					it.Before(func() {
						Expect(os.WriteFile(filepath.Join(workingDir, "pnpm-lock.yaml"), []byte(""), os.ModePerm)).To(Succeed())
						executable.ExecuteCall.Stub = func(execution pexec.Execution) error {
							return errors.New("very bad error")
						}
						installProcess = pnpminstall.NewPnpmInstallProcess(executable, summer, scribe.NewEmitter(bytes.NewBuffer(nil)))
					})

					it("fails", func() {
						_, _, err := installProcess.ShouldRun(workingDir, map[string]interface{}{})
						Expect(err).To(MatchError(ContainSubstring("very bad error")))
						Expect(err).To(MatchError(ContainSubstring("failed to execute pnpm config output")))
					})
				})
			})
		})
	})

	context("SetupModules", func() {
		var (
			workingDir              string
			currentModulesLayerPath string
			nextModulesLayerPath    string
			buffer                  *bytes.Buffer
			executable              *fakes.Executable
			summer                  *fakes.Summer

			installProcess pnpminstall.PnpmInstallProcess
		)

		it.Before(func() {
			var err error
			workingDir, err = os.MkdirTemp("", "working-dir")
			Expect(err).NotTo(HaveOccurred())

			currentModulesLayerPath, err = os.MkdirTemp("", "current-modules-dir")
			Expect(err).NotTo(HaveOccurred())

			nextModulesLayerPath, err = os.MkdirTemp("", "next-modules-dir")
			Expect(err).NotTo(HaveOccurred())

			summer = &fakes.Summer{}
			buffer = bytes.NewBuffer(nil)

			executable = &fakes.Executable{}

			installProcess = pnpminstall.NewPnpmInstallProcess(executable, summer, scribe.NewEmitter(buffer))
		})

		it.After(func() {
			Expect(os.RemoveAll(workingDir)).To(Succeed())
			Expect(os.RemoveAll(currentModulesLayerPath)).To(Succeed())
			Expect(os.RemoveAll(nextModulesLayerPath)).To(Succeed())
		})

		context("when the current node directory is not set", func() {
			context("when there is not a node_modules directory in the working", func() {
				it("makes a node_modules directory in the working dir", func() {
					nextPath, err := installProcess.SetupModules(workingDir, "", nextModulesLayerPath)
					Expect(err).NotTo(HaveOccurred())
					Expect(nextPath).To(Equal(nextModulesLayerPath))

					Expect(filepath.Join(workingDir, "node_modules")).To(BeADirectory())
				})
			})

			context("when there is a node_modules directory in the working dir", func() {
				it.Before(func() {
					Expect(os.MkdirAll(filepath.Join(workingDir, "node_modules"), os.ModePerm)).To(Succeed())

					Expect(os.WriteFile(filepath.Join(workingDir, "node_modules", "some-file"), []byte(""), os.ModePerm)).To(Succeed())
				})
				it("recreates the node_modules directory in the working dir", func() {
					nextPath, err := installProcess.SetupModules(workingDir, "", nextModulesLayerPath)
					Expect(err).NotTo(HaveOccurred())
					Expect(nextPath).To(Equal(nextModulesLayerPath))

					Expect(filepath.Join(workingDir, "node_modules")).To(BeADirectory())
					Expect(filepath.Join(workingDir, "node_modules", "some-file")).NotTo(BeAnExistingFile())
				})
			})
		})

		context("when the current modules directory is set", func() {
			it.Before(func() {
				Expect(os.MkdirAll(filepath.Join(currentModulesLayerPath, "node_modules"), os.ModePerm)).To(Succeed())

				Expect(os.WriteFile(filepath.Join(currentModulesLayerPath, "node_modules", "some-file"), []byte(""), os.ModePerm)).To(Succeed())
			})
			it("copies the contents of the node_modules directory in the current dir into the working dir", func() {
				nextPath, err := installProcess.SetupModules(workingDir, currentModulesLayerPath, nextModulesLayerPath)
				Expect(err).NotTo(HaveOccurred())
				Expect(nextPath).To(Equal(nextModulesLayerPath))

				Expect(filepath.Join(currentModulesLayerPath, "node_modules")).To(BeADirectory())
				Expect(filepath.Join(currentModulesLayerPath, "node_modules", "some-file")).To(BeAnExistingFile())

				Expect(filepath.Join(workingDir, "node_modules")).To(BeADirectory())
				Expect(filepath.Join(workingDir, "node_modules", "some-file")).To(BeAnExistingFile())

			})
		})

		context("failure cases", func() {
			context("when the node_module copy fails", func() {
				it.Before(func() {
					Expect(os.Chmod(currentModulesLayerPath, 0444)).To(Succeed())
				})
				it.After(func() {
					Expect(os.Chmod(currentModulesLayerPath, os.ModePerm)).To(Succeed())
				})
				it("returns an error", func() {
					_, err := installProcess.SetupModules(workingDir, currentModulesLayerPath, nextModulesLayerPath)
					Expect(err).To(MatchError(ContainSubstring("failed to copy cached node_modules to workspace")))
					Expect(err).To(MatchError(ContainSubstring("permission denied")))
				})
			})

			context("Lstat() cannot be run on node_modules in working directory", func() {
				it.Before(func() {
					Expect(os.Chmod(workingDir, 0000)).To(Succeed())
				})

				it.After(func() {
					Expect(os.Chmod(workingDir, os.ModePerm)).To(Succeed())
				})

				it("returns an error", func() {
					_, err := installProcess.SetupModules(workingDir, "", nextModulesLayerPath)
					Expect(err).To(MatchError(ContainSubstring("failed to remove existing node_modules")))
					Expect(err).To(MatchError(ContainSubstring("permission denied")))
				})
			})

			context("node_modules directory cannot be created in layer directory", func() {
				it.Before(func() {
					Expect(os.MkdirAll(filepath.Join(workingDir, "node_modules"), os.ModePerm)).To(Succeed())
					Expect(os.Chmod(workingDir, 0000)).To(Succeed())
				})

				it.After(func() {
					Expect(os.Chmod(workingDir, os.ModePerm)).To(Succeed())
				})

				it("returns an error", func() {
					_, err := installProcess.SetupModules(workingDir, "", nextModulesLayerPath)
					Expect(err).To(MatchError(ContainSubstring("failed to remove existing node_modules:")))
					Expect(err).To(MatchError(ContainSubstring("permission denied")))
				})
			})
		})
	})

	context("Execute", func() {
		var (
			workingDir       string
			modulesLayerPath string
			executions       []pexec.Execution
			buffer           *bytes.Buffer
			executable       *fakes.Executable
			summer           *fakes.Summer

			installProcess pnpminstall.PnpmInstallProcess
		)

		it.Before(func() {
			var err error
			workingDir, err = os.MkdirTemp("", "working-dir")
			Expect(err).NotTo(HaveOccurred())

			modulesLayerPath, err = os.MkdirTemp("", "modules-dir")
			Expect(err).NotTo(HaveOccurred())

			summer = &fakes.Summer{}
			buffer = bytes.NewBuffer(nil)

			executions = []pexec.Execution{}
			executable = &fakes.Executable{}
			executable.ExecuteCall.Stub = func(execution pexec.Execution) error {
				executions = append(executions, execution)
				_, err := fmt.Fprintln(execution.Stdout, "stdout output")
				Expect(err).NotTo(HaveOccurred())
				_, err = fmt.Fprintln(execution.Stderr, "stderr output")
				Expect(err).NotTo(HaveOccurred())

				if strings.Contains(strings.Join(execution.Args, " "), "store-dir") {
					_, err := fmt.Fprintln(execution.Stdout, "undefined")
					Expect(err).NotTo(HaveOccurred())
				}

				if strings.Contains(strings.Join(execution.Args, " "), "install") {
					Expect(os.MkdirAll(filepath.Join(workingDir, "node_modules"), os.ModePerm)).To(Succeed())
				}

				return nil
			}

			installProcess = pnpminstall.NewPnpmInstallProcess(executable, summer, scribe.NewEmitter(buffer))
		})

		it.After(func() {
			Expect(os.RemoveAll(workingDir)).To(Succeed())
			Expect(os.RemoveAll(modulesLayerPath)).To(Succeed())
		})

		context("when launch is false", func() {
			it("executes pnpm install", func() {
				err := installProcess.Execute(workingDir, modulesLayerPath, false)
				Expect(err).NotTo(HaveOccurred())

				Expect(executions).To(HaveLen(2))
				Expect(executions[0].Args).To(Equal([]string{
					"config",
					"get",
					"store-dir",
				}))
				Expect(executions[0].Env).To(ContainElement(MatchRegexp(fmt.Sprintf(`^PATH=.*:%s$`, filepath.Join(workingDir, "node_modules", ".bin")))))
				Expect(executions[0].Dir).To(Equal(workingDir))

				Expect(executions[1].Args).To(Equal([]string{
					"install",
					"--frozen-lockfile",
				}))
				Expect(executions[1].Env).To(ContainElement(MatchRegexp(fmt.Sprintf(`^PATH=.*:%s$`, filepath.Join(workingDir, "node_modules", ".bin")))))
				Expect(executions[1].Dir).To(Equal(workingDir))
				Expect(buffer.String()).To(ContainLines(
					"    Running 'pnpm install --frozen-lockfile'",
					"      stdout output",
					"      stderr output",
				))
			})
		})

		context("when launch is true", func() {
			it("executes pnpm install --prod", func() {
				err := installProcess.Execute(workingDir, modulesLayerPath, true)
				Expect(err).NotTo(HaveOccurred())

				Expect(executions).To(HaveLen(2))
				Expect(executions[0].Args).To(Equal([]string{
					"config",
					"get",
					"store-dir",
				}))
				Expect(executions[0].Env).To(ContainElement(MatchRegexp(fmt.Sprintf(`^PATH=.*:%s$`, filepath.Join(workingDir, "node_modules", ".bin")))))
				Expect(executions[0].Dir).To(Equal(workingDir))

				Expect(executions[1].Args).To(Equal([]string{
					"install",
					"--frozen-lockfile",
					"--prod",
				}))
				Expect(executions[1].Env).To(ContainElement(MatchRegexp(fmt.Sprintf(`^PATH=.*:%s$`, filepath.Join(workingDir, "node_modules", ".bin")))))
				Expect(executions[1].Dir).To(Equal(workingDir))
				Expect(buffer.String()).To(ContainLines(
					"    Running 'pnpm install --frozen-lockfile --prod'",
					"      stdout output",
					"      stderr output",
				))
			})
		})

		context("when there is an offline store directory", func() {
			it.Before(func() {
				Expect(os.Mkdir(filepath.Join(workingDir, "offline-store"), os.ModePerm)).To(Succeed())

				executable.ExecuteCall.Stub = func(execution pexec.Execution) error {
					executions = append(executions, execution)

					if strings.Contains(strings.Join(execution.Args, " "), "store-dir") {
						_, err := fmt.Fprintln(execution.Stdout, filepath.Join(workingDir, "offline-store"))
						Expect(err).NotTo(HaveOccurred())
					}

					if strings.Contains(strings.Join(execution.Args, " "), "install") {
						Expect(os.MkdirAll(filepath.Join(workingDir, "node_modules"), os.ModePerm)).To(Succeed())
					}

					return nil
				}
			})

			it("executes pnpm install in offline mode", func() {
				err := installProcess.Execute(workingDir, modulesLayerPath, true)
				Expect(err).NotTo(HaveOccurred())

				Expect(executions).To(HaveLen(2))
				Expect(executions[0].Args).To(Equal([]string{
					"config",
					"get",
					"store-dir",
				}))
				Expect(executions[0].Env).To(ContainElement(MatchRegexp(fmt.Sprintf(`^PATH=.*:%s$`, filepath.Join(workingDir, "node_modules", ".bin")))))
				Expect(executions[0].Dir).To(Equal(workingDir))

				Expect(executions[1].Args).To(Equal([]string{
					"install",
					"--frozen-lockfile",
					"--offline",
					"--prod",
				}))
				Expect(executions[1].Env).To(ContainElement(MatchRegexp(fmt.Sprintf(`^PATH=.*:%s$`, filepath.Join(workingDir, "node_modules", ".bin")))))
				Expect(executions[1].Dir).To(Equal(workingDir))
				Expect(buffer.String()).To(ContainSubstring("Running 'pnpm install --frozen-lockfile --offline --prod'"))
			})
		})

		context("when the offline store directory is specified as a relative path", func() {
			it.Before(func() {
				Expect(os.Mkdir(filepath.Join(workingDir, "pnpm-store"), os.ModePerm)).To(Succeed())

				executable.ExecuteCall.Stub = func(execution pexec.Execution) error {
					executions = append(executions, execution)

					if strings.Contains(strings.Join(execution.Args, " "), "store-dir") {
						// pnpm returns a relative path when .npmrc has store-dir=./pnpm-store
						_, err := fmt.Fprintln(execution.Stdout, "./pnpm-store")
						Expect(err).NotTo(HaveOccurred())
					}

					if strings.Contains(strings.Join(execution.Args, " "), "install") {
						Expect(os.MkdirAll(filepath.Join(workingDir, "node_modules"), os.ModePerm)).To(Succeed())
					}

					return nil
				}
			})

			it("resolves the relative path against workingDir and installs offline", func() {
				err := installProcess.Execute(workingDir, modulesLayerPath, true)
				Expect(err).NotTo(HaveOccurred())

				Expect(executions).To(HaveLen(2))
				Expect(executions[1].Args).To(Equal([]string{
					"install",
					"--frozen-lockfile",
					"--offline",
					"--prod",
				}))
				Expect(buffer.String()).To(ContainSubstring("Running 'pnpm install --frozen-lockfile --offline --prod'"))
			})
		})

		context("failure cases", func() {
			context("the pnpm executable fails to get config", func() {
				it.Before(func() {
					executable.ExecuteCall.Stub = func(execution pexec.Execution) error {
						if strings.Contains(strings.Join(execution.Args, " "), "config") {
							_, err := fmt.Fprintf(execution.Stdout, "some stdout error")
							Expect(err).NotTo(HaveOccurred())
							_, err = fmt.Fprintf(execution.Stderr, "some stderr error")
							Expect(err).NotTo(HaveOccurred())
							return errors.New("pnpm config failed")
						}

						return nil
					}
				})

				it("returns an error", func() {
					err := installProcess.Execute(workingDir, modulesLayerPath, true)
					Expect(err).To(MatchError(ContainSubstring("failed to execute pnpm config")))
					Expect(err).To(MatchError(ContainSubstring("error: pnpm config failed")))
				})
			})

			context("the pnpm executable fails to install", func() {
				it.Before(func() {
					executable.ExecuteCall.Stub = func(execution pexec.Execution) error {
						if strings.Contains(strings.Join(execution.Args, " "), "install") {
							_, err := execution.Stdout.Write([]byte("stdout output"))
							Expect(err).NotTo(HaveOccurred())
							_, err = execution.Stderr.Write([]byte("stderr output"))
							Expect(err).NotTo(HaveOccurred())

							return errors.New("pnpm install failed")
						}

						return nil
					}
				})

				it("prints the execution output and returns an error", func() {
					err := installProcess.Execute(workingDir, modulesLayerPath, true)
					Expect(err).To(MatchError(ContainSubstring("failed to execute pnpm install:")))
					Expect(err).To(MatchError(ContainSubstring("pnpm install failed")))

					Expect(buffer.String()).To(ContainSubstring("stdout output"))
					Expect(buffer.String()).To(ContainSubstring("stderr output"))
				})
			})
		})
	})
}
