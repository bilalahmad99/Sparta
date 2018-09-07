// +build mage

package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/magefile/mage/mg" // mg contains helpful utility functions, like Deps
	"github.com/magefile/mage/sh" // mg contains helpful utility functions, like Deps
)

const WORK_DIR = "./sparta"

var ignoreSubdirectoryPaths = []string{
	".vendor",
	".sparta",
	".vscode",
}

// Default target to run when none is specified
// If not set, running mage will list available targets
// var Default = Build

func mageScript(commands [][]string) error {
	for _, eachCommand := range commands {
		var commandErr error
		if len(eachCommand) <= 1 {
			commandErr = sh.Run(eachCommand[0])
		} else {
			commandErr = sh.Run(eachCommand[0], eachCommand[1:]...)
		}
		if commandErr != nil {
			return commandErr
		}
	}
	return nil
}
func mageLog(formatSpecifier string, args ...interface{}) {
	if mg.Verbose() {
		if len(args) != 0 {
			log.Printf(formatSpecifier, args...)
		} else {
			log.Printf(formatSpecifier)
		}
	}
}

func goSourceFiles() ([]string, error) {
	files := make([]string, 0)
	walker := func(path string, info os.FileInfo, err error) error {
		contains := false
		for _, eachComponent := range ignoreSubdirectoryPaths {
			contains = strings.Contains(path, eachComponent)
			if contains {
				break
			}
		}
		if !contains && (filepath.Ext(path) == ".go") {
			files = append(files, path)
		}
		return nil
	}
	goSourceFilesErr := filepath.Walk(".", walker)
	return files, goSourceFilesErr
}

func goSourceApply(commandParts ...string) error {
	goSourceFiles, goSourceFilesErr := goSourceFiles()
	if goSourceFilesErr != nil {
		return goSourceFilesErr
	}
	mageLog("Found %d `go` source files", len(goSourceFiles))

	if len(commandParts) <= 0 {
		return errors.New("goSourceApply requires a command to apply to source files")
	}
	commandArgs := []string{}
	if len(commandParts) > 1 {
		for _, eachPart := range commandParts[1:] {
			commandArgs = append(commandArgs, eachPart)
		}
	}
	for _, eachFile := range goSourceFiles {
		applyArgs := append(commandArgs, eachFile)
		applyErr := sh.Run(commandParts[0], applyArgs...)
		if applyErr != nil {
			return applyErr
		}
	}
	return nil
}

// GenerateBuildInfo creates the automatic buildinfo.go file so that we can
// stamp the SHA into the binaries we build...
func GenerateBuildInfo() error {
	// The first thing we need is the `git` SHA
	cmd := exec.Command("git", "rev-parse", "HEAD")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return err
	}
	stdOutResult := strings.TrimSpace(string(stdout.Bytes()))

	// Super = update the buildinfo data
	buildInfoTemplate := `package sparta

// THIS FILE IS AUTOMATICALLY GENERATED
// DO NOT EDIT
// CREATED: %s

// SpartaGitHash is the commit hash of this Sparta library
const SpartaGitHash = "%s"
`
	updatedInfo := fmt.Sprintf(buildInfoTemplate, time.Now().UTC(), stdOutResult)
	// Write it to the output location...
	return ioutil.WriteFile("./buildinfo.go", []byte(updatedInfo), os.ModePerm)
}

// GenerateConstants runs the set of commands that update the embedded CONSTANTS
// for both local and AWS Lambda execution
func GenerateConstants() error {
	generateCommands := [][]string{
		// Create the embedded version
		{"go", "run", "$GOPATH/src/github.com/mjibson/esc/main.go", "-o", "./CONSTANTS.go", "-private", "-pkg", "sparta", "./resources"},
		//Create a secondary CONSTANTS_AWSBINARY.go file with empty content.
		{"go", "run", "$GOPATH/src/github.com/mjibson/esc/main.go", "-o", "./CONSTANTS_AWSBINARY.go", "-private", "-pkg", "sparta", "./resources/awsbinary/README.md"},
		//The next step will insert the
		// build tags at the head of each file so that they are mutually exclusive
		{"go", "run", "./cmd/insertTags/main.go", "./CONSTANTS", "!lambdabinary"},
		{"go", "run", "./cmd/insertTags/main.go", "./CONSTANTS_AWSBINARY", "lambdabinary"},
		{"git", "commit", "-a", "-m", "Autogenerated constants"},
	}
	return mageScript(generateCommands)
}

