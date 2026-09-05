package renstiq

import "runtime/debug"

// Version and Commit can be set with -ldflags when building release binaries.
var Version string
var Commit string

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
	if Commit != "" {
		return version + " (" + Commit + ")"
	}
	return version
}
