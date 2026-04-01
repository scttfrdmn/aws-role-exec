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
# Linux amd64
curl -Lo aws-role-exec.tar.gz \
  https://github.com/scttfrdmn/aws-role-exec/releases/latest/download/aws-role-exec_linux_amd64.tar.gz
tar -xzf aws-role-exec.tar.gz
sudo mv aws-role-exec /usr/local/bin/

# Linux arm64 (Graviton)
curl -Lo aws-role-exec.tar.gz \
  https://github.com/scttfrdmn/aws-role-exec/releases/latest/download/aws-role-exec_linux_arm64.tar.gz
tar -xzf aws-role-exec.tar.gz
sudo mv aws-role-exec /usr/local/bin/

# macOS (Apple Silicon)
curl -Lo aws-role-exec.tar.gz \
  https://github.com/scttfrdmn/aws-role-exec/releases/latest/download/aws-role-exec_darwin_arm64.tar.gz
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

# When you are done, unset them:
unset AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN
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

### Dry-run — see what would happen without calling STS
```bash
aws-role-exec \
  --role-arn arn:aws:iam::123456789012:role/my-role \
  --duration 04:00:00 \
  --session-name my-job-42 \
  --dry-run \
  -- python3 train.py
# DRY RUN: would assume arn:aws:iam::123456789012:role/my-role
#   session-name: my-job-42
#   duration:     4h0m0s
#   command:      python3 train.py
```

---

## Flags

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--role-arn` | string | *(required)* | IAM role ARN to assume |
| `--duration` | string | `1h` | Credential lifetime. Accepts Go durations (`1h30m`, `45m`) or HPC walltime format (`HH:MM:SS`). Min: 15m, max: 12h |
| `--session-name` | string | `aws-role-exec-<user>-<hex>` | STS RoleSessionName. Appears in CloudTrail |
| `--region` | string | `AWS_DEFAULT_REGION` or `us-east-1` | AWS region for the STS endpoint |
| `--format` | string | `env` | Output mode when no command given: `env`, `json`, or `credentials-file` |
| `--output` | string | stdout | Write credentials to this file instead of stdout (all formats) |
| `--policy` | string | *(none)* | Inline JSON session policy to scope the assumed credentials further |
| `--sts-timeout` | string | `30s` | Timeout for the STS AssumeRole API call (Go duration, e.g. `10s`, `1m`) |
| `--dry-run` | bool | false | Print what would happen without calling STS |
| `--version` | | | Print version and exit |

---

## Examples

### HPC / Slurm

#### Slurm batch script: S3 data access scoped to job lifetime

The simplest integration. Add `exec aws-role-exec` at the top of your job script. The Python process replaces the shell — Slurm's accounting sees the right PID, SIGTERM for time limits is delivered directly, and credentials expire automatically when the walltime is reached.

```bash
#!/bin/bash
#SBATCH --job-name=ml-training
#SBATCH --time=04:00:00
#SBATCH --partition=gpu

exec aws-role-exec \
    --role-arn arn:aws:iam::123456789012:role/hpc-ml-training \
    --duration 04:00:00 \
    --session-name "slurm-${SLURM_JOB_ID}" \
    -- python3 train.py \
        --data    s3://research-bucket/training-data/ \
        --output  s3://research-bucket/model-outputs/run-${SLURM_JOB_ID}/
```

#### Slurm prolog: per-partition role injection, credentials-file approach

When the job script itself shouldn't know about roles (e.g., users submit jobs without modifying their scripts), inject from the prolog instead. Add to `/etc/slurm/prolog.d/10-aws-creds.sh` on compute nodes:

```bash
#!/bin/bash
# Slurm prolog: assume a per-partition AWS role, write credentials-file, export path.
# SLURM_JOB_PARTITION and SLURM_JOB_WALLTIME are set by Slurm before prolog runs.

declare -A ROLE_MAP=(
    [gpu]="arn:aws:iam::123456789012:role/hpc-gpu-s3-readwrite"
    [highmem]="arn:aws:iam::123456789012:role/hpc-highmem-s3-readonly"
    [standard]="arn:aws:iam::123456789012:role/hpc-standard-s3-readonly"
    [genomics]="arn:aws:iam::123456789012:role/hpc-genomics-s3-omics"
)

