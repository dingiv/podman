//go:build !remote && (linux || freebsd)

package buildah

import (
	"os"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"
)

// easytidy et-probe: label-gated source diagnostics.  Set ET_PROBE to a
// comma-separated label list (or "all") to enable matching etProbe() calls;
// without the variable every probe stays silent.
var (
	etProbeOnce   sync.Once
	etProbeLabels = map[string]bool{}
)

func etProbeEnabled(label string) bool {
	etProbeOnce.Do(func() {
		for _, l := range strings.Split(os.Getenv("ET_PROBE"), ",") {
			if l = strings.TrimSpace(l); l != "" {
				etProbeLabels[l] = true
			}
		}
	})
	return etProbeLabels[label] || etProbeLabels["all"]
}

// etProbe logs a diagnostic line when label is enabled via ET_PROBE.
func etProbe(label, format string, args ...interface{}) {
	if !etProbeEnabled(label) {
		return
	}
	logrus.Infof("et-probe["+label+"]: "+format, args...)
}
