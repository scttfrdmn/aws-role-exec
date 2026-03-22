//go:build integration

package main

import (
	"context"
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
