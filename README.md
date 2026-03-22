# aws-role-exec

Assume an AWS IAM role and exec a command with the credentials — `sudo` for AWS roles.

`aws-role-exec` calls `sts:AssumeRole`, injects the temporary credentials into a child process environment, and (on Unix) replaces itself with that process via `syscall.Exec`. No daemons, no config files, no credential files written to disk unless you explicitly ask for it. The credentials live only in the child process environment and expire automatically at the end of the session.

---

## Install

### go install (latest)
```bash
go install github.com/scttfrdmn/aws-role-exec@latest
```

### Prebuilt binary
Download from the [Releases](https://github.com/scttfrdmn/aws-role-exec/releases) page:
```bash
# Example for Linux amd64
curl -Lo aws-role-exec.tar.gz \
  https://github.com/scttfrdmn/aws-role-exec/releases/latest/download/aws-role-exec_linux_amd64.tar.gz
tar -xzf aws-role-exec.tar.gz
sudo mv aws-role-exec /usr/local/bin/
```

### Homebrew (future)
```bash
# Coming in v1.0
brew install scttfrdmn/tap/aws-role-exec
```

---

## Quick start

### Exec pattern — replace current process with the command
The credentials are injected into the child process environment. When the child exits, the credentials are gone. On Unix, `syscall.Exec` is used so the child truly replaces this process — correct signal handling, no zombie, exit code propagates naturally.
```bash
aws-role-exec --role-arn arn:aws:iam::123456789012:role/my-role -- python3 analysis.py --input /data
```

### Eval pattern — export credentials into your current shell
```bash
eval $(aws-role-exec --role-arn arn:aws:iam::123456789012:role/my-role --format env)
# Your shell now has AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_SESSION_TOKEN set.
```

### JSON pattern — machine-readable output for scripts
```bash
aws-role-exec --role-arn arn:aws:iam::123456789012:role/my-role --format json
```
Output:
```json
{
  "AccessKeyId": "ASIA...",
  "SecretAccessKey": "...",
  "SessionToken": "...",
  "Expiration": "2026-03-21T20:00:00Z",
  "Region": "us-east-1"
}
```

### Credentials file pattern — write ~/.aws/credentials format
```bash
aws-role-exec \
  --role-arn arn:aws:iam::123456789012:role/my-role \
  --format credentials-file \
  --output /tmp/job-creds

AWS_SHARED_CREDENTIALS_FILE=/tmp/job-creds aws s3 ls s3://my-bucket/
```

---

## Flags

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--role-arn` | string | *(required)* | IAM role ARN to assume |
| `--duration` | string | `1h` | Credential lifetime. Accepts Go durations (`1h30m`, `45m`) or HPC walltime format (`HH:MM:SS`). Min: 15m, max: 12h |
| `--session-name` | string | `aws-role-exec-<user>-<pid>` | STS RoleSessionName. Appears in CloudTrail |
| `--region` | string | `AWS_DEFAULT_REGION` or `us-east-1` | AWS region for the STS endpoint |
| `--format` | string | `env` | Output format when no command is given: `env`, `json`, or `credentials-file` |
| `--output` | string | stdout | File path for `--format credentials-file` |
| `--policy` | string | *(none)* | Inline JSON session policy to scope the assumed credentials further |
| `--dry-run` | bool | false | Print what would happen without calling STS |
| `--version` | | | Print version and exit |

---

## HPC use cases

### Slurm prolog: inject credentials before a job starts

Add to `/etc/slurm/prolog.d/10-aws-creds.sh` on your compute nodes:

```bash
#!/bin/bash
# Slurm prolog: assume the per-partition AWS role for the duration of the job.
# SLURM_JOB_PARTITION and SLURM_JOB_WALLTIME are set by Slurm.

ROLE_MAP_s3_readonly="arn:aws:iam::123456789012:role/hpc-s3-readonly"
ROLE_MAP_gpu="arn:aws:iam::123456789012:role/hpc-gpu-s3"

VAR="ROLE_MAP_${SLURM_JOB_PARTITION}"
ROLE_ARN="${!VAR}"

if [[ -z "$ROLE_ARN" ]]; then
    exit 0  # No role mapping for this partition
fi

CREDS_FILE="/run/slurm/job-${SLURM_JOB_ID}-aws-creds"

aws-role-exec \
    --role-arn "$ROLE_ARN" \
    --duration "${SLURM_JOB_WALLTIME:-01:00:00}" \
    --session-name "slurm-job-${SLURM_JOB_ID}" \
    --format credentials-file \
    --output "$CREDS_FILE"

chmod 600 "$CREDS_FILE"
chown "${SLURM_JOB_USER}:" "$CREDS_FILE"

# Make the path available to the job environment
echo "AWS_SHARED_CREDENTIALS_FILE=${CREDS_FILE}" >> /var/lib/slurm/job-${SLURM_JOB_ID}/environment
```

### Open OnDemand adapter injection

In an OOD batch connect or adapter script, inject credentials before spawning the session:

```bash
# In your OOD form submission handler or job template:
exec aws-role-exec \
    --role-arn "${AWS_ROLE_ARN}" \
    --duration "${SESSION_WALLTIME:-01:00:00}" \
    --session-name "ood-${USER}-${JOB_ID}" \
    -- jupyter-lab --no-browser --port="${PORT}"
```

The Jupyter process inherits the credentials directly. No files written to disk.

### Cluster job that reads from S3

Your batch script just calls `aws-role-exec` at the top:

```bash
#!/bin/bash
#SBATCH --job-name=ml-training
#SBATCH --time=04:00:00

exec aws-role-exec \
    --role-arn arn:aws:iam::123456789012:role/ml-training-s3-read \
    --duration 04:00:00 \
    --session-name "slurm-${SLURM_JOB_ID}" \
    -- python3 train.py \
        --data s3://my-bucket/training-data/ \
        --output s3://my-bucket/model-outputs/
```

`exec` ensures the Python process replaces the shell — Slurm's accounting sees the right PID, signals (SIGTERM for time limit) are delivered directly, and the credentials expire automatically when the job ends.

---

## Security notes

- **Credentials never touch disk** unless `--format credentials-file` is explicitly used. Even then, the output file is created with mode `0600`.
- **Credentials live only in the child process environment.** With the exec pattern on Unix, `syscall.Exec` replaces the current process — there is no parent process holding credentials after the `execve` syscall.
- **Credentials expire automatically.** The `--duration` flag maps directly to `DurationSeconds` in the STS `AssumeRole` call. When the session expires, any AWS API calls using these credentials will fail with an auth error.
- **Session name appears in CloudTrail.** The default session name includes username and PID, making it easy to correlate CloudTrail events back to specific jobs or users. Use `--session-name` to set a meaningful value (e.g., `slurm-job-$SLURM_JOB_ID`).
- **Use `--policy` to scope down permissions.** Even if the target role has broad permissions, you can pass an inline session policy to restrict what the child process can do.
- **Your existing AWS credentials are not passed to the child.** `credEnv` removes all `AWS_*` credential variables from the parent environment before injecting the new ones.
