package debugutil

import (
	"github.com/jagreehal/autotel-go"
)

// Print proxies to autotel.debugPrint to avoid import cycles.
func Print(format string, args ...any) {
	autotel.DebugPrintf(format, args...)
}
