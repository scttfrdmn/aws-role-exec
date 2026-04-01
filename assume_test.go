package main

import (
	"bytes"
	"context"
	"os"
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

func TestRun_InvalidStsTimeout(t *testing.T) {
	cfg := runConfig{
		roleArn:    "arn:aws:iam::123456789012:role/test-role",
		duration:   "1h",
		stsTimeout: "not-a-duration",
		dryRun:     true, // short-circuits before STS; timeout is validated first
	}
	err := run(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error for invalid --sts-timeout, got nil")
	}
	if !strings.Contains(err.Error(), "--sts-timeout") {
		t.Errorf("error should mention --sts-timeout, got: %v", err)
	}
}

func TestRun_InvalidRegion(t *testing.T) {
	tests := []struct {
		region string
	}{
		{"../../../../etc/passwd"},
		{"US-EAST-1"},
		{"us_east_1"},
		{"not a region"},
	}
	for _, tt := range tests {
		t.Run(tt.region, func(t *testing.T) {
			cfg := runConfig{
				roleArn:  "arn:aws:iam::123456789012:role/test-role",
				duration: "1h",
				region:   tt.region,
			}
			err := run(context.Background(), cfg)
			if err == nil {
				t.Fatalf("expected error for invalid region %q, got nil", tt.region)
			}
			if !strings.Contains(err.Error(), "--region") {
				t.Errorf("error should mention --region flag, got: %v", err)
			}
		})
	}
}

// TestRegionRe directly exercises the compiled regionRe regexp to verify it
// accepts all standard and partition-specific AWS region names and rejects
// malformed or malicious values.
func TestRegionRe(t *testing.T) {
	valid := []string{
		"us-east-1",
		"us-west-2",
		"eu-central-1",
		"ap-southeast-2",
		"me-south-1",
		"af-south-1",
		"us-iso-east-1",  // GovCloud ISO
		"us-isob-east-1", // GovCloud ISO-B
		"us-gov-west-1",  // GovCloud
		"eu-central-2",
	}
	for _, r := range valid {
		if !regionRe.MatchString(r) {
			t.Errorf("regionRe should match valid region %q but did not", r)
		}
	}

	invalid := []string{
		"US-EAST-1",
		"us_east_1",
		"1us-east-1",
		"us-east",
		"",
		"../../../../etc/passwd",
		"us east 1",
	}
	for _, r := range invalid {
		if regionRe.MatchString(r) {
			t.Errorf("regionRe should not match invalid region %q but did", r)
		}
	}
}

// TestExecWithCreds_EmptyCommandElement verifies that passing an empty string
// as the command name produces a clear error instead of an opaque "no such file"
// from exec.LookPath("").
func TestExecWithCreds_EmptyCommandElement(t *testing.T) {
	creds := &credentials{
		AccessKeyID:     "AKIA",
		SecretAccessKey: "secret",
		SessionToken:    "token",
		Region:          "us-east-1",
	}
	err := execWithCreds(creds, []string{""})
	if err == nil {
		t.Fatal("expected error for empty command element, got nil")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error should mention 'empty', got: %v", err)
	}
}

// TestExecWithCreds_EmptyCommandArray verifies that an empty command slice
// produces a clear "no command specified" error.
func TestExecWithCreds_EmptyCommandArray(t *testing.T) {
	creds := &credentials{
		AccessKeyID:     "AKIA",
		SecretAccessKey: "secret",
		SessionToken:    "token",
		Region:          "us-east-1",
	}
	err := execWithCreds(creds, []string{})
	if err == nil {
		t.Fatal("expected error for empty command slice, got nil")
	}
	if !strings.Contains(err.Error(), "no command") {
		t.Errorf("error should mention 'no command', got: %v", err)
	}
}

// TestExecWithCreds_CommandNotFound verifies that passing a command name that
// does not exist in PATH produces a clear "command not found" error instead of
// a generic exec.LookPath message.
func TestExecWithCreds_CommandNotFound(t *testing.T) {
	creds := &credentials{
		AccessKeyID:     "AKIA",
		SecretAccessKey: "secret",
		SessionToken:    "token",
		Region:          "us-east-1",
	}
	err := execWithCreds(creds, []string{"definitely-not-a-real-binary-xyz-abc"})
	if err == nil {
		t.Fatal("expected error for command not in PATH, got nil")
	}
	if !strings.Contains(err.Error(), "command not found") {
		t.Errorf("error should mention 'command not found', got: %v", err)
	}
}

// TestRun_DryRun_WithCommand verifies the dry-run output path when a command
// is provided (prints "command:" instead of "format:").
func TestRun_DryRun_WithCommand(t *testing.T) {
	cfg := runConfig{
		roleArn:  "arn:aws:iam::123456789012:role/test-role",
		duration: "1h",
		dryRun:   true,
		command:  []string{"aws", "s3", "ls"},
	}
	if err := run(context.Background(), cfg); err != nil {
		t.Fatalf("run with dry-run + command: %v", err)
	}
}

// TestRun_RegionPrecedence verifies that AWS_DEFAULT_REGION takes priority
// over AWS_REGION when both environment variables are set.
func TestRun_RegionPrecedence(t *testing.T) {
	t.Setenv("AWS_DEFAULT_REGION", "eu-west-1")
	t.Setenv("AWS_REGION", "us-west-2")

	// dry-run so no STS call is made; the region resolved is printed to stderr.
	cfg := runConfig{
		roleArn:  "arn:aws:iam::123456789012:role/test-role",
		duration: "1h",
		dryRun:   true,
	}
	// We just need to confirm no error — if regionRe rejects the resolved
	// region, run() would return an error. The correct region (eu-west-1) is
	// valid; us-west-2 is also valid, so the test is really about which one
	// is chosen. We verify that by redirecting stderr and checking its content.
	old := os.Stderr
	r, w, _ := os.Pipe()
	os.Stderr = w
	err := run(context.Background(), cfg)
	w.Close()
	os.Stderr = old
	var buf bytes.Buffer
	buf.ReadFrom(r)
	if err != nil {
		t.Fatalf("dry-run with region env vars: %v", err)
	}
	if !strings.Contains(buf.String(), "eu-west-1") {
		t.Errorf("expected AWS_DEFAULT_REGION (eu-west-1) to win, got dry-run output:\n%s", buf.String())
	}
	if strings.Contains(buf.String(), "us-west-2") {
		t.Errorf("AWS_REGION (us-west-2) should not appear when AWS_DEFAULT_REGION is set:\n%s", buf.String())
	}
}

// TestValidatePolicyStructure exercises the policy structure validator.
func TestValidatePolicyStructure(t *testing.T) {
	tests := []struct {
		name    string
		policy  string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid minimal policy",
			policy:  `{"Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"*"}]}`,
			wantErr: false,
		},
		{
			name:    "valid policy with Version",
			policy:  `{"Version":"2012-10-17","Statement":[{"Effect":"Deny","Action":"*","Resource":"*"}]}`,
			wantErr: false,
		},
		{
			name:    "missing Statement",
			policy:  `{"foo":"bar"}`,
			wantErr: true,
			errMsg:  "Statement",
		},
		{
			name:    "Statement not an array",
			policy:  `{"Statement":"Allow"}`,
			wantErr: true,
			errMsg:  "Statement",
		},
		{
			name:    "statement missing Effect",
			policy:  `{"Statement":[{"Action":"s3:GetObject","Resource":"*"}]}`,
			wantErr: true,
			errMsg:  "Effect",
		},
		{
			name:    "invalid Effect value",
			policy:  `{"Statement":[{"Effect":"Maybe","Action":"s3:*","Resource":"*"}]}`,
			wantErr: true,
			errMsg:  "Effect",
		},
		{
			name:    "Effect not a string",
			policy:  `{"Statement":[{"Effect":42,"Action":"s3:*","Resource":"*"}]}`,
			wantErr: true,
			errMsg:  "Effect",
		},
		{
			name:    "empty Statement array is valid",
			policy:  `{"Statement":[]}`,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePolicyStructure(tt.policy)
			if tt.wantErr && err == nil {
				t.Errorf("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if tt.wantErr && tt.errMsg != "" && err != nil && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("error should contain %q, got: %v", tt.errMsg, err)
			}
		})
	}
}