ROLE_ARN="${ROLE_MAP[$SLURM_JOB_PARTITION]}"
[[ -z "$ROLE_ARN" ]] && exit 0   # partition has no AWS role

CREDS_FILE="/run/slurm/job-${SLURM_JOB_ID}-aws-creds"

aws-role-exec \
    --role-arn     "$ROLE_ARN" \
    --duration     "${SLURM_JOB_WALLTIME:-01:00:00}" \
    --session-name "slurm-${SLURM_JOB_ID}" \
    --format       credentials-file \
    --output       "$CREDS_FILE"

chmod 600 "$CREDS_FILE"
chown "${SLURM_JOB_USER}:" "$CREDS_FILE"

echo "AWS_SHARED_CREDENTIALS_FILE=${CREDS_FILE}" \
    >> /var/lib/slurm/job-${SLURM_JOB_ID}/environment
```

Pair with an epilog to clean up: `/etc/slurm/epilog.d/10-aws-creds.sh`:

```bash
#!/bin/bash
rm -f "/run/slurm/job-${SLURM_JOB_ID}-aws-creds"
```

#### Slurm array job: each task gets its own session

```bash
#!/bin/bash
#SBATCH --job-name=parallel-inference
#SBATCH --array=0-99
#SBATCH --time=01:00:00

exec aws-role-exec \
    --role-arn     arn:aws:iam::123456789012:role/hpc-inference \
    --duration     01:00:00 \
    --session-name "slurm-${SLURM_JOB_ID}-${SLURM_ARRAY_TASK_ID}" \
    -- python3 run_inference.py --chunk "${SLURM_ARRAY_TASK_ID}"
```

Each array task is a separate CloudTrail entry — you can trace S3 or Bedrock calls back to individual task IDs.

#### MPI job: all ranks share the same credential file

```bash
#!/bin/bash
#SBATCH --job-name=mpi-simulation
#SBATCH --nodes=8
#SBATCH --ntasks-per-node=32
#SBATCH --time=08:00:00

# Write credentials to a shared filesystem all nodes can read
CREDS_FILE="${TMPDIR}/aws-creds-${SLURM_JOB_ID}"

aws-role-exec \
    --role-arn     arn:aws:iam::123456789012:role/hpc-simulation-s3 \
    --duration     08:00:00 \
    --session-name "slurm-${SLURM_JOB_ID}" \
    --format       credentials-file \
    --output       "$CREDS_FILE"

export AWS_SHARED_CREDENTIALS_FILE="$CREDS_FILE"

mpirun -np 256 ./simulation --checkpoint-bucket s3://sim-bucket/checkpoints/
```

#### PBS/Torque prolog

```bash
#!/bin/bash
# /var/spool/torque/mom_priv/prologue

ROLE_ARN="arn:aws:iam::123456789012:role/hpc-pbs-worker"
CREDS_FILE="/tmp/pbs-aws-creds-${PBS_JOBID}"

aws-role-exec \
    --role-arn     "$ROLE_ARN" \
    --duration     "${PBS_WALLTIME:-01:00:00}" \
    --session-name "pbs-${PBS_JOBID}" \
    --format       credentials-file \
    --output       "$CREDS_FILE"

echo "AWS_SHARED_CREDENTIALS_FILE=${CREDS_FILE}" >> "${PBS_ENVIRONMENT}"
```

---

### Amazon Bedrock — AI inference from HPC jobs

Bedrock is a natural fit for HPC workflows: models are invoked on demand without managing GPU instances, and `aws-role-exec` scopes the credentials to exactly what the job needs.

#### Bioinformatics: annotate genomic sequences with a language model

```bash
#!/bin/bash
#SBATCH --job-name=sequence-annotation
#SBATCH --array=0-999
#SBATCH --time=02:00:00

