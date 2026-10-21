package config

import "testing"

func TestAnonymousBackoffDefaultsAndValidation(t *testing.T) {
	b := &AnonymousBackoffConfig{}
	if err := b.Normalize(); err != nil {
		t.Fatal(err)
	}
	if b.Threshold != 6 || b.BaseHours != 1 || b.MaxHours != 24 {
		t.Fatalf("defaults = %+v", b)
	}
	if err := (&AnonymousBackoffConfig{Threshold: -1}).Normalize(); err == nil {
		t.Fatal("a negative threshold must be rejected")
	}
	if err := (&AnonymousBackoffConfig{BaseHours: 5, MaxHours: 2}).Normalize(); err == nil {
		t.Fatal("max_hours below base_hours must be rejected")
	}
	var absent *AnonymousBackoffConfig
	if err := absent.Normalize(); err != nil {
		t.Fatalf("an absent block disables the breaker: %v", err)
	}
}