// TestAssumeRole_PolicyMissingStatement verifies that a syntactically valid
// JSON policy that lacks a Statement array is rejected locally.
func TestAssumeRole_PolicyMissingStatement(t *testing.T) {
	_, err := assumeRole(context.Background(), "us-east-1",
		"arn:aws:iam::123456789012:role/test-role",
		"test-session", 3600,
		`{"foo":"bar"}`,
	)
	if err == nil {
		t.Fatal("expected error for policy missing Statement, got nil")
	}
	if !strings.Contains(err.Error(), "Statement") {
		t.Errorf("error should mention Statement, got: %v", err)
	}
}

func TestEnvKey(t *testing.T) {
	tests := []struct {
		kv   string
		want string
	}{
		{"HOME=/home/user", "HOME"},
		{"AWS_ACCESS_KEY_ID=AKIA123", "AWS_ACCESS_KEY_ID"},
		{"NOEQUALS", "NOEQUALS"},
		{"=value", ""},
		{"KEY=", "KEY"},
		{"KEY=val=extra", "KEY"},
	}
	for _, tt := range tests {
		t.Run(tt.kv, func(t *testing.T) {
			if got := envKey(tt.kv); got != tt.want {
				t.Errorf("envKey(%q) = %q, want %q", tt.kv, got, tt.want)
			}
		})
	}
}
