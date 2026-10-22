# Security Policy

FeatherDesk is maintained by [RadioBros](https://radiobros.com/).

## Supported versions

FeatherDesk ships one release line. Security fixes go into the latest release only. Upgrade to the newest `ghcr.io/radiobros/featherdesk` tag to receive them.

## Reporting a vulnerability

**Do not open a public issue for a security problem.**

Report it privately through GitHub's private vulnerability reporting: open the repository's **Security** tab and choose **Report a vulnerability**. Only the maintainers can see the report.

Please include:

- the affected version (`featherdesk version`) and the database engine (SQLite or Postgres);
- steps to reproduce, or a proof of concept;
- the impact you expect, for example data exposure across organizations, privilege escalation between support tiers, or authentication bypass.

What happens next:

- We acknowledge the report within **3 working days**.
- We send an assessment and a planned fix date within **10 working days**.
- We credit you in the release notes, unless you ask us not to.
- Please give us **90 days** to ship a fix before you disclose anything publicly.

## Scope

In scope:

- the `featherdesk` binary and container image;
- the deployment files under `deploy/`.

Out of scope:

- the dev-only credentials in `deploy/.env.dev`, `deploy/compose.dev.yml`, `deploy/compose.test.yml` and `deploy/authentik/featherdesk-dev-users.blueprint.yaml`. They are public by design.
- findings that require a misconfiguration the docs warn against, for example `FD_DEV=true`, `FD_INSECURE_COOKIES=true`, `FD_TRUSTED_PROXIES=0.0.0.0/0` or more than one replica.

## Hardening defaults

The image and the shipped compose file set these by default:

- distroless base with no shell;
- non-root user;
- read-only root filesystem;
- all Linux capabilities dropped and `no-new-privileges` set;
- the port bound to localhost, so it sits behind your TLS proxy.

Every build runs these scans:

- Trivy (fixable HIGH and CRITICAL findings fail the build);
- gitleaks;
- a dependency gate.
