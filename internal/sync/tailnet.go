package sync

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"time"
)

// TailscaleTimeout bounds the `tailscale status` call that learns our names.
const TailscaleTimeout = 3 * time.Second

// TailnetNames returns this device's MagicDNS name (trailing dot stripped)
// and its short host name, in that order. It returns nil when tailscale is
// absent, fails, or reports no name: pairing then behaves as it did before.
func TailnetNames() []string {
	ctx, cancel := context.WithTimeout(context.Background(), TailscaleTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tailscale", "status", "--json").Output() //nolint:gosec // fixed command
	if err != nil {
		return nil
	}
	return parseTailnetNames(out)
}

func parseTailnetNames(raw []byte) []string {
	var st struct {
		Self struct {
			DNSName string `json:"DNSName"`
		} `json:"Self"`
	}
	if json.Unmarshal(raw, &st) != nil {
		return nil
	}
	fqdn := strings.TrimSuffix(strings.TrimSpace(st.Self.DNSName), ".")
	if fqdn == "" {
		return nil
	}
	short, _, _ := strings.Cut(fqdn, ".")
	if short == "" || short == fqdn {
		return []string{fqdn}
	}
	return []string{fqdn, short}
}
