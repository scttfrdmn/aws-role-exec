package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testCreds = &credentials{
	AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
	SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	SessionToken:    "AQoXnyc4lcK4w",
	Expiration:      time.Date(2026, 3, 21, 15, 0, 0, 0, time.UTC),
	Region:          "us-east-1",
}

func TestPrintEnv(t *testing.T) {
	var buf bytes.Buffer
	if err := printEnv(&buf, testCreds); err != nil {
		t.Fatalf("printEnv: %v", err)
	}
	out := buf.String()

	// Values are single-quoted for safe eval in POSIX shells.
	checks := []string{
		"export AWS_ACCESS_KEY_ID='AKIAIOSFODNN7EXAMPLE'",
		"export AWS_SECRET_ACCESS_KEY='wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY'",
		"export AWS_SESSION_TOKEN='AQoXnyc4lcK4w'",
		"export AWS_DEFAULT_REGION='us-east-1'",
		"export AWS_REGION='us-east-1'",
		"# expires: 2026-03-21T15:00:00Z",
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("printEnv output missing %q\ngot:\n%s", want, out)
		}
	}
}

func TestPrintJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := printJSON(&buf, testCreds); err != nil {
		t.Fatalf("printJSON: %v", err)
	}

	var got struct {
		AccessKeyID     string `json:"AccessKeyId"`
		SecretAccessKey string `json:"SecretAccessKey"`
		SessionToken    string `json:"SessionToken"`
		Expiration      string `json:"Expiration"`
		Region          string `json:"Region"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal JSON output: %v\noutput was:\n%s", err, buf.String())
	}

	if got.AccessKeyID != testCreds.AccessKeyID {
		t.Errorf("AccessKeyId: got %q, want %q", got.AccessKeyID, testCreds.AccessKeyID)
	}
	if got.SecretAccessKey != testCreds.SecretAccessKey {
		t.Errorf("SecretAccessKey: got %q, want %q", got.SecretAccessKey, testCreds.SecretAccessKey)
	}
	if got.SessionToken != testCreds.SessionToken {
		t.Errorf("SessionToken: got %q, want %q", got.SessionToken, testCreds.SessionToken)
	}
	if got.Expiration != "2026-03-21T15:00:00Z" {
		t.Errorf("Expiration: got %q, want %q", got.Expiration, "2026-03-21T15:00:00Z")
	}
	if got.Region != testCreds.Region {
		t.Errorf("Region: got %q, want %q", got.Region, testCreds.Region)
	}
}

func TestPrintCredentialsFile(t *testing.T) {
	var buf bytes.Buffer
	if err := printCredentialsFile(&buf, testCreds); err != nil {
		t.Fatalf("printCredentialsFile: %v", err)
	}
	out := buf.String()

	checks := []string{
		"[default]",
		"aws_access_key_id = AKIAIOSFODNN7EXAMPLE",
		"aws_secret_access_key = wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		"aws_session_token = AQoXnyc4lcK4w",
		"region = us-east-1",
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("printCredentialsFile output missing %q\ngot:\n%s", want, out)
		}
	}
}

func TestPrintCreds_UnknownFormat(t *testing.T) {
	err := printCreds(testCreds, "xml", "")
	if err == nil {
		t.Fatal("expected error for unknown format, got nil")
	}
	if !strings.Contains(err.Error(), "xml") {
		t.Errorf("error should mention format name, got: %v", err)
	}
}

func TestPrintCreds_EnvDefault(t *testing.T) {
	// Empty format string should route to env output without error.
	// Redirect stdout to avoid test output noise.
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := printCreds(testCreds, "", "")

	w.Close()
	os.Stdout = old
	r.Close()

	if err != nil {
		t.Fatalf("printCreds with empty format: %v", err)
	}
}

func TestPrintCreds_FileOutput(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "credentials")

	if err := printCreds(testCreds, "credentials-file", outPath); err != nil {
		t.Fatalf("printCreds to file: %v", err)
	}

	info, err := os.Stat(outPath)
	if err != nil {
		t.Fatalf("stat output file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("file permissions: got %04o, want 0600", perm)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	if !strings.Contains(string(data), "[default]") {
		t.Errorf("file missing [default] section, got:\n%s", data)
	}
}

func TestPrintCreds_FileOutput_RefusesExisting(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "credentials")

	// First write — must succeed.
	if err := printCreds(testCreds, "credentials-file", outPath); err != nil {
		t.Fatalf("first write: %v", err)
	}

	// Second write to the same path — must fail with a clear error (O_EXCL).
	err := printCreds(testCreds, "credentials-file", outPath)
	if err == nil {
		t.Fatal("second write to existing path: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("expected 'already exists' in error, got: %v", err)
	}
}

// TestShellQuote verifies the shellQuote helper covers all injection-relevant cases.
func TestShellQuote(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"simple", "'simple'"},
		{"with space", "'with space'"},
		{"with$dollar", "'with$dollar'"},
		{"with`backtick`", "'with`backtick`'"},
		{"it's", `'it'\''s'`},
		{"a'b'c", `'a'\''b'\''c'`},
		{"", "''"},
		{"/slashes/are/fine", "'/slashes/are/fine'"},
		{"key=value", "'key=value'"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := shellQuote(tt.input)
			if got != tt.want {
				t.Errorf("shellQuote(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestPrintEnv_SpecialChars verifies that shell-special characters in credential
// values are safely single-quoted and do not break eval.
func TestPrintEnv_SpecialChars(t *testing.T) {
	creds := &credentials{
		AccessKeyID:     "AKIA$SPECIAL",
		SecretAccessKey: "secret`with`backticks",
		SessionToken:    "token/with/slashes+and=equals",
		Region:          "us-east-1",
		Expiration:      time.Date(2026, 3, 21, 15, 0, 0, 0, time.UTC),
	}
	var buf bytes.Buffer
	if err := printEnv(&buf, creds); err != nil {
		t.Fatalf("printEnv: %v", err)
	}
	out := buf.String()

	checks := []string{
		"export AWS_ACCESS_KEY_ID='AKIA$SPECIAL'",
		"export AWS_SECRET_ACCESS_KEY='secret`with`backticks'",
		"export AWS_SESSION_TOKEN='token/with/slashes+and=equals'",
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("printEnv output missing %q\ngot:\n%s", want, out)
		}
	}
}

// TestPrintEnv_SingleQuoteEscaping verifies that a single quote inside a
// credential value is correctly escaped as '\” in the output.
func TestPrintEnv_SingleQuoteEscaping(t *testing.T) {
	creds := &credentials{
		AccessKeyID:     "KEY",
		SecretAccessKey: "it's-a-secret",
		SessionToken:    "tok",
		Region:          "us-east-1",
		Expiration:      time.Date(2026, 3, 21, 15, 0, 0, 0, time.UTC),
	}
	var buf bytes.Buffer
	if err := printEnv(&buf, creds); err != nil {
		t.Fatalf("printEnv: %v", err)
	}
	out := buf.String()
	// "it's-a-secret" → 'it'\''s-a-secret'
	if !strings.Contains(out, `'it'\''s-a-secret'`) {
		t.Errorf("single quote not escaped correctly, got:\n%s", out)
	}
}

// TestPrintCredentialsFile_NewlineInjection verifies that a credential value
// containing a newline is rejected, preventing INI key injection.
func TestPrintCredentialsFile_NewlineInjection(t *testing.T) {
	creds := &credentials{
		AccessKeyID:     "AKIA\naws_secret_access_key = injected",
		SecretAccessKey: "secret",
		SessionToken:    "token",
		Region:          "us-east-1",
	}
	var buf bytes.Buffer
	err := printCredentialsFile(&buf, creds)
	if err == nil {
		t.Fatal("expected error for newline in credential value, got nil")
	}
	if !strings.Contains(err.Error(), "newline") {
		t.Errorf("error should mention newline, got: %v", err)
	}
	// No partial output must be written before the error is detected.
	if buf.Len() != 0 {
		t.Errorf("expected no output on error, got %d bytes:\n%s", buf.Len(), buf.String())
	}
}

// TestPrintCredentialsFile_CarriageReturnInjection verifies that \r is also rejected.
func TestPrintCredentialsFile_CarriageReturnInjection(t *testing.T) {
	creds := &credentials{
		AccessKeyID:     "AKIA\rinjected",
		SecretAccessKey: "secret",
		SessionToken:    "token",
		Region:          "us-east-1",
	}
	var buf bytes.Buffer
	err := printCredentialsFile(&buf, creds)
	if err == nil {
		t.Fatal("expected error for carriage return in credential value, got nil")
	}
}

// TestPrintCredentialsFile_NewlineInRegion verifies that a newline in the
// region field is rejected, even though it is the last field written.
func TestPrintCredentialsFile_NewlineInRegion(t *testing.T) {
	creds := &credentials{
		AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		SecretAccessKey: "secret",
		SessionToken:    "token",
		Region:          "us-east-1\ninjected = bad",
	}
	var buf bytes.Buffer
	err := printCredentialsFile(&buf, creds)
	if err == nil {
		t.Fatal("expected error for newline in region value, got nil")
	}
	if !strings.Contains(err.Error(), "newline") {
		t.Errorf("error should mention newline, got: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("expected no output on error, got %d bytes:\n%s", buf.Len(), buf.String())
	}
}

// TestPrintCreds_PathTraversal verifies that paths escaping the current
// directory via ".." are rejected before any file is created.
func TestPrintCreds_PathTraversal(t *testing.T) {
	err := printCreds(testCreds, "env", "../../etc/passwd")
	if err == nil {
		t.Fatal("expected error for traversal path, got nil")
	}
	if !strings.Contains(err.Error(), "escapes") {
		t.Errorf("error should mention 'escapes', got: %v", err)
	}
}
