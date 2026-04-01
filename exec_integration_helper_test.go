//go:build integration

package main

import (
	"context"
	"fmt"
	"os"
	"testing"
)

// TestMain intercepts the GO_TEST_SUBPROCESS sentinel used by
// TestRun_ExecWithCreds_Substrate to test the syscall.Exec path.
// On Unix, syscall.Exec replaces the test process with the child command,
// so we cannot test execWithCreds from within a normal test goroutine.
// Instead, that test re-invokes the test binary as a subprocess with the
// sentinel set, and this TestMain routes it to runSubprocessHelper instead
// of running any actual test functions.
func TestMain(m *testing.M) {
	if os.Getenv("GO_TEST_SUBPROCESS") == "1" {
		runSubprocessHelper()
		// On Unix, syscall.Exec in run() replaces this process — unreachable.
		// On Windows, cmd.Run() returns normally and we fall through here.
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runSubprocessHelper() {
	outFile := os.Getenv("SUBPROCESS_OUTPUT")
	roleArn := os.Getenv("SUBPROCESS_ROLE_ARN")
	region := os.Getenv("SUBPROCESS_REGION")
	if region == "" {
		region = "us-east-1"
	}
	if outFile == "" || roleArn == "" {
		fmt.Fprintln(os.Stderr, "subprocess: SUBPROCESS_OUTPUT and SUBPROCESS_ROLE_ARN must be set")
		os.Exit(1)
	}
	cfg := runConfig{
		roleArn:  roleArn,
		duration: "1h",
		region:   region,
		// Write the child process environment to the output file so the
		// parent test can verify credential injection.
		command: []string{"sh", "-c", "env > " + outFile},
	}
	if err := run(context.Background(), cfg); err != nil {
		fmt.Fprintf(os.Stderr, "subprocess run error: %v\n", err)
		os.Exit(1)
	}
}