# Each task annotates a chunk of sequences using Claude on Bedrock.
# The role only allows bedrock:InvokeModel on the specific model needed.
exec aws-role-exec \
    --role-arn arn:aws:iam::123456789012:role/hpc-bedrock-claude \
    --duration 02:00:00 \
    --session-name "annot-${SLURM_JOB_ID}-${SLURM_ARRAY_TASK_ID}" \
    --policy '{
      "Version": "2012-10-17",
      "Statement": [{
        "Effect": "Allow",
        "Action": "bedrock:InvokeModel",
        "Resource": "arn:aws:bedrock:us-east-1::foundation-model/anthropic.claude-3-5-haiku-20241022-v1:0"
      }]
    }' \
    -- python3 annotate_chunk.py \
        --chunk "${SLURM_ARRAY_TASK_ID}" \
        --input  s3://genomics-bucket/sequences/ \
        --output s3://genomics-bucket/annotations/
```

#### Climate science: natural language summary of model output

```bash
#!/bin/bash
#SBATCH --job-name=climate-postprocess
#SBATCH --time=00:30:00
#SBATCH --dependency=afterok:${SIMULATION_JOB_ID}

exec aws-role-exec \
    --role-arn arn:aws:iam::123456789012:role/hpc-bedrock-nova \
    --duration 00:30:00 \
    --session-name "climate-summary-${SLURM_JOB_ID}" \
    -- python3 summarize_output.py \
        --model  "amazon.nova-pro-v1:0" \
        --data   /scratch/sim-outputs/run-${SIMULATION_JOB_ID}/ \
        --report s3://climate-bucket/reports/run-${SIMULATION_JOB_ID}.md
```

#### Bedrock Batch Inference: submit a large inference job from inside a Slurm task

Bedrock Batch Inference is the right pattern for >1000 prompts — you submit once and poll for completion, exactly like an HPC job.

```bash
#!/bin/bash
#SBATCH --job-name=batch-inference-submit
#SBATCH --time=00:10:00

exec aws-role-exec \
    --role-arn arn:aws:iam::123456789012:role/hpc-bedrock-batch \
    --duration 00:10:00 \
    --session-name "batch-submit-${SLURM_JOB_ID}" \
    -- python3 submit_batch_inference.py \
        --model        "anthropic.claude-3-haiku-20240307-v1:0" \
        --input-s3     s3://research-bucket/prompts/batch-001.jsonl \
        --output-s3    s3://research-bucket/results/batch-001/ \
        --role-arn     arn:aws:iam::123456789012:role/bedrock-batch-execution
```

#### Scoped-down role: Bedrock-only, no S3 write

```bash
# The --policy flag layers an additional restriction on top of the assumed role.
# Even if the role allows s3:PutObject, this session cannot write to S3.
aws-role-exec \
    --role-arn arn:aws:iam::123456789012:role/hpc-bedrock-full \
    --policy '{
      "Version": "2012-10-17",
      "Statement": [{
        "Effect": "Allow",
        "Action": ["bedrock:InvokeModel", "bedrock:InvokeModelWithResponseStream"],
        "Resource": "*"
      }]
    }' \
    -- python3 interactive_analysis.py
```

---

### Workflow managers (Nextflow, Snakemake, WDL)

#### Nextflow: wrap the entire workflow in a scoped role

```bash
# In your launch script, exec into the assumed role.
# All Nextflow processes inherit the credentials through the process environment.
exec aws-role-exec \
    --role-arn arn:aws:iam::123456789012:role/nextflow-pipeline-role \
    --duration 08:00:00 \
    --session-name "nxf-${USER}-$(date +%s)" \
    -- nextflow run nf-core/rnaseq \
        -profile aws \
        --input  s3://nf-bucket/samplesheet.csv \
        --outdir s3://nf-bucket/results/
```

#### Nextflow: per-process role assumption via beforeScript

```groovy
// nextflow.config — assume a tighter role for each process
process {
    withLabel: 'aws_access' {
        beforeScript = """
            eval \$(aws-role-exec \
                --role-arn arn:aws:iam::123456789012:role/nf-task-role \
                --duration 01:00:00 \
                --session-name "nf-task-\${PROCESS_NAME}" \
                --format env)
        """
    }
}
```

#### Snakemake: per-rule credential injection

```python
# Snakefile
rule download_from_s3:
    output: "data/{sample}.fastq.gz"
    shell:
        """
        aws-role-exec \
            --role-arn arn:aws:iam::123456789012:role/snakemake-s3-reader \
            --duration 00:30:00 \
            --session-name "snakemake-download-{wildcards.sample}" \
            -- aws s3 cp s3://raw-data/{wildcards.sample}.fastq.gz {output}
        """

