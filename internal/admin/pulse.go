package admin

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"routerllm/internal/provider"
)

// Pulse is the poll heartbeat: change signatures for /status and /metrics plus
// the telemetry cursor. The console polls this every 3s and re-fetches a full
// payload only when its signature moved — a steady-state poll costs ~100 bytes
// instead of four complete JSON documents.
type Pulse struct {
	Seq        uint64 `json:"seq"`
	StatusSig  string `json:"status_sig"`
	MetricsSig string `json:"metrics_sig"`
}

func hashSig(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "|")))

	return hex.EncodeToString(h[:8])
}

func (d Deps) handlePulse(w http.ResponseWriter, r *http.Request) {
	seq := uint64(0)
	if d.Telemetry != nil {
		seq = d.Telemetry.Latest()
	}

	reload := d.Reloads.Last()

	// A 1-minute clock tick keeps time-anchored windows (the current hour on
	// the chart) moving without any traffic flowing.
	bucket := time.Now().Unix() / 60

	writeJSON(w, http.StatusOK, Pulse{
		Seq:        seq,
		StatusSig:  hashSig("status", fmt.Sprint(reload.At.UnixNano()), fmt.Sprint(reload.OK), keySig(d.Registry()), fmt.Sprint(seq), fmt.Sprint(bucket)),
		MetricsSig: hashSig("metrics", fmt.Sprint(reload.At.UnixNano()), fmt.Sprint(seq), fmt.Sprint(bucket)),
	})
}

// keySig fingerprints every key's lifecycle state (alive / manual-disabled /
// cooling until T). Cooldowns expire on the clock, not via events, so a live
// cooldown adds a 3s tick to the signature — the badge refreshes on the next
// poll and idle systems stay byte-stable.
func keySig(reg *provider.Registry) string {
	names := make([]string, 0, reg.TotalProviders())
	states := make(map[string][]string, reg.TotalProviders())

	for _, pc := range reg.ProviderConfigs() {
		names = append(names, pc.Name)

		live, ok := reg.Provider(pc.Name)
		if !ok || pc.Disabled {
			states[pc.Name] = []string{"off"}
			continue
		}

		parts := make([]string, 0, len(pc.Keys))
		cooling := false
		for _, s := range live.Keys.States() {
			switch {
			case s.Alive:
				parts = append(parts, "up")
			case s.Manual:
				parts = append(parts, "manual")
			default:
				parts = append(parts, fmt.Sprintf("cd=%d", s.DeadUntil.Unix()))
				cooling = true
			}
		}
		if cooling {
			parts = append(parts, fmt.Sprintf("tick=%d", time.Now().Unix()/3))
		}
		states[pc.Name] = parts
	}

	sort.Strings(names)
	joined := make([]string, 0, len(names))
	for _, name := range names {
		joined = append(joined, name+"="+strings.Join(states[name], ","))
	}

	return strings.Join(joined, ";")
}
