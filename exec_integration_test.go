//go:build integration

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	substrate "github.com/scttfrdmn/substrate"
)

// createTestRole creates an IAM role in the substrate test server so AssumeRole can find it.
func createTestRole(t *testing.T, ctx context.Context, region, endpointURL, roleName string) {
	t.Helper()
	awsCfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithBaseEndpoint(endpointURL),
	)
	if err != nil {
		t.Fatalf("load config for IAM: %v", err)
	}
	iamClient := iam.NewFromConfig(awsCfg)
	_, err = iamClient.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 aws.String(roleName),
		AssumeRolePolicyDocument: aws.String(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"ec2.amazonaws.com"},"Action":"sts:AssumeRole"}]}`),
	})
	if err != nil {
		t.Fatalf("create test role %s: %v", roleName, err)
	}
}

func TestAssumeRole_Substrate(t *testing.T) {
	ts := substrate.StartTestServer(t)
	t.Setenv("AWS_ENDPOINT_URL", ts.URL)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")

	ctx := context.Background()
	createTestRole(t, ctx, "us-east-1", ts.URL, "test-role")

	creds, err := assumeRole(ctx, "us-east-1",
		"arn:aws:iam::123456789012:role/test-role",
		"test-session",
		3600,
		"",
	)
	if err != nil {
		t.Fatalf("assumeRole: %v", err)
	}
	if creds.AccessKeyID == "" {
		t.Error("expected non-empty AccessKeyID")
	}
	if creds.SecretAccessKey == "" {
		t.Error("expected non-empty SecretAccessKey")
	}
	if creds.SessionToken == "" {
		t.Error("expected non-empty SessionToken")
	}
	t.Logf("assumed role: key=%s expiry=%s", creds.AccessKeyID, creds.Expiration)
}

func TestAssumeRole_WithPolicy_Substrate(t *testing.T) {
	ts := substrate.StartTestServer(t)
	t.Setenv("AWS_ENDPOINT_URL", ts.URL)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")

	ctx := context.Background()
	createTestRole(t, ctx, "us-east-1", ts.URL, "test-role")

	policy := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"*"}]}`

	creds, err := assumeRole(ctx, "us-east-1",
		"arn:aws:iam::123456789012:role/test-role",
		"scoped-session",
		900,
		policy,
	)
	if err != nil {
		t.Fatalf("assumeRole with policy: %v", err)
	}
	if creds.AccessKeyID == "" {
		t.Error("expected non-empty AccessKeyID")
	}
	t.Logf("scoped session: key=%s", creds.AccessKeyID)
}

func TestRun_EnvFormat_Substrate(t *testing.T) {
	ts := substrate.StartTestServer(t)
	t.Setenv("AWS_ENDPOINT_URL", ts.URL)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")

	ctx := context.Background()
	createTestRole(t, ctx, "us-east-1", ts.URL, "test-role")

	outFile := filepath.Join(t.TempDir(), "creds.env")
	cfg := runConfig{
		roleArn:  "arn:aws:iam::123456789012:role/test-role",
		duration: "1h",
		region:   "us-east-1",
		format:   "env",
		output:   outFile,
	}
	if err := run(ctx, cfg); err != nil {
		t.Fatalf("run env format: %v", err)
	}

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	out := string(data)
	for _, want := range []string{
		"export AWS_ACCESS_KEY_ID=",
		"export AWS_SECRET_ACCESS_KEY=",
		"export AWS_SESSION_TOKEN=",
		"export AWS_DEFAULT_REGION=us-east-1",
		"# expires:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("env output missing %q\ngot:\n%s", want, out)
		}
	}
}

func TestRun_JSONFormat_Substrate(t *testing.T) {
	ts := substrate.StartTestServer(t)
	t.Setenv("AWS_ENDPOINT_URL", ts.URL)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")

	ctx := context.Background()
	createTestRole(t, ctx, "us-east-1", ts.URL, "test-role")

	outFile := filepath.Join(t.TempDir(), "creds.json")
	cfg := runConfig{
		roleArn:  "arn:aws:iam::123456789012:role/test-role",
		duration: "1h",
		region:   "us-east-1",
		format:   "json",
		output:   outFile,
	}
	if err := run(ctx, cfg); err != nil {
		t.Fatalf("run json format: %v", err)
	}

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	var got struct {
		AccessKeyID     string `json:"AccessKeyId"`
		SecretAccessKey string `json:"SecretAccessKey"`
		SessionToken    string `json:"SessionToken"`
		Expiration      string `json:"Expiration"`
		Region          string `json:"Region"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal JSON output: %v\noutput:\n%s", err, data)
	}
	if got.AccessKeyID == "" {
		t.Error("JSON output missing AccessKeyId")
	}
	if got.Region != "us-east-1" {
		t.Errorf("JSON Region: got %q, want %q", got.Region, "us-east-1")
	}
}

func TestRun_CredentialsFileFormat_Substrate(t *testing.T) {
	ts := substrate.StartTestServer(t)
	t.Setenv("AWS_ENDPOINT_URL", ts.URL)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")

	ctx := context.Background()
	createTestRole(t, ctx, "us-east-1", ts.URL, "test-role")

	outFile := filepath.Join(t.TempDir(), "credentials")
	cfg := runConfig{
		roleArn:  "arn:aws:iam::123456789012:role/test-role",
		duration: "1h",
		region:   "us-east-1",
		format:   "credentials-file",
		output:   outFile,
	}
	if err := run(ctx, cfg); err != nil {
		t.Fatalf("run credentials-file format: %v", err)
	}

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	out := string(data)
	for _, want := range []string{
		"[default]",
		"aws_access_key_id =",
		"aws_secret_access_key =",
		"aws_session_token =",
		"region = us-east-1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("credentials-file output missing %q\ngot:\n%s", want, out)
		}
	}
}

func TestRun_RoleNotFound_Substrate(t *testing.T) {
	ts := substrate.StartTestServer(t)
	t.Setenv("AWS_ENDPOINT_URL", ts.URL)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")

	cfg := runConfig{
		roleArn:  "arn:aws:iam::123456789012:role/nonexistent-role",
		duration: "1h",
		region:   "us-east-1",
		format:   "env",
	}
	err := run(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error for nonexistent role, got nil")
	}
}

func TestRun_RegionFromEnv_Substrate(t *testing.T) {
	ts := substrate.StartTestServer(t)
	t.Setenv("AWS_ENDPOINT_URL", ts.URL)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_DEFAULT_REGION", "eu-west-1")

	ctx := context.Background()
	createTestRole(t, ctx, "eu-west-1", ts.URL, "test-role")

	outFile := filepath.Join(t.TempDir(), "creds.json")
	cfg := runConfig{
		roleArn:  "arn:aws:iam::123456789012:role/test-role",
		duration: "1h",
		// region intentionally empty — should pick up AWS_DEFAULT_REGION
		format: "json",
		output: outFile,
	}
	if err := run(ctx, cfg); err != nil {
		t.Fatalf("run with region from env: %v", err)
	}

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	var got struct {
		Region string `json:"Region"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Region != "eu-west-1" {
		t.Errorf("Region: got %q, want %q", got.Region, "eu-west-1")
	}
}
