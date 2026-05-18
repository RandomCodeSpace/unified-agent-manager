package version

import "runtime/debug"

// Override can be set at build time with -ldflags "-X .../internal/version.Override=vX.Y.Z".
// When unset, Go module builds installed with `go install module@version` expose the
// selected module version through debug.ReadBuildInfo.
var Override string

func String() string {
	if Override != "" {
		return Override
	}
	info, ok := debug.ReadBuildInfo()
	if ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
