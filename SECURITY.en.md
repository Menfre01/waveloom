# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability, please **do not** file a public Issue.

Send an email to menfre@proton.me, preferably PGP-encrypted.

We will acknowledge receipt within 48 hours and publicly credit you after the fix (let us know if you prefer to remain anonymous).

## Supported Versions

| Version | Support Status |
|---------|----------------|
| v0.1.x (alpha) | ✅ Security fixes |

## Security Checklist

- Waveloom never silently executes commands or modifies files — all write operations require user confirmation by default
- `--bypass-permissions` should only be used in trusted CI environments
- Do not commit the API Key in `settings.json` to public repositories
- Shell commands run as the current user; ensure build tools like `make`, `go`, `npm` come from trusted sources
