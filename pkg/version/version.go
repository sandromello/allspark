package version

import (
	"fmt"
	"os"
	"runtime"
)

// Info contains versioning information.
type Info struct {
	// KubernetesVersion string `json:"kubernetesVersion"`
	Version   string `json:"version"`
	GitCommit string `json:"git_commit"`
	BuildDate string `json:"build_date"`
	GoVersion string `json:"go_version"`
	Compiler  string `json:"compiler"`
	Platform  string `json:"platform"`
}

var (
	version string
	// gitVersion string
	gitCommit = "$Format:%H$"          // sha1 from git, output of $(git rev-parse HEAD)
	buildDate = "1970-01-01T00:00:00Z" // build date in ISO8601 format, output of $(date -u +'%Y-%m-%dT%H:%M:%SZ')
)

// Get returns the overall codebase version. It's for detecting
// what code a binary was built from.
func Get() Info {
	// These variables typically come from -ldflags settings and in
	// their absence fallback to the settings in pkg/version/base.go
	return Info{
		Version:   version,
		GitCommit: gitCommit,
		BuildDate: buildDate,
		GoVersion: runtime.Version(),
		Compiler:  runtime.Compiler,
		Platform:  fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
	}
}

func Print() {
	vinfo := Get()
	fmt.Println("----------------------------------")
	fmt.Println("Build Date: ", vinfo.BuildDate)
	fmt.Println("Compiler: ", vinfo.Compiler)
	fmt.Println("Version: ", vinfo.Version)
	fmt.Println("Git Commit: ", vinfo.GitCommit)
	fmt.Println("GO Version: ", vinfo.GoVersion)
	fmt.Println("Platform: ", vinfo.Platform)
	fmt.Println("----------------------------------")
}

func PrintAndExit() {
	Print()
	os.Exit(0)
}