rule run_analysis:
    input:  "data/{sample}.fastq.gz"
    output: "results/{sample}.tsv"
    shell:
        """
        exec aws-role-exec \
            --role-arn arn:aws:iam::123456789012:role/snakemake-compute \
            --duration 04:00:00 \
            --session-name "snakemake-{wildcards.sample}" \
            -- python3 analyze.py --input {input} --output {output}
        """
```

---

### Open OnDemand

#### Batch Connect: inject credentials before a JupyterLab session

```bash
# In your OOD Batch Connect template (before.sh or the job template itself):
exec aws-role-exec \
    --role-arn  "${AWS_ROLE_ARN}" \
    --duration  "${SESSION_WALLTIME:-02:00:00}" \
    --session-name "ood-${USER}-${JOB_ID}" \
    -- jupyter-lab \
        --no-browser \
        --port="${PORT}" \
        --ip=0.0.0.0
```

The JupyterLab server process inherits credentials. Every notebook cell that calls boto3 or the AWS CLI uses the scoped role — not the node's instance profile.

#### OOD adapter script: inject credentials for a compute backend call

```bash
# In an OOD adapter script that submits to AWS Batch or Bedrock:
CREDS_JSON=$(aws-role-exec \
    --role-arn arn:aws:iam::123456789012:role/ood-adapter-role \
    --duration "${WALLTIME:-01:00:00}" \
    --session-name "ood-adapter-${USER}" \
    --format json)

export AWS_ACCESS_KEY_ID=$(echo "$CREDS_JSON"     | jq -r .AccessKeyId)
export AWS_SECRET_ACCESS_KEY=$(echo "$CREDS_JSON" | jq -r .SecretAccessKey)
export AWS_SESSION_TOKEN=$(echo "$CREDS_JSON"     | jq -r .SessionToken)

# Now invoke the adapter binary with scoped credentials
ood-aws-batch-adapter submit <<< "${JOB_SPEC_JSON}"
```

---

### CI/CD pipelines

Long-lived AWS credentials in CI secrets are a supply-chain risk. Assume a short-lived role with exactly the permissions each stage needs.

#### GitHub Actions: assume role per job step

```yaml
# .github/workflows/deploy.yml
jobs:
  deploy:
    runs-on: ubuntu-latest
    permissions:
      id-token: write   # needed for OIDC
      contents: read
    steps:
      - uses: actions/checkout@v4

      - name: Configure base AWS credentials (OIDC)
        uses: aws-actions/configure-aws-credentials@v4
        with:
          role-to-assume: arn:aws:iam::123456789012:role/github-oidc-base
          aws-region: us-east-1

      - name: Deploy with scoped credentials
        run: |
          exec aws-role-exec \
              --role-arn     arn:aws:iam::123456789012:role/deploy-production \
              --duration     15m \
              --session-name "github-${GITHUB_RUN_ID}-deploy" \
              --policy '{
                "Version": "2012-10-17",
                "Statement": [{"Effect":"Allow","Action":"s3:PutObject",
                  "Resource":"arn:aws:s3:::my-deploy-bucket/*"}]
              }' \
              -- aws s3 sync dist/ s3://my-deploy-bucket/
```

#### Jenkins: per-stage role assumption

```groovy
// Jenkinsfile
pipeline {
    agent any
    stages {
        stage('Run Tests') {
            steps {
                sh '''
                    exec aws-role-exec \
                        --role-arn arn:aws:iam::123456789012:role/ci-test-readonly \
                        --duration 30m \
                        --session-name "jenkins-${BUILD_NUMBER}-test" \
                        -- ./run-tests.sh
                '''
            }
        }
        stage('Deploy') {
            when { branch 'main' }
            steps {
                sh '''
                    exec aws-role-exec \
                        --role-arn arn:aws:iam::123456789012:role/ci-deploy \
                        --duration 15m \
                        --session-name "jenkins-${BUILD_NUMBER}-deploy" \
                        -- ./deploy.sh
                '''
            }
        }
    }
}
```

#### GitLab CI: job-scoped credentials

```yaml
# .gitlab-ci.yml
test:
  script:
    - |
      exec aws-role-exec \
          --role-arn arn:aws:iam::123456789012:role/gitlab-ci-test \
          --duration 30m \
          --session-name "gitlab-${CI_JOB_ID}" \
          -- pytest tests/

