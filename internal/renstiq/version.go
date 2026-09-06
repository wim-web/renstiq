package renstiq

import "runtime/debug"

// Version can be set with -ldflags when building release binaries.
var Version string

func currentVersion() string {
	version := Version
	if version == "" {
		version = "dev"
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
			version = info.Main.Version
		}
	}
	return version
}

func buildVersion() string {
	version := currentVersion()
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && setting.Value != "" {
				commit := setting.Value
				if len(commit) > 12 {
					commit = commit[:12]
				}
				return version + " (" + commit + ")"
			}
		}
	}
	return version
}
