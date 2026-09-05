package renstiq

import "runtime/debug"

// Version can be set with -ldflags when building release binaries.
var Version string

func buildVersion() string {
	if Version != "" {
		return Version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