deploy:
  script:
    - |
      exec aws-role-exec \
          --role-arn arn:aws:iam::123456789012:role/gitlab-ci-deploy \
          --duration 15m \
          --session-name "gitlab-${CI_JOB_ID}-deploy" \
          -- ./scripts/deploy.sh
  only:
    - main
```

---

### Containers

#### Docker ENTRYPOINT: inject credentials at container start

```dockerfile
# Dockerfile
FROM python:3.12-slim
RUN pip install boto3
COPY --from=ghcr.io/scttfrdmn/aws-role-exec:latest /aws-role-exec /usr/local/bin/
COPY entrypoint.sh /entrypoint.sh
ENTRYPOINT ["/entrypoint.sh"]
```

```bash
# entrypoint.sh
#!/bin/bash
exec aws-role-exec \
    --role-arn     "${AWS_ROLE_ARN}" \
    --duration     "${AWS_SESSION_DURATION:-1h}" \
    --session-name "${AWS_SESSION_NAME:-container-$(hostname)}" \
    -- "$@"
```

```bash
docker run \
    -e AWS_ROLE_ARN=arn:aws:iam::123456789012:role/my-container-role \
    -e AWS_SESSION_DURATION=2h \
    -e AWS_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID}" \
    -e AWS_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY}" \
    my-image python3 process.py
```

#### Kubernetes init container: write credentials file before main container

```yaml
# pod.yaml
initContainers:
  - name: aws-creds
    image: ghcr.io/scttfrdmn/aws-role-exec:latest
    command:
      - aws-role-exec
      - --role-arn
      - arn:aws:iam::123456789012:role/k8s-job-role
      - --duration
      - 4h
      - --format
      - credentials-file
      - --output
      - /aws-creds/credentials
    volumeMounts:
      - name: aws-creds
        mountPath: /aws-creds
containers:
  - name: worker
    image: my-worker:latest
    env:
      - name: AWS_SHARED_CREDENTIALS_FILE
        value: /aws-creds/credentials
    volumeMounts:
      - name: aws-creds
        mountPath: /aws-creds
        readOnly: true
volumes:
  - name: aws-creds
    emptyDir:
      medium: Memory   # credentials never hit disk
```

#### Docker Compose: scoped credentials per service

```yaml
# docker-compose.yml
services:
  processor:
    image: my-processor
    environment:
      AWS_ROLE_ARN: arn:aws:iam::123456789012:role/processor-role
    entrypoint:
      - aws-role-exec
      - --role-arn
      - ${AWS_ROLE_ARN}
      - --duration
      - 2h
      - --
```

---

### Interactive shell sessions

#### Temporarily assume a role in your current terminal

```bash
# Assume and export into current shell (eval pattern)
eval $(aws-role-exec \
    --role-arn arn:aws:iam::123456789012:role/admin-readonly \
    --duration 1h \
    --format env)

# Check who you are now
aws sts get-caller-identity

# When done, clear the credentials
unset AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN
```

#### Open a subshell with a different role

```bash
aws-role-exec \
    --role-arn arn:aws:iam::123456789012:role/prod-admin \
    --duration 30m \
    --session-name "prod-investigation-${USER}" \
    -- bash

# You are now in a subshell with prod-admin credentials.
# Ctrl-D or 'exit' to return to your original credentials.
```

#### Cross-account access

```bash
# Access a resource in another AWS account
aws-role-exec \
    --role-arn arn:aws:iam::999888777666:role/cross-account-data-reader \
    --duration 1h \
    --session-name "cross-account-${USER}" \
    -- aws s3 ls s3://partner-data-bucket/
