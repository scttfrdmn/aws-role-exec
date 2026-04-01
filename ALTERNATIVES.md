# Alternatives to aws-role-exec

Several tools solve the "assume an IAM role and run something" problem. This document explains where each fits and where `aws-role-exec` differs.

---

## Direct alternatives (exec pattern)

### [aws-vault](https://github.com/99designs/aws-vault)

The most widely used tool in this space. Stores base credentials encrypted in the OS keychain (macOS Keychain, Windows Credential Manager, Secret Service on Linux) and can exec commands under an assumed role.

**Use aws-vault if:**
- You work interactively on a laptop and want encrypted credential storage
- You need MFA support
- You need to generate browser login URLs for the AWS console

**aws-vault is a poor fit if:**
- You're running headless (HPC compute nodes, CI runners, containers) — no keychain available
- You're in an environment that already provides credentials via instance profile or OIDC and just needs to assume a child role
- You need true `execve`-based process replacement for signal transparency

---

### [granted](https://github.com/common-fate/granted)

A modern credential management tool from Common Fate. Has an exec mode, integrates with AWS SSO/Identity Center, and supports browser-based SSO flows.

**Use granted if:**
- Your organisation uses AWS SSO / Identity Center
- You want a polished interactive CLI experience
- You need multi-account, multi-role switching on a developer workstation

**granted is a poor fit if:**
- You're in a headless or automated environment (requires browser for SSO)
- You need minimal dependencies in a container or HPC prolog

---

### [aws-runas](https://github.com/mmmorris1975/aws-runas)

Close in purpose to `aws-role-exec` — wraps a command with assumed role credentials. Reads configuration from `~/.aws/config` profiles and supports MFA and SAML.

**Use aws-runas if:**
- You already manage roles via `~/.aws/config` named profiles
- You need SAML or MFA support alongside exec

**aws-runas is a poor fit if:**
- You want a single-binary with no config file dependency
- You need `syscall.Exec`-based process replacement (aws-runas uses `os/exec`, not `execve`)

---

## Eval-pattern tools

### [awsume](https://awsu.me)

A Python-based tool, very popular in enterprise environments. Primarily designed for the eval pattern — `awsume role-name` modifies your current shell session. Has a plugin ecosystem.

**Use awsume if:**
- You want interactive shell-session role switching
- You need the plugin ecosystem (custom credential sources, etc.)
- Your team is already standardised on it

**awsume is a poor fit if:**
- You're scripting — the eval pattern doesn't work inside non-interactive scripts without extra wiring
- You need exec-pattern process replacement
- You're in a container or HPC environment without Python

---

## IdP-specific tools

These solve a different entry point — getting base credentials from an identity provider — rather than assuming a child role from existing credentials. They're complementary, not competing.

| Tool | IdP |
|------|-----|
| [saml2aws](https://github.com/Versent/saml2aws) | SAML (Okta, ADFS, Azure AD, …) |
| [gimme-aws-creds](https://github.com/Nike-Inc/gimme-aws-creds) | Okta |
| [aws-sso-util](https://github.com/benkehoe/aws-sso-util) | AWS SSO / Identity Center |

A common pattern: use `saml2aws` or `aws-sso-util` to get base credentials, then use `aws-role-exec` to assume a job-specific child role from those base credentials.

---

## The manual approach

```bash
OUT=$(aws sts assume-role \
  --role-arn arn:aws:iam::123456789012:role/my-role \
  --role-session-name my-session)

export AWS_ACCESS_KEY_ID=$(echo $OUT | jq -r .Credentials.AccessKeyId)
export AWS_SECRET_ACCESS_KEY=$(echo $OUT | jq -r .Credentials.SecretAccessKey)
export AWS_SESSION_TOKEN=$(echo $OUT | jq -r .Credentials.SessionToken)
```

This works, but:
- Credentials persist in the shell until you explicitly unset them
- Requires `jq`
- No `--policy` scoping
- No signal-transparent exec
- Error-prone to paste and forget

---

## Why aws-role-exec exists

The gap none of the above fills cleanly:

- **No credential storage required.** The caller already has credentials (instance profile, OIDC token, env vars). `aws-role-exec` only needs to assume one child role from whatever is already in the environment.
- **Single static binary.** No Python, no keychain daemon, no browser. Installs in a Slurm prolog, a `Dockerfile`, or a CI runner with one `curl`.
- **True process replacement.** On Unix, `syscall.Exec` replaces the current process with the target command. Signals (SIGTERM for walltime limits, SIGINT from Ctrl-C) arrive directly at the target. Exit codes propagate naturally. The job scheduler sees the right PID.
- **HPC walltime duration format.** `--duration 04:00:00` accepts HH:MM:SS so it can be set directly from `$SLURM_JOB_WALLTIME` without reformatting.
- **Inline session policy.** `--policy` lets you scope credentials down to exactly what a specific invocation needs, even if the assumed role has broader permissions.
