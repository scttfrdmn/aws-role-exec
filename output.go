package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// errWriter wraps an io.Writer and remembers the first write error.
// Subsequent writes are no-ops once an error has been recorded, allowing
// callers to issue multiple fmt.Fprintf calls and check a single error at
// the end rather than checking each call individually.
type errWriter struct {
	w   io.Writer
	err error
}

func (ew *errWriter) Write(p []byte) (int, error) {
	if ew.err != nil {
		return 0, ew.err
	}
	n, err := ew.w.Write(p)
	ew.err = err
	return n, err
}

// shellQuote wraps s in single quotes and escapes any embedded single quotes
// using the standard POSIX shell idiom ' → '\”  making the output safe for
// eval in any POSIX-compatible shell (bash, sh, zsh, dash).
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// sanitizeCredValue rejects any credential value that contains a newline or
// carriage return. AWS never legitimately issues credentials with such
// characters, so their presence always indicates corruption or an attack.
// Failing hard prevents injected lines from being parsed as valid INI
// directives in the credentials-file format.
func sanitizeCredValue(name, value string) error {
	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("credential value for %s contains a newline, which is invalid", name)
	}
	return nil
}

func printCreds(creds *credentials, format, outputPath string) (retErr error) {
	var w io.Writer = os.Stdout
	if outputPath != "" {
		clean := filepath.Clean(outputPath)
		// Reject paths that escape the current directory via "..".
		// filepath.Clean collapses traversal sequences but preserves leading
		// ".." components, making them detectable here.
		if strings.HasPrefix(clean, "..") {
			return fmt.Errorf("output path %q escapes the current directory via ..", outputPath)
		}
		// O_EXCL refuses to create the file if it already exists (as a regular
		// file or a symlink), preventing symlink attacks in shared directories
		// such as /tmp or HPC job scratch paths. Remove the file first if you
		// need to overwrite it.
		f, err := os.OpenFile(clean, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			if errors.Is(err, fs.ErrExist) {
				return fmt.Errorf("output file %q already exists: remove it first to prevent symlink attacks", outputPath)
			}
			return fmt.Errorf("open output file: %w", err)
		}
		// Surface close errors (e.g. flush failures on NFS) only when no
		// earlier write error occurred, to avoid masking a more specific error.
		// On close failure, remove the file so O_EXCL does not block retries.
		defer func() {
			if cerr := f.Close(); cerr != nil && retErr == nil {
				retErr = fmt.Errorf("close output file: %w", cerr)
				os.Remove(clean) //nolint:errcheck // best-effort cleanup; original close error takes priority
			}
		}()
		w = f
	}

	switch format {
	case "env", "":
		return printEnv(w, creds)
	case "json":
		return printJSON(w, creds)
	case "credentials-file":
		return printCredentialsFile(w, creds)
	default:
		return fmt.Errorf("unknown format %q: must be env, json, or credentials-file", format)
	}
}

func printEnv(w io.Writer, creds *credentials) error {
	// Single-quote all credential values so the output is safe for
	// eval in POSIX shells. Without quoting, characters like $, `, ;,
	// or newlines in a session token could execute arbitrary commands.
	ew := &errWriter{w: w}
	fmt.Fprintf(ew, "export AWS_ACCESS_KEY_ID=%s\n", shellQuote(creds.AccessKeyID))
	fmt.Fprintf(ew, "export AWS_SECRET_ACCESS_KEY=%s\n", shellQuote(creds.SecretAccessKey))
	fmt.Fprintf(ew, "export AWS_SESSION_TOKEN=%s\n", shellQuote(creds.SessionToken))
	fmt.Fprintf(ew, "export AWS_DEFAULT_REGION=%s\n", shellQuote(creds.Region))
	fmt.Fprintf(ew, "export AWS_REGION=%s\n", shellQuote(creds.Region))
	// The expiration is a fixed-format timestamp from time.Time — no injection risk.
	fmt.Fprintf(ew, "# expires: %s\n", creds.Expiration.UTC().Format("2006-01-02T15:04:05Z"))
	return ew.err
}

func printJSON(w io.Writer, creds *credentials) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(struct {
		AccessKeyID     string `json:"AccessKeyId"`
		SecretAccessKey string `json:"SecretAccessKey"`
		SessionToken    string `json:"SessionToken"`
		Expiration      string `json:"Expiration"`
		Region          string `json:"Region"`
	}{
		AccessKeyID:     creds.AccessKeyID,
		SecretAccessKey: creds.SecretAccessKey,
		SessionToken:    creds.SessionToken,
		Expiration:      creds.Expiration.UTC().Format("2006-01-02T15:04:05Z"),
		Region:          creds.Region,
	})
}

func printCredentialsFile(w io.Writer, creds *credentials) error {
	fields := []struct{ name, value string }{
		{"aws_access_key_id", creds.AccessKeyID},
		{"aws_secret_access_key", creds.SecretAccessKey},
		{"aws_session_token", creds.SessionToken},
		{"region", creds.Region},
	}
	// Validate all values before writing so a bad value never produces
	// partial output (which would leave a corrupt file on disk).
	for _, f := range fields {
		if err := sanitizeCredValue(f.name, f.value); err != nil {
			return err
		}
	}
	ew := &errWriter{w: w}
	fmt.Fprintf(ew, "[default]\n")
	for _, f := range fields {
		fmt.Fprintf(ew, "%s = %s\n", f.name, f.value)
	}
	return ew.err
}