```

---

### Notebooks and interactive tools

#### JupyterLab: run a single cell with elevated permissions

In a cell before the sensitive section:
```python
import subprocess, os, json

result = subprocess.run(
    ["aws-role-exec",
     "--role-arn", "arn:aws:iam::123456789012:role/ml-training-role",
     "--duration", "2h",
     "--format", "json"],
    capture_output=True, text=True, check=True
)
creds = json.loads(result.stdout)

os.environ["AWS_ACCESS_KEY_ID"]     = creds["AccessKeyId"]
os.environ["AWS_SECRET_ACCESS_KEY"] = creds["SecretAccessKey"]
os.environ["AWS_SESSION_TOKEN"]     = creds["SessionToken"]

# boto3 calls from here use the scoped role
import boto3
s3 = boto3.client("s3")
```

#### JupyterLab: launch the whole server under a role

```bash
# In your ~/.bashrc or lab launch script:
aws-role-exec \
    --role-arn arn:aws:iam::123456789012:role/ml-notebook-role \
    --duration 8h \
    --session-name "jupyter-${USER}" \
    -- jupyter lab --no-browser
```

---

### Tools that don't read environment variables

Some tools only read `~/.aws/credentials` or a named profile and ignore `AWS_*` env vars. Use the credentials-file pattern:

```bash
CREDS_FILE=$(mktemp /tmp/aws-creds-XXXXX)

aws-role-exec \
    --role-arn arn:aws:iam::123456789012:role/my-role \
    --duration 1h \
    --format credentials-file \
    --output "$CREDS_FILE"

# Terraform reads credentials from file
AWS_SHARED_CREDENTIALS_FILE="$CREDS_FILE" terraform apply

# Pulumi
AWS_SHARED_CREDENTIALS_FILE="$CREDS_FILE" pulumi up

# Ansible
AWS_SHARED_CREDENTIALS_FILE="$CREDS_FILE" ansible-playbook playbook.yml

# Clean up when done
rm -f "$CREDS_FILE"
```

---

### Scoping down permissions with --policy

Even if the assumed role has broad permissions, you can attach a session policy to restrict what the child process can do. The effective permissions are the intersection of the role's policies and the session policy.

#### Allow only specific S3 prefix

```bash
aws-role-exec \
    --role-arn arn:aws:iam::123456789012:role/full-data-lake-role \
    --policy '{
      "Version": "2012-10-17",
      "Statement": [{
        "Effect": "Allow",
        "Action": ["s3:GetObject", "s3:ListBucket"],
        "Resource": [
          "arn:aws:s3:::data-lake",
          "arn:aws:s3:::data-lake/project-x/*"
        ]
      }]
    }' \
    -- python3 etl.py --project project-x
```

#### Allow only specific DynamoDB table

```bash
aws-role-exec \
    --role-arn arn:aws:iam::123456789012:role/db-admin \
    --policy '{
      "Version": "2012-10-17",
      "Statement": [{
        "Effect": "Allow",
        "Action": ["dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:UpdateItem"],
        "Resource": "arn:aws:dynamodb:us-east-1:123456789012:table/my-specific-table"
      }]
    }' \
    -- ./data-migrator
```

#### Read-only session from a read-write role

```bash
aws-role-exec \
    --role-arn arn:aws:iam::123456789012:role/rw-role \
    --policy '{
      "Version": "2012-10-17",
      "Statement": [{
        "Effect": "Allow",
        "Action": ["s3:GetObject", "s3:ListBucket", "s3:HeadObject"],
        "Resource": "*"
      }]
    }' \
    -- aws s3 sync s3://my-bucket/ ./local-backup/
