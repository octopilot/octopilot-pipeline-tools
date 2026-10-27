package pack

import "strings"

// exportStep is how pack and the lifecycle announce the export phase.
const exportStep = "===> EXPORTING"

// exportSection is the output of the latest export: from the last "===> EXPORTING" on, or the last 8 KiB when the
// marker has scrolled out. Classifying only this part keeps build output (a test named after a 403, a crate printing
// "denied") from deciding whether a push is retried.
func exportSection(output string) string {
	if i := strings.LastIndex(output, exportStep); i >= 0 {
		return output[i:]
	}
	if len(output) > 8<<10 {
		return output[len(output)-8<<10:]
	}
	return output
}
