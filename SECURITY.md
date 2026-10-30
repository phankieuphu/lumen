# Security policy

## Supported versions

Only the latest release receives security fixes until v1.0.

## Reporting a vulnerability

Please **do not** open a public issue. Use GitHub's
[private vulnerability reporting](../../security/advisories/new) for this
repository. You will get a reply within 7 days.

## Scope

lumen stores connection secrets in the OS keychain and keeps a local audit log.
Reports about secret leakage, audit-log tampering, read-only bypass, or
execution of SQL the user did not write are especially welcome.
