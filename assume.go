package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

type credentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	Expiration      time.Time
	Region          string
}

type runConfig struct {
	roleArn     string
	duration    string
	sessionName string
	region      string
	format      string
	output      string
	policy      string
	dryRun      bool
	command     []string
}

// roleARNRe matches IAM role ARNs across all AWS partitions
// (aws, aws-cn, aws-us-gov, aws-iso, aws-iso-b).
var roleARNRe = regexp.MustCompile(`^arn:[a-z][a-z0-9-]*:iam::[0-9]{12}:role/[\w+=,.@/-]+$`)

// validateRoleARN returns an error if arn does not look like a valid IAM role ARN.
func validateRoleARN(arn string) error {
	if !roleARNRe.MatchString(arn) {
		return fmt.Errorf("--role-arn: invalid IAM role ARN %q (expected arn:PARTITION:iam::ACCOUNT_ID:role/ROLE_NAME)", arn)
	}
	return nil
}

// validateSessionName returns an error if name contains characters outside the
// STS-allowed set [a-zA-Z0-9=,.@-] or exceeds the 64-character maximum.
func validateSessionName(name string) error {
	const maxLen = 64
	if len(name) > maxLen {
		return fmt.Errorf("--session-name: exceeds %d-character limit (%d chars)", maxLen, len(name))
	}
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '=' || c == ',' || c == '.' || c == '@' || c == '-') {
			return fmt.Errorf("--session-name: invalid character %q (allowed: [a-zA-Z0-9=,.@-])", c)
		}
	}
	return nil
}

func run(ctx context.Context, cfg runConfig) error {
	secs, err := parseDuration(cfg.duration)
	if err != nil {
		return fmt.Errorf("--duration: %w", err)
	}

	if err := validateRoleARN(cfg.roleArn); err != nil {
		return err
	}

	sessionName := cfg.sessionName
	if sessionName == "" {
		sessionName = defaultSessionName()
	}
	// Validate user-supplied session names; generated names are already safe.
	if cfg.sessionName != "" {
		if err := validateSessionName(cfg.sessionName); err != nil {
			return err
		}
	}

	region := cfg.region
	if region == "" {
		region = os.Getenv("AWS_DEFAULT_REGION")
	}
	if region == "" {
		region = os.Getenv("AWS_REGION")
	}
	if region == "" {
		region = "us-east-1"
	}

	if cfg.dryRun {
		fmt.Fprintf(os.Stderr, "dry-run: would assume role %s\n", cfg.roleArn)
		fmt.Fprintf(os.Stderr, "  session-name : %s\n", sessionName)
		fmt.Fprintf(os.Stderr, "  duration     : %ds\n", secs)
		fmt.Fprintf(os.Stderr, "  region       : %s\n", region)
		if len(cfg.command) > 0 {
			fmt.Fprintf(os.Stderr, "  command      : %v\n", cfg.command)
		} else {
			fmt.Fprintf(os.Stderr, "  format       : %s\n", cfg.format)
		}
		return nil
	}

	creds, err := assumeRole(ctx, region, cfg.roleArn, sessionName, secs, cfg.policy)
	if err != nil {
		return err
	}
	creds.Region = region

	if len(cfg.command) > 0 {
		return execWithCreds(creds, cfg.command)
	}

	// Guard against a cancelled context before writing the output file.
	// Without this, a cancellation after AssumeRole succeeds could create an
	// O_EXCL-locked file that blocks retries without any credentials inside.
	select {
	case <-ctx.Done():
		return fmt.Errorf("context cancelled before writing credentials: %w", ctx.Err())
	default:
	}

	return printCreds(creds, cfg.format, cfg.output)
}

func assumeRole(ctx context.Context, region, roleArn, sessionName string, durationSecs int32, policy string) (*credentials, error) {
	awsCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	input := &sts.AssumeRoleInput{
		RoleArn:         aws.String(roleArn),
		RoleSessionName: aws.String(sessionName),
		DurationSeconds: aws.Int32(durationSecs),
	}
	if policy != "" {
		// AWS limits inline session policies to 2,048 characters.
		const maxPolicyLen = 2048
		if len(policy) > maxPolicyLen {
			return nil, fmt.Errorf("--policy: exceeds %d-character AWS limit (%d chars)", maxPolicyLen, len(policy))
		}
		if !json.Valid([]byte(policy)) {
			return nil, fmt.Errorf("--policy: invalid JSON")
		}
		input.Policy = aws.String(policy)
	}

	client := sts.NewFromConfig(awsCfg)
	out, err := client.AssumeRole(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("sts AssumeRole: %w", err)
	}

	return &credentials{
		AccessKeyID:     aws.ToString(out.Credentials.AccessKeyId),
		SecretAccessKey: aws.ToString(out.Credentials.SecretAccessKey),
		SessionToken:    aws.ToString(out.Credentials.SessionToken),
		Expiration:      aws.ToTime(out.Credentials.Expiration),
	}, nil
}

// parseDuration accepts Go durations ("1h30m") or HH:MM:SS (for HPC walltime compatibility).
// Returns seconds. Enforces 15m <= duration <= 12h.
func parseDuration(s string) (int32, error) {
	var d time.Duration

	if strings.Count(s, ":") == 2 {
		// HH:MM:SS
		parts := strings.Split(s, ":")
		h, err1 := strconv.Atoi(parts[0])
		m, err2 := strconv.Atoi(parts[1])
		sec, err3 := strconv.Atoi(parts[2])
		if err1 != nil || err2 != nil || err3 != nil {
			return 0, fmt.Errorf("invalid HH:MM:SS format %q", s)
		}
		if h < 0 || h > 12 || m < 0 || m > 59 || sec < 0 || sec > 59 {
			return 0, fmt.Errorf("invalid HH:MM:SS format %q: hours must be 0-12, minutes and seconds 0-59", s)
		}
		d = time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(sec)*time.Second
	} else {
		var err error
		d, err = time.ParseDuration(s)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q (use Go duration like 1h30m or HH:MM:SS)", s)
		}
	}

	const minDuration = 15 * time.Minute
	const maxDuration = 12 * time.Hour

	if d < minDuration {
		return 0, fmt.Errorf("duration %v is below the 15-minute minimum", d)
	}
	if d > maxDuration {
		return 0, fmt.Errorf("duration %v exceeds the 12-hour maximum", d)
	}

	return int32(d.Seconds()), nil
}

func defaultSessionName() string {
	name := "aws-role-exec"
	if u, err := user.Current(); err == nil && u.Username != "" {
		// Keep only [a-zA-Z0-9] — a strict subset of the STS-allowed set
		// [a-zA-Z0-9=,.@-] — to avoid ambiguous characters in CloudTrail.
		safe := strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
				return r
			}
			return '-'
		}, u.Username)
		name = "aws-role-exec-" + safe
	}
	// Use a crypto-random suffix instead of the process ID so session names
	// are not predictable from username + PID, preventing an attacker from
	// correlating or pre-computing CloudTrail session names.
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand should never fail on any supported platform. If it does,
		// warn loudly so the operator knows session names are predictable.
		fmt.Fprintf(os.Stderr, "warning: crypto/rand unavailable, session name will use predictable PID: %v\n", err)
		return fmt.Sprintf("%s-%d", name, os.Getpid())
	}
	return fmt.Sprintf("%s-%s", name, hex.EncodeToString(b))
}