// InstallBuildRequirements installs or updates the dependent
// packages that aren't referenced by the source, but are needed
// to build the Sparta source
func InstallBuildRequirements() error {
	mageLog("`go get` update flags (env.GO_GET_FLAG): %s", os.Getenv("GO_GET_FLAG"))

	requirements := []string{
		"github.com/golang/dep/...",
		"honnef.co/go/tools/cmd/megacheck",
		"honnef.co/go/tools/cmd/gosimple",
		"honnef.co/go/tools/cmd/unused",
		"honnef.co/go/tools/cmd/staticcheck",
		"golang.org/x/tools/cmd/goimports",
		"github.com/fzipp/gocyclo",
		"github.com/golang/lint/golint",
		"github.com/mjibson/esc",
		"github.com/securego/gosec/cmd/gosec/...",
	}
	for _, eachDep := range requirements {
		cmdErr := sh.Run("go",
			"get",
			os.Getenv("GO_GET_FLAG"),
			eachDep)

		// cmdErr := exec.Command(.Run()
		if cmdErr != nil {
			return cmdErr
		}
	}
	return nil
}

// EnsureVet ensures that the source has been `go vet`ted
func EnsureVet() error {
	return goSourceApply("go", "tool", "vet")
}

// EnsureLint ensures that the source is `golint`ed
func EnsureLint() error {
	return goSourceApply("golint")
}

// EnsureFormatted ensures that the source code is formatted with goimports
func EnsureFormatted() error {
	return goSourceApply("goimports", "-d")
}

// EnsureStaticChecks ensures that the source code passes static code checks
func EnsureStaticChecks() error {
	// Megacheck
	megacheckErr := sh.Run("megacheck",
		"-ignore",
		"github.com/mweagle/Sparta/CONSTANTS.go:*")
	if megacheckErr != nil {
		return megacheckErr
	}
	// Gosec
	return sh.Run("gosec",
		"-exclude=G204,G505,G401",
		"./...")
}

// EnsureAllPreconditions ensures that the source passes *ALL* static `ensure*`
// precondition steps
func EnsureAllPreconditions() error {
	mg.SerialDeps(
		InstallBuildRequirements,
		EnsureVet,
		EnsureLint,
		EnsureFormatted,
		EnsureStaticChecks,
	)
	return nil
}

// EnsureTravisBuildEnvironment is the command that sets up the Travis
// environment to run the build.
func EnsureTravisBuildEnvironment() error {
	mg.SerialDeps(InstallBuildRequirements)

	// Super run some commands
	travisComands := [][]string{
		[]string{"dep", "version"},
		[]string{"dep", "ensure"},
		[]string{"rsync", "-a", "--quiet", "--remove-source-files", "./vendor/", "$GOPATH/src"},
	}
	return mageScript(travisComands)
}

// Build the application
func Build() error {
	mg.Deps(EnsureAllPreconditions)
	return sh.Run("go", "build", ".")
}

// Clean the working directory
func Clean() error {
	cleanCommands := [][]string{
		[]string{"go", "clean", "."},
		[]string{"rm", "-rf", "./graph.html"},
		[]string{"rsync", "-a", "--quiet", "--remove-source-files", "./vendor/", "$GOPATH/src"},
	}
	return mageScript(cleanCommands)
}

// Describe runs the `TestDescribe` test to generate a describe HTML output
// file at graph.html
func Describe() error {
	describeCommands := [][]string{
		[]string{"rm", "-rf", "./graph.html"},
		[]string{"go", "test", "-v", "-run", "TestDescribe"},
	}
	return mageScript(describeCommands)
}

// Publish the latest source
func Publish() error {
	mg.SerialDeps(GenerateBuildInfo)

	describeCommands := [][]string{
		[]string{"echo", "Checking `git` tree status"},
		[]string{"git", "diff", "--exit-code"},
		// TODO - migrate to Go
		[]string{"./buildinfo.sh"},
		[]string{"git", "commit", "-a", "-m", "Tagging Sparta commit"},
		[]string{"git", "push", "origin"},
	}
	return mageScript(describeCommands)
}

// Test runs the Sparta tests
func Test() {
	testCommand := func() error {
		return sh.Run("go",
			"test",
			"-cover",
			"-race",
			"./...")
	}
	mg.SerialDeps(
		EnsureAllPreconditions,
		testCommand,
	)
}

// TestCover runs the test and opens up the resulting report
func TestCover() error {
	// mg.SerialDeps(
	// 	EnsureAllPreconditions,
	// )
	coverageReport := fmt.Sprintf("%s/cover.out", WORK_DIR)
	testCoverCommands := [][]string{
		[]string{"go", "test", fmt.Sprintf("-coverprofile=%s", coverageReport), "."},
		[]string{"go", "tool", "cover", fmt.Sprintf("-html=%s", coverageReport)},
		[]string{"rm", coverageReport},
		[]string{"open", fmt.Sprintf("%s/cover.html", WORK_DIR)},
	}
	return mageScript(testCoverCommands)
}

// TravisBuild is the task to build in the context of a Travis CI pipeline
func TravisBuild() error {
	mg.SerialDeps(EnsureTravisBuildEnvironment,
		Build,
		Test)
	return nil
}
