package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var version = "dev"

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	var (
		roleArn     string
		duration    string
		sessionName string
		region      string
		format      string
		output      string
		policy      string
		dryRun      bool
	)

	cmd := &cobra.Command{
		Use:   "aws-role-exec --role-arn ARN [flags] [-- command [args...]]",
		Short: "Assume an AWS IAM role and exec a command with the credentials",
		Long: `aws-role-exec assumes an AWS IAM role via sts:AssumeRole and either:
  - Execs a child process with the credentials injected into its environment (-- cmd args)
  - Prints credentials as shell exports for eval (--format env)
  - Prints credentials as JSON (--format json)
  - Writes an AWS credentials file (--format credentials-file --output /path/to/file)`,
		Version:      version,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd.Context(), runConfig{
				roleArn:     roleArn,
				duration:    duration,
				sessionName: sessionName,
				region:      region,
				format:      format,
				output:      output,
				policy:      policy,
				dryRun:      dryRun,
				command:     args,
			})
		},
	}

	f := cmd.Flags()
	f.StringVar(&roleArn, "role-arn", "", "IAM role ARN to assume (required)")
	f.StringVar(&duration, "duration", "1h", "Credential lifetime: Go duration (1h30m) or HH:MM:SS (walltime-compatible)")
	f.StringVar(&sessionName, "session-name", "", "STS session name (default: aws-role-exec-<user>-<pid>)")
	f.StringVar(&region, "region", "", "AWS region (default: AWS_DEFAULT_REGION or us-east-1)")
	f.StringVar(&format, "format", "env", "Output format when no command given: env | json | credentials-file")
	f.StringVar(&output, "output", "", "Output file path for credentials-file format (default: stdout)")
	f.StringVar(&policy, "policy", "", "Inline JSON session policy to scope credentials further")
	f.BoolVar(&dryRun, "dry-run", false, "Print what would happen without calling STS")

	_ = cmd.MarkFlagRequired("role-arn")

	return cmd
}
