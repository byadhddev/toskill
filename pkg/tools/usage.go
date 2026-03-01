package tools

import (
	"fmt"
	"os"
	"sync"

	copilot "github.com/github/copilot-sdk/go"
)

// AgentUsage tracks accumulated token usage for a single agent phase.
type AgentUsage struct {
	InputTokens      float64
	OutputTokens     float64
	CacheReadTokens  float64
	CacheWriteTokens float64
	Requests         float64
	PremiumRequests  float64
	DurationMS       float64
}

// UsageTracker accumulates token usage across all pipeline agents.
type UsageTracker struct {
	mu     sync.Mutex
	phases map[string]*AgentUsage // "extract", "curate", "build"
}

// GlobalTracker is the shared usage tracker for all pipeline agents.
var GlobalTracker = NewUsageTracker()

// NewUsageTracker creates a new tracker.
func NewUsageTracker() *UsageTracker {
	return &UsageTracker{phases: make(map[string]*AgentUsage)}
}

// Track records a usage event for a phase.
func (t *UsageTracker) Track(phase string, event copilot.SessionEvent) {
	t.mu.Lock()
	defer t.mu.Unlock()

	u, ok := t.phases[phase]
	if !ok {
		u = &AgentUsage{}
		t.phases[phase] = u
	}

	d := event.Data
	if d.TotalPremiumRequests != nil {
		u.PremiumRequests = *d.TotalPremiumRequests
	}
	if d.TotalAPIDurationMS != nil {
		u.DurationMS = *d.TotalAPIDurationMS
	}
	if d.ModelMetrics != nil {
		// ModelMetrics contains cumulative totals — take the latest snapshot
		var totalIn, totalOut, totalCacheR, totalCacheW, totalReqs float64
		for _, m := range d.ModelMetrics {
			totalIn += m.Usage.InputTokens
			totalOut += m.Usage.OutputTokens
			totalCacheR += m.Usage.CacheReadTokens
			totalCacheW += m.Usage.CacheWriteTokens
			totalReqs += m.Requests.Count
		}
		u.InputTokens = totalIn
		u.OutputTokens = totalOut
		u.CacheReadTokens = totalCacheR
		u.CacheWriteTokens = totalCacheW
		u.Requests = totalReqs
	}
}

// PrintSummary prints a formatted usage summary to stderr.
func (t *UsageTracker) PrintSummary() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.phases) == 0 {
		return
	}

	var totalIn, totalOut, totalCacheR, totalCacheW, totalReqs, totalPremium float64
	phaseOrder := []string{"extract", "curate", "build"}

	fmt.Fprintf(os.Stderr, "\n📊 Token Usage\n")
	for _, phase := range phaseOrder {
		u, ok := t.phases[phase]
		if !ok {
			continue
		}
		label := map[string]string{"extract": "Extract", "curate": "Curate", "build": "Build"}[phase]
		fmt.Fprintf(os.Stderr, "   %s: %s in / %s out",
			label, formatTokens(u.InputTokens), formatTokens(u.OutputTokens))
		if u.CacheReadTokens > 0 {
			fmt.Fprintf(os.Stderr, " (cache: %s read)", formatTokens(u.CacheReadTokens))
		}
		if u.PremiumRequests > 0 {
			fmt.Fprintf(os.Stderr, " [%.0f premium req]", u.PremiumRequests)
		}
		fmt.Fprintln(os.Stderr)

		totalIn += u.InputTokens
		totalOut += u.OutputTokens
		totalCacheR += u.CacheReadTokens
		totalCacheW += u.CacheWriteTokens
		totalReqs += u.Requests
		totalPremium += u.PremiumRequests
	}

	fmt.Fprintf(os.Stderr, "   ─────────────────────────────\n")
	fmt.Fprintf(os.Stderr, "   Total: %s in / %s out",
		formatTokens(totalIn), formatTokens(totalOut))
	if totalCacheR > 0 {
		fmt.Fprintf(os.Stderr, " (cache: %s read)", formatTokens(totalCacheR))
	}
	if totalPremium > 0 {
		fmt.Fprintf(os.Stderr, " [%.0f premium reqs]", totalPremium)
	}
	fmt.Fprintln(os.Stderr)
}

func formatTokens(n float64) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", n/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fK", n/1_000)
	}
	return fmt.Sprintf("%.0f", n)
}

// EmitUsage is the legacy JSON emitter — kept for --verbose mode only.
func EmitUsage(event copilot.SessionEvent) {
	d := event.Data
	if d.ModelMetrics == nil && d.TotalPremiumRequests == nil {
		return
	}
	// Compact one-liner
	for name, m := range d.ModelMetrics {
		fmt.Fprintf(os.Stderr, "   📈 %s: %s in / %s out (%.0f reqs)\n",
			name, formatTokens(m.Usage.InputTokens), formatTokens(m.Usage.OutputTokens), m.Requests.Count)
	}
}
