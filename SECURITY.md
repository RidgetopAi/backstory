# Security

## Reporting a vulnerability

Please report it privately. Do not open a public issue.

Use GitHub's private vulnerability reporting: on the
[RidgetopAi/backstory](https://github.com/RidgetopAi/backstory) page open the
**Security** tab and choose **Report a vulnerability**. That creates a private
security advisory that only you and the maintainers can see.

Include what you found, the version (`backstory version`), and the steps to
reproduce it. We will acknowledge the report, work on a fix in the private
advisory, and credit you when the fix is released unless you prefer not to be
named.

## Supported versions

Fixes go into the latest release and into `main`. Older releases do not get
backports; update to the latest tag (see [Updating](README.md#updating)).

## Verifying a release

Release assets ship with a `SHA256SUMS` file and a build-provenance
attestation. To check them, follow
[Verify a release](RELEASING.md#verify-a-release).

## What Backstory touches

For every file Backstory writes or reads, and the promise that it makes no
network connections of its own, see
[docs/WHAT-BACKSTORY-TOUCHES.md](docs/WHAT-BACKSTORY-TOUCHES.md). The
no-network part is enforced by a test (`ops/nonetwork_test.go`) that runs in
`make check`.
