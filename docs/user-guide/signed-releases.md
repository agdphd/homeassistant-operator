# Signed Releases

Starting with **v1.2.0**, every release artifact — the container image, the Helm
chart OCI artifact, and a `checksums.txt` bundle attached to the GitHub Release —
is cryptographically signed. Anyone can verify authenticity without contacting the
maintainer, and Kubernetes cluster operators can enforce it automatically with
Kyverno.

!!! info "Releases before v1.2.0 are not signed"
    Signing starts at v1.2.0. There is no retroactive signing of earlier releases —
    do not assume coverage for tags published before it.

## What is signed

| Artifact | What it is |
|---|---|
| Container image | The multi-architecture (amd64/arm64) manifest list pushed to `ghcr.io/przemekhys/homeassistant-operator` |
| Helm chart | The chart packaged and pushed as an OCI artifact to `oci://ghcr.io/przemekhys/charts/homeassistant-operator` |
| `checksums.txt` | A text file listing the image and chart digests for the release, attached to the GitHub Release along with its own signature |

All three are signed **keyless**, using [Sigstore](https://www.sigstore.dev/)/`cosign`,
bound to this repository's own GitHub Actions release workflow identity. There is
no long-lived signing key anywhere — the maintainer never generates, stores, or
rotates one. Each signature is backed by a short-lived certificate from Sigstore's
Fulcio and a public transparency-log entry in Rekor, which is what the verification
commands below check against.

## Verify the container image

```bash
IMAGE=ghcr.io/przemekhys/homeassistant-operator
DIGEST=$(crane digest "$IMAGE:v1.2.0")   # or read it from checksums.txt

cosign verify \
  --certificate-identity-regexp \
    'https://github.com/przemekhys/homeassistant-operator/\.github/workflows/release\.yml@refs/tags/.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  "$IMAGE@$DIGEST"
```

To verify the whole `checksums.txt` bundle instead (covers the image and chart
digests transitively in one check):

```bash
gh release download v1.2.0 -p 'checksums.txt*'
cosign verify-blob \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp \
    'https://github.com/przemekhys/homeassistant-operator/\.github/workflows/release\.yml@refs/tags/.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt
```

!!! tip "Local reproduction"
    [`hack/verify-signatures.sh`](https://github.com/przemekhys/homeassistant-operator/blob/main/hack/verify-signatures.sh)
    runs these same checks (image, chart, checksums bundle) in one command:
    `hack/verify-signatures.sh v1.2.0`.

## Verify with Kyverno

Cluster operators can enforce this automatically at admission time with
[Kyverno](https://kyverno.io/), so an unsigned or tampered image is rejected before
it ever runs.

!!! note "Minimum Kyverno version"
    This sample was validated against the classic `kyverno.io/v1 ClusterPolicy` API,
    supported since **Kyverno 1.9**. It intentionally does not use the newer
    CEL-based `ImageValidatingPolicy` (Kyverno 1.14+) so it works on a broader range
    of clusters.

The full sample lives at
[`hack/kyverno/verify-homeassistant-operator-image.yaml`](https://github.com/przemekhys/homeassistant-operator/blob/main/hack/kyverno/verify-homeassistant-operator-image.yaml):

```yaml
apiVersion: kyverno.io/v1
kind: ClusterPolicy
metadata:
  name: verify-homeassistant-operator-image
  annotations:
    policies.kyverno.io/title: Verify homeassistant-operator image signature
    policies.kyverno.io/category: Software Supply Chain
    policies.kyverno.io/severity: high
    policies.kyverno.io/description: >-
      Requires that any ghcr.io/przemekhys/homeassistant-operator image was signed
      by the project's own GitHub Actions release workflow (keyless Sigstore
      signing), rejecting unsigned or tampered images at admission time.
spec:
  validationFailureAction: Enforce
  webhookTimeoutSeconds: 30
  rules:
    - name: verify-signature
      match:
        any:
          - resources:
              kinds:
                - Pod
      verifyImages:
        - imageReferences:
            - "ghcr.io/przemekhys/homeassistant-operator*"
          attestors:
            - count: 1
              entries:
                - keyless:
                    issuer: "https://token.actions.githubusercontent.com"
                    subject: "https://github.com/przemekhys/homeassistant-operator/.github/workflows/release.yml@refs/tags/*"
                    rekor:
                      url: https://rekor.sigstore.dev
```

Apply it, then try both a genuine and a tampered image:

```bash
kubectl apply -f hack/kyverno/verify-homeassistant-operator-image.yaml

# Genuine image: admitted.
kubectl run ha-genuine --image=ghcr.io/przemekhys/homeassistant-operator:v1.2.0 --restart=Never

# Tampered/re-tagged image: rejected by the admission webhook.
kubectl run ha-tampered --image=<attacker-controlled-retag> --restart=Never
```

!!! warning "Roll out with Audit first"
    The sample defaults to `validationFailureAction: Enforce`. For a first adoption
    in an existing cluster, consider switching it to `Audit` (log-only) to confirm
    it matches as expected before enforcing rejections.

!!! warning "Keep this policy in sync if the signing identity changes"
    The `issuer`/`subject` values above are pinned to this repository's release
    workflow file path and tag-ref pattern. If that workflow is ever renamed or
    moved, both this doc page and the policy file must be updated together in the
    same change — otherwise the policy would silently stop matching new releases
    instead of failing loudly.

## Verify the Helm chart

```bash
cosign verify \
  --certificate-identity-regexp \
    'https://github.com/przemekhys/homeassistant-operator/\.github/workflows/release\.yml@refs/tags/.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  ghcr.io/przemekhys/charts/homeassistant-operator@<chart-digest>
```

!!! note "No `oci://` prefix here"
    That scheme is Helm-specific syntax for `helm install`/`push`. `cosign`
    expects a bare `registry/repo@digest` — passing `oci://...` makes it try to
    resolve `oci` itself as a registry hostname and fail.

This check is independent of Kyverno and of any cluster — it works anywhere
`cosign` can reach the OCI registry, for example as a preflight step in a GitOps
pipeline before `helm install`/`upgrade` ever runs.

## Scope and limitations

!!! warning "Kyverno enforcement covers the container image only"
    Kyverno's `verifyImages` rule operates on images referenced by Pod specs at
    admission time — it has no hook into `helm install`/`upgrade`, so it cannot and
    does not verify the Helm chart OCI artifact. Verify the chart manually with the
    command above.

!!! info "The default installation is unaffected"
    Whether or not Kyverno is present, or whether you verify anything at all,
    `helm install`/`upgrade` of this chart works exactly as before this feature
    shipped. Signature verification is entirely opt-in — nothing here is required
    for the operator to run.
