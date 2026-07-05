# Ecosystem Guides

Practical guides for integrating the Home Assistant Operator with other tools in the Kubernetes ecosystem — GitOps, monitoring, secret management.

!!! note "Not part of the supported API"
    These guides show one way to integrate the operator with other tools in the Kubernetes ecosystem. Unlike the rest of this documentation, they are **not** part of the operator's supported API — they reflect the state at the time of writing and may need adjusting for a different tool version or cluster setup.

## Available guides

- **[Flux CD](flux.md)** — deploying the operator and its custom resources with Flux, and securely auto-updating the Home Assistant image with Flux Image Automation.
- **[External Secret Management](secrets-management.md)** — sourcing `HomeAssistantSecrets` from External Secrets Operator, Sealed Secrets, or Vault instead of `kubectl create secret`.
