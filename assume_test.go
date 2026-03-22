package main

import (
	"context"
	"strings"
	"testing"
)

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input   string
		wantSec int32
		wantErr bool
	}{
		{"1h", 3600, false},
		{"30m", 1800, false},
		{"1h30m", 5400, false},
		{"15m", 900, false},
		{"12h", 43200, false},
		{"01:00:00", 3600, false},  // HH:MM:SS
		{"04:30:00", 16200, false}, // 4.5 hours
		{"00:15:00", 900, false},   // exactly 15 minutes
		{"14m", 0, true},           // below minimum
		{"13h", 0, true},           // above maximum
		{"0s", 0, true},            // zero
		{"invalid", 0, true},
		{"01:60:00", 0, true},    // minutes out of range
		{"01:00:60", 0, true},    // seconds out of range
		{"01:-1:00", 0, true},    // negative minutes
		{"13:00:00", 0, true},    // hours > 12 (would overflow bounds check)
		{"99999:00:00", 0, true}, // extreme hours (integer overflow guard)
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseDuration(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseDuration(%q) expected error, got %d", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Errorf("parseDuration(%q) unexpected error: %v", tt.input, err)
				return
			}
			if got != tt.wantSec {
				t.Errorf("parseDuration(%q) = %d, want %d", tt.input, got, tt.wantSec)
			}
		})
	}
}

func TestCredEnv(t *testing.T) {
	base := []string{
		"HOME=/home/user",
		"AWS_ACCESS_KEY_ID=old-key",
		"AWS_SECRET_ACCESS_KEY=old-secret",
		"AWS_ENDPOINT_URL=http://localhost:4566", // must be stripped
		"AWS_PROFILE=prod",                       // must be stripped
		"AWS_SHARED_CREDENTIALS_FILE=/etc/creds", // must be stripped
		"PATH=/usr/bin",
	}

	creds := &credentials{
		AccessKeyID:     "ASIA-NEW-KEY",
		SecretAccessKey: "new-secret",
		SessionToken:    "new-token",
		Region:          "us-west-2",
	}

	result := credEnv(creds, base)

	// All AWS_* vars from baseEnv must be gone — credentials, endpoint
	// overrides, profiles, and credential file paths alike.
	for _, e := range result {
		key := envKey(e)
		if strings.HasPrefix(key, "AWS_") {
			// The only AWS_* vars allowed are the ones we explicitly inject.
			allowed := map[string]bool{
				"AWS_ACCESS_KEY_ID":     true,
				"AWS_SECRET_ACCESS_KEY": true,
				"AWS_SESSION_TOKEN":     true,
				"AWS_DEFAULT_REGION":    true,
				"AWS_REGION":            true,
			}
			if !allowed[key] {
				t.Errorf("unexpected AWS_* var in child env: %s", e)
			}
		}
	}

	// Check new credential vars are present with correct values
	check := map[string]string{
		"AWS_ACCESS_KEY_ID":     "ASIA-NEW-KEY",
		"AWS_SECRET_ACCESS_KEY": "new-secret",
		"AWS_SESSION_TOKEN":     "new-token",
		"AWS_DEFAULT_REGION":    "us-west-2",
	}
	for k, v := range check {
		found := false
		for _, e := range result {
			if e == k+"="+v {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected %s=%s in env, not found", k, v)
		}
	}

	// Non-AWS vars must be preserved
	for _, e := range result {
		if strings.HasPrefix(e, "HOME=") {
			goto homeFound
		}
	}
	t.Error("HOME var not preserved")
homeFound:
}

func TestDefaultSessionName(t *testing.T) {
	name := defaultSessionName()
	if name == "" {
		t.Fatal("defaultSessionName() returned empty string")
	}
	// Must start with aws-role-exec
	if !strings.HasPrefix(name, "aws-role-exec") {
		t.Errorf("defaultSessionName() = %q, expected prefix aws-role-exec", name)
	}
	// Must only contain valid STS session name chars
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			t.Errorf("defaultSessionName() contains invalid char %q in %q", c, name)
		}
	}
}

func TestDefaultSessionName_Unique(t *testing.T) {
	// Two consecutive calls must produce different names (crypto-random suffix).
	a := defaultSessionName()
	b := defaultSessionName()
	if a == b {
		t.Errorf("defaultSessionName() returned identical names on successive calls: %q", a)
	}
}

func TestRun_DryRun(t *testing.T) {
	cfg := runConfig{
		roleArn:  "arn:aws:iam::123456789012:role/test-role",
		duration: "1h",
		dryRun:   true,
	}
	if err := run(context.Background(), cfg); err != nil {
		t.Fatalf("run with dry-run: %v", err)
	}
}
