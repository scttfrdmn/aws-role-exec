package main

import (
	"context"
	"strings"
	"testing"
	"time"
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

func TestAssumeRole_InvalidPolicy(t *testing.T) {
	// Policy validation is local — no AWS call should be made.
	_, err := assumeRole(context.Background(), "us-east-1",
		"arn:aws:iam::123456789012:role/test-role",
		"test-session", 3600,
		`not-valid-json`,
	)
	if err == nil {
		t.Fatal("expected error for invalid JSON policy, got nil")
	}
	if !strings.Contains(err.Error(), "invalid JSON") {
		t.Errorf("error should mention invalid JSON, got: %v", err)
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

func TestValidateRoleARN(t *testing.T) {
	tests := []struct {
		arn     string
		wantErr bool
	}{
		{"arn:aws:iam::123456789012:role/MyRole", false},
		{"arn:aws-cn:iam::123456789012:role/MyRole", false},
		{"arn:aws-us-gov:iam::123456789012:role/MyRole", false},
		{"arn:aws-iso:iam::123456789012:role/MyRole", false},
		{"arn:aws-iso-b:iam::123456789012:role/MyRole", false},
		{"arn:aws:iam::123456789012:role/path/to/MyRole", false},
		{"arn:aws:iam::123456789012:role/My.Role@Example", false},
		// invalid cases
		{"", true},
		{"not-an-arn", true},
		{"arn:aws:iam::12345:role/MyRole", true},            // account ID too short
		{"arn:aws:iam::1234567890123:role/MyRole", true},    // account ID too long
		{"arn:aws:iam::123456789012:user/MyUser", true},     // not a role
		{"arn:AWS:iam::123456789012:role/MyRole", true},     // uppercase partition
		{"arn:aws:iam:us-east-1:123456789012:role/R", true}, // region in IAM ARN
	}
	for _, tt := range tests {
		t.Run(tt.arn, func(t *testing.T) {
			err := validateRoleARN(tt.arn)
			if tt.wantErr && err == nil {
				t.Errorf("validateRoleARN(%q) expected error, got nil", tt.arn)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("validateRoleARN(%q) unexpected error: %v", tt.arn, err)
			}
		})
	}
}

func TestValidateSessionName(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{"ValidName", false},
		{"valid-name-123", false},
		{"name@example.com", false},
		{"name=value,key.other", false},
		{strings.Repeat("a", 64), false}, // exactly at limit
		{strings.Repeat("a", 65), true},  // over limit
		{"invalid name", true},           // space
		{"invalid/name", true},           // slash
		{"invalid\x00name", true},        // null byte
		{"invalid:name", true},           // colon
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSessionName(tt.name)
			if tt.wantErr && err == nil {
				t.Errorf("validateSessionName(%q) expected error, got nil", tt.name)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("validateSessionName(%q) unexpected error: %v", tt.name, err)
			}
		})
	}
}

func TestAssumeRole_PolicyTooLong(t *testing.T) {
	// Policy size check is local — no AWS call should be made.
	bigPolicy := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"*","Resource":"*"}]}` +
		strings.Repeat(" ", 2048)
	_, err := assumeRole(context.Background(), "us-east-1",
		"arn:aws:iam::123456789012:role/test-role",
		"test-session", 3600,
		bigPolicy,
	)
	if err == nil {
		t.Fatal("expected error for oversized policy, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("error should mention exceeds, got: %v", err)
	}
}

func TestRun_ContextCancelled(t *testing.T) {
	// A pre-cancelled context must be caught before any AWS call is made.
	ctx, cancel := context.WithTimeout(context.Background(), 0)
	cancel()
	// Give the scheduler a moment so the deadline is definitely past.
	time.Sleep(time.Millisecond)

	cfg := runConfig{
		roleArn:  "arn:aws:iam::123456789012:role/test-role",
		duration: "1h",
		dryRun:   false,
	}
	err := run(ctx, cfg)
	// We expect either a context error or an STS/config error (no real AWS).
	// What we must NOT see is a nil error or a panic.
	if err == nil {
		t.Fatal("expected error with cancelled context, got nil")
	}
}

func TestRun_InvalidRoleARN(t *testing.T) {
	cfg := runConfig{
		roleArn:  "not-a-valid-arn",
		duration: "1h",
	}
	err := run(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error for invalid role ARN, got nil")
	}
	if !strings.Contains(err.Error(), "--role-arn") {
		t.Errorf("error should mention --role-arn flag, got: %v", err)
	}
}

func TestRun_InvalidSessionName(t *testing.T) {
	cfg := runConfig{
		roleArn:     "arn:aws:iam::123456789012:role/test-role",
		duration:    "1h",
		sessionName: "bad name with spaces",
	}
	err := run(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error for invalid session name, got nil")
	}
	if !strings.Contains(err.Error(), "--session-name") {
		t.Errorf("error should mention --session-name flag, got: %v", err)
	}
}
