# Security policy

## Supported versions

Nostekon is pre-alpha. Only the latest commit on `main` receives fixes.

## Reporting a vulnerability

Please do not open a public issue. Report vulnerabilities privately through
[GitHub private vulnerability reporting](https://github.com/jagarkarlo/checkride/security/advisories/new).

Include the affected version or commit, the steps to reproduce, and the impact
you observed. You can expect an acknowledgement within seven days.

## Operating Nostekon safely

- Drills need broad permissions on the clusters they touch. Use dedicated lab
  or restore clusters and short-lived credentials.
- Drill specs must not contain secrets. Reference Kubernetes Secrets instead.
- The acknowledged-write ledger stores write identifiers and timestamps only,
  never row contents.
