package config

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func loadLoggingConfig(t *testing.T, logging string) Config {
	t.Helper()
	body := `{"server_keys": ["local-key"], "zen_keys": ["zen-key"], "logging": ` + logging + `}`
	cfg, err := Load(writeConfig(t, body))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return cfg
}

func TestRequestsLogDefaults(t *testing.T) {
	cfg := loadLoggingConfig(t, `{"level": "info", "ring_size": 2000}`)
	requests := cfg.Logging.Requests
	if !requests.IsEnabled() {
		t.Fatal("an absent requests section keeps persistence on")
	}
	if filepath.Base(requests.StatePath) != "config.json.requests.db" {
		t.Fatalf("expected a path derived from the config file, got %q", requests.StatePath)
	}
	if requests.RetentionDays != 7 || requests.MaxRows != 200000 {
		t.Fatalf("unexpected defaults: %+v", requests)
	}
}

func TestRequestsLogDisabledClearsDerivedFields(t *testing.T) {
	cfg := loadLoggingConfig(t, `{"level": "info", "ring_size": 2000, "requests": {"enabled": false}}`)
	if cfg.Logging.Requests.IsEnabled() {
		t.Fatal("enabled false must disable persistence")
	}
	if cfg.Logging.Requests.StatePath != "" || cfg.Logging.Requests.MaxRows != 0 {
		t.Fatalf("a disabled log must not carry a path or caps: %+v", cfg.Logging.Requests)
	}
}

func TestRequestsLogValidation(t *testing.T) {
	for name, body := range map[string]string{
		"retention too large": `{"requests": {"retention_days": 4000}}`,
		"rows too large":      `{"requests": {"max_rows": 9000000}}`,
		"buffer too large":    `{"requests": {"buffer_size": 900000}}`,
	} {
		path := writeConfig(t, `{"server_keys": ["local-key"], "zen_keys": ["zen-key"], "logging": `+body+`}`)
		if _, err := Load(path); err == nil {
			t.Fatalf("%s must be rejected", name)
		}
	}
	cfg := loadLoggingConfig(t, `{"level": "info", "ring_size": 2000, "requests": {"retention_days": 3, "max_rows": 5000}}`)
	if cfg.Logging.Requests.RetentionDays != 3 || cfg.Logging.Requests.MaxRows != 5000 {
		t.Fatalf("explicit values must survive: %+v", cfg.Logging.Requests)
	}
}

func TestRequestsLogSameSource(t *testing.T) {
	off := false
	current := RequestsLogConfig{StatePath: "a.db", RetentionDays: 7, MaxRows: 100}
	if !current.SameSourceAs(RequestsLogConfig{StatePath: "a.db", RetentionDays: 30, MaxRows: 900}) {
		t.Fatal("retention changes must not force a reopen")
	}
	if current.SameSourceAs(RequestsLogConfig{StatePath: "b.db"}) {
		t.Fatal("a path change must force a reopen")
	}
	if current.SameSourceAs(RequestsLogConfig{Enabled: &off}) {
		t.Fatal("disabling must force a reopen")
	}
}

func TestRequestsLogRoundTrip(t *testing.T) {
	cfg := loadLoggingConfig(t, `{"level": "info", "ring_size": 2000, "requests": {"retention_days": 14}}`)
	encoded, err := json.Marshal(cfg.Logging)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded LoggingConfig
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Requests.RetentionDays != 14 {
		t.Fatalf("retention lost in round trip: %+v", decoded.Requests)
	}
}
