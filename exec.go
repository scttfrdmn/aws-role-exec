//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// execWithCreds replaces the current process with command, injecting AWS credentials
// into the environment. Uses syscall.Exec on Unix so signal handling is correct
// and the child process truly replaces this one (no zombie, correct exit code).
func execWithCreds(creds *credentials, command []string) error {
	if len(command) == 0 {
		return fmt.Errorf("no command specified")
	}

	env := credEnv(creds, os.Environ())

	// Resolve the command binary path
	bin, err := exec.LookPath(command[0])
	if err != nil {
		return fmt.Errorf("command not found: %s: %w", command[0], err)
	}

	// syscall.Exec replaces the current process image — correct signal forwarding,
	// no intermediate process, exit code propagates naturally to the caller.
	return syscall.Exec(bin, command, env)
}

// credEnv returns a copy of baseEnv with AWS credential vars set/overridden.
// All AWS_* prefixed vars are stripped from baseEnv to prevent inherited
// endpoint overrides (AWS_ENDPOINT_URL), profile redirects (AWS_PROFILE),
// credential file paths (AWS_SHARED_CREDENTIALS_FILE), and other SDK
// configuration from interfering with the freshly-injected credentials.
func credEnv(creds *credentials, baseEnv []string) []string {
	overrides := map[string]string{
		"AWS_ACCESS_KEY_ID":     creds.AccessKeyID,
		"AWS_SECRET_ACCESS_KEY": creds.SecretAccessKey,
		"AWS_SESSION_TOKEN":     creds.SessionToken,
		"AWS_DEFAULT_REGION":    creds.Region,
		"AWS_REGION":            creds.Region,
	}

	// Strip ALL AWS_* vars from the inherited environment so no inherited
	// SDK configuration (endpoint, profile, credentials file, etc.) can
	// interfere with the injected credentials.
	filtered := make([]string, 0, len(baseEnv)+len(overrides))
	for _, e := range baseEnv {
		if !strings.HasPrefix(envKey(e), "AWS_") {
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
	for i := 0; i < len(kv); i++ {
		if kv[i] == '=' {
			return kv[:i]
		}
	}
	return kv
}
