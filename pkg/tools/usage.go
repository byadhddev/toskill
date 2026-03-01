package tools

import (
	"encoding/json"
	"fmt"
	"os"

	copilot "github.com/github/copilot-sdk/go"
)

// EmitUsage outputs a structured JSON usage line to stderr for pipeline capture.
func EmitUsage(event copilot.SessionEvent) {
	d := event.Data
	u := map[string]interface{}{"type": "usage"}
	data := map[string]interface{}{}
	if d.TotalPremiumRequests != nil {
		data["premiumRequests"] = *d.TotalPremiumRequests
	}
	if d.TotalAPIDurationMS != nil {
		data["durationMs"] = *d.TotalAPIDurationMS
	}
	if d.CurrentTokens != nil {
		data["currentTokens"] = *d.CurrentTokens
	}
	if d.ModelMetrics != nil {
		models := map[string]interface{}{}
		for name, m := range d.ModelMetrics {
			models[name] = map[string]interface{}{
				"inputTokens":      m.Usage.InputTokens,
				"outputTokens":     m.Usage.OutputTokens,
				"cacheReadTokens":  m.Usage.CacheReadTokens,
				"cacheWriteTokens": m.Usage.CacheWriteTokens,
				"requests":         m.Requests.Count,
				"cost":             m.Requests.Cost,
			}
		}
		data["models"] = models
	}
	u["data"] = data
	if b, err := json.Marshal(u); err == nil {
		fmt.Fprintf(os.Stderr, "%s\n", string(b))
	}
}
