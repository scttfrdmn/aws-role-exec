package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

func printCreds(creds *credentials, format, outputPath string) error {
	var w io.Writer = os.Stdout
	if outputPath != "" {
		// O_EXCL refuses to create the file if it already exists (as a regular
		// file or a symlink), preventing symlink attacks in shared directories
		// such as /tmp or HPC job scratch paths. Remove the file first if you
		// need to overwrite it.
		f, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			if os.IsExist(err) {
				return fmt.Errorf("output file %q already exists: remove it first to prevent symlink attacks", outputPath)
			}
			return fmt.Errorf("open output file: %w", err)
		}
		defer f.Close()
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
	fmt.Fprintf(w, "export AWS_ACCESS_KEY_ID=%s\n", creds.AccessKeyID)
	fmt.Fprintf(w, "export AWS_SECRET_ACCESS_KEY=%s\n", creds.SecretAccessKey)
	fmt.Fprintf(w, "export AWS_SESSION_TOKEN=%s\n", creds.SessionToken)
	fmt.Fprintf(w, "export AWS_DEFAULT_REGION=%s\n", creds.Region)
	fmt.Fprintf(w, "export AWS_REGION=%s\n", creds.Region)
	fmt.Fprintf(w, "# expires: %s\n", creds.Expiration.UTC().Format("2006-01-02T15:04:05Z"))
	return nil
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
	fmt.Fprintf(w, "[default]\n")
	fmt.Fprintf(w, "aws_access_key_id = %s\n", creds.AccessKeyID)
	fmt.Fprintf(w, "aws_secret_access_key = %s\n", creds.SecretAccessKey)
	fmt.Fprintf(w, "aws_session_token = %s\n", creds.SessionToken)
	fmt.Fprintf(w, "region = %s\n", creds.Region)
	return nil
}
