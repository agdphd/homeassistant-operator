# TLS with cert-manager

The operator integrates with [cert-manager](https://cert-manager.io/) to provision
TLS certificates for two independent use cases:

- **Ingress / API Gateway** — the operator manages the edge routing and its certificate.
- **Webhook** — the operator's validating admission webhook serves over TLS.

TLS is always terminated **at the edge** (an Ingress controller or an API Gateway).
The Home Assistant pod itself always speaks plain HTTP inside the cluster.

!!! info "cert-manager is an optional, external dependency"
    Neither the operator nor its Helm chart ever installs cert-manager. You install
    it (and provide an `Issuer`/`ClusterIssuer`) yourself. If cert-manager is **not**
    present, Ingress / API Gateway reconciliation degrades gracefully: the
    corresponding mode simply stays inactive and the resource reports a status
    condition — nothing fails or loops. A cert-manager installed *after* the
    operator is picked up automatically.

    This graceful degradation does **not** cover the webhook's cert-manager
    override (`--set webhook.certManager.enabled=true`): that path renders an
    `Issuer`/`Certificate` directly via Helm, which requires the cert-manager CRDs
    to exist at install time. Only enable it when cert-manager is already installed.

!!! warning "Native TLS has been removed"
    Earlier versions offered an experimental `spec.alpha.tls` mode where Home
    Assistant terminated HTTPS itself on port `8123`. It has been removed — see
    [Migrating from native TLS](#migrating-from-native-tls) below.

## Prerequisites

- cert-manager installed on the cluster.
- A ready `Issuer` or `ClusterIssuer`. The operator only **references** an issuer;
  it never creates application issuers.

```yaml
# Example: a self-signed ClusterIssuer for testing
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: ca-issuer
spec:
  selfSigned: {}
```

## The `issuerRef`

Every TLS mode references an issuer the same way. `kind` defaults to `Issuer`,
`group` to `cert-manager.io`.

```yaml
issuerRef:
  name: ca-issuer
  kind: ClusterIssuer   # or Issuer (namespaced)
```

!!! tip "Bring your own certificate"
    Each mode also accepts a `secretName` pointing at a TLS Secret you manage
    yourself. When set, it **takes precedence over `issuerRef`** and the operator
    does not create a cert-manager `Certificate`.

## Ingress / API Gateway exposure

The operator manages the edge routing resources **and** their certificate.

### Ingress

Enable `spec.ingress` and add an `issuerRef` under `tls`. The operator creates the
`Ingress` and a `Certificate` whose Secret backs the Ingress TLS.

```yaml
spec:
  ingress:
    enabled: true
    host: ha.example.com
    ingressClassName: traefik
    tls:
      enabled: true
      issuerRef:
        name: ca-issuer
        kind: ClusterIssuer
```

### Gateway API

`spec.gateway` (a stable opt-in) makes the operator manage a `HTTPRoute` that routes
to Home Assistant, and — when `manageGateway: true` — a `Gateway` with an HTTPS
listener. Attach the route to an existing `Gateway` via `parentRef`, or let the
operator create one.

```yaml
spec:
  gateway:
    enabled: true
    host: ha.example.com
    issuerRef:
      name: ca-issuer
      kind: ClusterIssuer
    parentRef:                 # attach to an existing Gateway listener
      name: traefik-gateway
      namespace: gateway
      sectionName: https
    # manageGateway: true      # ...or let the operator create the Gateway
```

!!! note "What the operator does not manage"
    The operator does **not** manage the `GatewayClass` or the Ingress/Gateway
    controller itself — those are provided by your platform. It only manages the
    routing resources and the certificate.

## Migrating from native TLS

`spec.alpha.tls` (native HTTPS inside the Home Assistant pod) has been removed.
It was an experimental `spec.alpha` feature; the maintenance cost of switching the
pod between HTTP and HTTPS, mounting the certificate and having the operator trust
Home Assistant over HTTPS outweighed its value next to mature edge termination.

To keep HTTPS:

1. Drop the `spec.alpha.tls` block from your `HomeAssistant` manifest. (Newer
   operators ignore an unknown field, so a stale manifest applies without error and
   `kubectl diff` shows no drift.)
2. Enable **Ingress** (`spec.ingress.tls`) or **API Gateway** (`spec.gateway`) as
   shown above — both terminate TLS at the edge, in front of the Service.
3. If your automation waited on the `TLSReady` condition, switch it to
   `ExposureReady` instead.

On upgrade, an instance that had native TLS enabled reverts to HTTP automatically
on the first reconcile, the operator-managed `<name>-native-tls` Certificate is
deleted, and a single `Warning` event (`NativeTLSRemoved`) is emitted. A TLS Secret
you provided yourself (`secretName`) is never deleted — only unmounted.

## Webhook

The operator ships a validating admission webhook that checks the coherence of your
configuration at apply time (for example, it rejects `spec.ingress.tls` enabled
without an `issuerRef` or `secretName`). It is **enabled by default (opt-out)** —
more validations will be added over time; disable it with `--set webhook.enabled=false`
if it ever gets in your way.

### Self-managed serving certificate (default — no cert-manager)

By default the **operator manages its own serving certificate**: it generates a
self-signed certificate, rotates it automatically, and injects the CA bundle into
its own `ValidatingWebhookConfiguration`. This needs **no cert-manager** and works
the same on Helm, Kustomize and plain manifests.

| `webhook.enabled` | `webhook.certManager.enabled` | Serving certificate |
|-------------------|-------------------------------|---------------------|
| `true` (default)  | `false` (default)             | **Self-managed by the operator — no cert-manager** |
| `true`            | `true`                        | Issued by cert-manager, CA injected via annotation |
| `false`           | —                             | Webhook not deployed (`ENABLE_WEBHOOKS=false`) |

### cert-manager (opt-in override)

If you prefer cert-manager to issue and rotate the serving certificate (for example
to centralize certificate policy), opt in:

```bash
helm upgrade ha-operator ... \
  --set webhook.certManager.enabled=true   # requires cert-manager installed
```

!!! info "Installation never requires cert-manager"
    The webhook's default self-managed path needs no cert-manager. Enable the
    cert-manager override only when cert-manager is installed.

!!! tip "Availability of a default-on webhook"
    With `failurePolicy: Ignore` (the default), `HomeAssistant` create/update calls
    are admitted best-effort while the webhook is unavailable (e.g. during an
    operator restart) — validation simply doesn't run for that call. Set
    `--set webhook.failurePolicy=Fail` to reject calls instead while the webhook is
    down, or disable the webhook entirely.

## Status conditions

The operator reflects TLS state in `status.conditions`:

| Condition | Meaning |
|-----------|---------|
| `CertManagerAvailable` | Whether cert-manager was detected on the cluster |
| `ExposureReady` | Whether the Ingress/Gateway exposure resources are reconciled |

```bash
kubectl get homeassistant home -o jsonpath='{.status.conditions}' | jq
```

## Behavior without cert-manager

If you enable a cert-manager-backed TLS mode while cert-manager is absent:

- The resource reports `CertManagerAvailable=False` (reason `CertManagerNotInstalled`)
  and emits a `CertManagerUnavailable` event.
- Home Assistant keeps serving over HTTP; exposure keeps working over HTTP.
- No error is raised and reconciliation does not loop.
- Once you install cert-manager, the operator provisions the certificate automatically.

See also: [Troubleshooting](../reference/troubleshooting.md) and the
[`config/samples/`](https://github.com/przemekhys/homeassistant-operator/tree/main/config/samples)
directory (`ha_v1_ingress_tls.yaml`, `ha_v1_gateway_managed_tls.yaml`).