```

---

## Problems this solves (that you might not have known you had)

### 1. Credentials that outlive their job

When you set `AWS_ACCESS_KEY_ID` in a shell and forget to unset it, those credentials persist indefinitely in that shell session — and in any child process that inherits the environment. A job that ran four hours ago is still carrying credentials that are theoretically valid for months.

`aws-role-exec` uses STS temporary credentials with a hard expiry. The credentials stop working at the end of the session duration, regardless of whether anyone cleaned up.

### 2. The same credentials for everything

A cluster node typically has one instance profile or one set of `AWS_*` environment variables. Every job that runs on that node — simulation, ML training, data download, billing query — uses the same identity with the same permissions.

With `aws-role-exec`, each job assumes a purpose-built role. The genomics pipeline gets read access to the genomics S3 bucket. The ML training job gets read access to training data and write access to the model output bucket. Neither can touch the other's data, even if they run on the same node.

### 3. Credentials in container images

Baking credentials into a Docker image is a common accident. The image gets pushed to a registry, the credentials are in the layer history, and now anyone who can pull the image has your credentials.

With `aws-role-exec` as the `ENTRYPOINT`, the image contains no credentials at all. They are injected at runtime from an external trust relationship.

### 4. Multi-tenant HPC security

On a shared cluster, if node-level credentials leak into the job environment through a misconfigured prolog or a world-readable file, any user on that node can exfiltrate them. Credentials that are scoped to a single job, with a session name tied to the job ID, limit the blast radius: leaked credentials expire when the job ends, and CloudTrail shows exactly which job they belonged to.

### 5. CI/CD credential sprawl

A CI pipeline that has a single AWS secret shared across dozens of jobs — build, test, integration, deploy, rollback — is operating at the union of all those permissions all the time. If the secret leaks, an attacker can do anything any job can do.

With `aws-role-exec`, each pipeline stage assumes the minimum role it needs, for exactly as long as it needs it. The base CI role only needs `sts:AssumeRole` on the specific child roles.

### 6. Tools that don't respect AWS credential chains

Most tools support `AWS_*` environment variables, but some have their own credential handling that only reads `~/.aws/credentials`. The credentials-file output mode produces a file in the format those tools expect, written to a path you control, cleaned up when you're done.

### 7. Ephemeral compute that needs AWS access

Spot instances, GitHub Actions runners, Fargate tasks — these environments come and go and shouldn't carry long-lived credentials. With `aws-role-exec`, the instance or runner uses an instance profile or OIDC token to assume a job-specific role, and the child process credentials are short-lived by construction.

### 8. Auditing: knowing *which job* made *which API call*

CloudTrail logs every API call, but if hundreds of jobs share one identity, the logs are useless for attribution. When every job sets `--session-name "slurm-${SLURM_JOB_ID}"`, CloudTrail entries carry the job ID. You can join CloudTrail logs with your cluster accounting logs to see exactly which job read which S3 object or invoked which Bedrock model.

### 9. Signal-transparent credential injection

A common pattern is to write a wrapper shell script that sets credentials and then calls the real program. The shell process sits between the job scheduler and the program — SIGTERM for walltime limits goes to the shell, which may or may not propagate it correctly. With `exec aws-role-exec`, the shell is replaced by the target program. Signals arrive directly. Exit codes propagate. The scheduler sees the right PID.

---

## Security notes

- **Credentials never touch disk** unless `--format credentials-file` is explicitly used. Even then, the output file is created with mode `0600`.
- **Credentials live only in the child process environment.** With the exec pattern on Unix, `syscall.Exec` replaces the current process — there is no parent process holding credentials after the `execve` syscall.
- **Credentials expire automatically.** The `--duration` flag maps directly to `DurationSeconds` in the STS `AssumeRole` call. When the session expires, any AWS API calls using these credentials will fail with an auth error.
- **Session name appears in CloudTrail.** The default session name includes username and PID, making it easy to correlate CloudTrail events back to specific jobs or users. Use `--session-name` to set a meaningful value (e.g., `slurm-job-$SLURM_JOB_ID`).
- **Use `--policy` to scope down permissions.** Even if the target role has broad permissions, you can pass an inline session policy to restrict what the child process can do. Effective permissions are the intersection of the role's policies and the session policy.
- **Your existing AWS credentials are not passed to the child.** `credEnv` strips all `AWS_*` credential variables from the parent environment before injecting the new ones.
- **Trust the role, not the node.** The cluster node (or CI runner, or container host) needs only `sts:AssumeRole` on the target roles. It does not need — and should not have — the permissions those roles carry.

---

## Contributing

Issues and pull requests welcome. See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

Apache 2.0
