# Security Policy

## Reporting a Vulnerability

Open a [GitHub Security Advisory](https://github.com/przemekhys/homeassistant-operator/security/advisories/new) — do not use public issues for security reports.

Dependency vulnerabilities are the most likely risk and are tracked automatically via [govulncheck](.github/workflows/security.yml) and [Renovate](renovate.json).

## Supply Chain: Signed Releases

Every release artifact (container image, Helm chart, and a `checksums.txt` bundle
attached to the GitHub Release) is signed keylessly via Sigstore/cosign, bound to
this repository's own release workflow identity — no long-lived signing key
exists. See the [Signed Releases guide](https://przemekhys.github.io/homeassistant-operator/user-guide/signed-releases/)
for verification commands and a sample Kyverno policy for in-cluster enforcement.
