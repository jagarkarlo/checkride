package buildinfo

import (
	"runtime"
	"runtime/debug"
)

var Version = "dev"
var Revision = "unknown"
var BuiltAt = ""

type Info struct {
	Version   string `json:"version"`
	Revision  string `json:"revision"`
	BuiltAt   string `json:"builtAt,omitempty"`
	GoVersion string `json:"goVersion"`
	Platform  string `json:"platform"`
	Modified  bool   `json:"modified"`
}

func Current() Info {
	info := Info{Version: Version, Revision: Revision, BuiltAt: BuiltAt, GoVersion: runtime.Version(), Platform: runtime.GOOS + "/" + runtime.GOARCH}
	if build, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range build.Settings {
			if setting.Key == "vcs.revision" && info.Revision == "unknown" {
				info.Revision = setting.Value
			}
			if setting.Key == "vcs.modified" {
				info.Modified = setting.Value == "true"
			}
		}
	}
	return info
}
