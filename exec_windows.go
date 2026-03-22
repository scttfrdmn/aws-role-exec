//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
)

// execWithCreds on Windows uses os/exec.Command since syscall.Exec is not available.
// The child process is run as a subprocess; exit code is propagated.
func execWithCreds(creds *credentials, command []string) error {
	if len(command) == 0 {
		return fmt.Errorf("no command specified")
	}

	env := credEnv(creds, os.Environ())

	cmd := exec.Command(command[0], command[1:]...)
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		return err
	}
	return nil
}

// credEnv returns a copy of baseEnv with AWS credential vars set/overridden.
func credEnv(creds *credentials, baseEnv []string) []string {
	overrides := map[string]string{
		"AWS_ACCESS_KEY_ID":     creds.AccessKeyID,
		"AWS_SECRET_ACCESS_KEY": creds.SecretAccessKey,
		"AWS_SESSION_TOKEN":     creds.SessionToken,
		"AWS_DEFAULT_REGION":    creds.Region,
		"AWS_REGION":            creds.Region,
	}

	// Filter out any existing AWS credential vars from the base environment
	filtered := make([]string, 0, len(baseEnv)+len(overrides))
	for _, e := range baseEnv {
		key := envKey(e)
		if _, overridden := overrides[key]; !overridden {
			filtered = append(filtered, e)
		}
	}

	// Append the new credential vars
	for k, v := range overrides {
		filtered = append(filtered, k+"="+v)
	}
	return filtered
}

func envKey(kv string) string {
	for i, c := range kv {
		if c == '=' {
			return kv[:i]
		}
	}
	return kv
}
