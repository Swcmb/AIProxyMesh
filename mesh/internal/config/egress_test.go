package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const baseConfig = `{
  "server_keys": ["local-key"],
  "zen_keys": ["zen-key"],
  "upstream": {"zen": "https://opencode.ai/zen", "go": "https://opencode.ai/zen/go", "anonymous": "https://opencode.ai/inference"},
  "egress": {
    "enabled": true,
    "subscriptions": [
      {"id": "sub-1", "name": "primary", "url": "https://example.com/sub", "enabled": true, "refresh_seconds": 3600}
    ]
  }
}`

func TestEgressDefaults(t *testing.T) {
	path := writeConfig(t, baseConfig)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.EgressEnabled() {
		t.Fatal("egress must be enabled")
	}
	egress := cfg.Egress
	if egress.Cooldown.BaseSeconds != 30 || egress.Cooldown.MaxSeconds != 900 {
		t.Fatalf("cooldown defaults = %+v", egress.Cooldown)
	}
	if egress.Probe.IntervalSeconds != 60 || egress.Probe.TimeoutSeconds != 10 || egress.Probe.Concurrency != 8 {
		t.Fatalf("probe defaults = %+v", egress.Probe)
	}
	if egress.Schedule.HealthWeight != 0.30 || egress.Schedule.UpstreamSuccessWeight != 0.40 {
		t.Fatalf("schedule defaults = %+v", egress.Schedule)
	}
	if !egress.LegacyProxiesImported() {
		t.Fatal("legacy proxies must be imported by default")
	}
	for _, scheme := range []string{"http", "https", "socks5", "socks5h", "direct"} {
		if !egress.ProtocolAllowed(scheme) {
			t.Fatalf("%s must be allowed by default", scheme)
		}
	}
	if !strings.HasSuffix(egress.StatePath, ".egress.db") {
		t.Fatalf("state path = %q", egress.StatePath)
	}
	if cfg.LegacyContent() != "direct" {
		t.Fatalf("legacy content = %q", cfg.LegacyContent())
	}
}

func TestEgressValidation(t *testing.T) {
	cases := map[string]string{
		"duplicate id":   `"subscriptions":[{"id":"a","url":"https://x.test","enabled":true},{"id":"a","url":"https://y.test","enabled":true}]`,
		"bad url":        `"subscriptions":[{"id":"a","url":"ftp://x.test","enabled":true}]`,
		"negative":       `"schedule":{"health_weight":-1,"latency_weight":0.1,"routing_weight":0.1,"upstream_success_weight":0.1}`,
		"cooldown":       `"cooldown":{"base_seconds":600,"max_seconds":30}`,
		"probe interval": `"probe":{"interval_seconds":5,"timeout_seconds":2,"concurrency":2}`,
		"bad target":     `"probe":{"targets":["not a url"]}`,
	}
	for name, fragment := range cases {
		body := `{"server_keys":["local-key"],"egress":{"enabled":true,` + fragment + `}}`
		path := writeConfig(t, body)
		if _, err := Load(path); err == nil {
			t.Fatalf("%s: expected validation error", name)
		}
	}
}

func TestEgressSaveRoundTrip(t *testing.T) {
	path := writeConfig(t, baseConfig)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Egress.Cooldown.BaseSeconds = 45
	if err := SaveAtomic(path, cfg); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Egress.Cooldown.BaseSeconds != 45 {
		t.Fatalf("round trip lost egress config: %+v", reloaded.Egress)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["egress"]; !ok {
		t.Fatal("saved config must include egress when configured")
	}
}

func TestEgressAbsentIsNotPersisted(t *testing.T) {
	path := writeConfig(t, `{"server_keys":["local-key"],"zen_keys":["zen-key"]}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Egress != nil {
		t.Fatal("egress must stay nil when absent")
	}
	if err := SaveAtomic(path, cfg); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "\"egress\"") {
		t.Fatal("absent egress must not appear in the saved file")
	}
}

func TestCloneEgressIsDeep(t *testing.T) {
	path := writeConfig(t, baseConfig)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	clone := Clone(cfg)
	clone.Egress.Subscriptions[0].Name = "changed"
	clone.Egress.Failover.StatusPolicy["429"] = "changed"
	clone.Egress.Probe.Targets = append(clone.Egress.Probe.Targets, "https://x.test")
	if cfg.Egress.Subscriptions[0].Name != "primary" {
		t.Fatal("clone must not alias subscriptions")
	}
	if cfg.Egress.Failover.StatusPolicy["429"] != "switch-or-wait" {
		t.Fatal("clone must not alias status policy")
	}
}
