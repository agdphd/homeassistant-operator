# TLS with cert-manager

The operator integrates with [cert-manager](https://cert-manager.io/) to provision
TLS certificates for three independent use cases:

- **Native TLS** — Home Assistant serves HTTPS itself, on its existing port.
- **Ingress / API Gateway** — the operator manages the edge routing and its certificate.
- **Webhook** — the operator's validating admission webhook serves over TLS.

!!! info "cert-manager is an optional, external dependency"
    Neither the operator nor its Helm chart ever installs cert-manager. You install
    it (and provide an `Issuer`/`ClusterIssuer`) yourself. If cert-manager is **not**
    present, the operator keeps running normally: any TLS mode you enabled simply
    stays inactive and the resource reports a status condition — nothing fails or
    loops. A cert-manager installed *after* the operator is picked up automatically.

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

## Native TLS (alpha)

Home Assistant terminates TLS itself, serving HTTPS on its existing port (`8123`) —
no reverse proxy required. The operator provisions a certificate, mounts the Secret
into the pod at `/config/ssl`, sets `http.ssl_certificate`/`ssl_key` in the generated
configuration, and switches its own connection to Home Assistant to HTTPS (trusting
the issued CA — certificate verification is never disabled).

!!! warning "This is an `spec.alpha` feature"
    Native TLS changes how Home Assistant serves traffic and how the operator
    connects to it, so it lives under `spec.alpha` and is **off by default**. Alpha
    fields may change or be removed without a deprecation notice.

```yaml
apiVersion: ha.homeassistant.io/v1
kind: HomeAssistant
metadata:
  name: home
spec:
  alpha:
    tls:
      native:
        enabled: true
        issuerRef:
          name: ca-issuer
          kind: ClusterIssuer
        dnsNames:
          - ha.example.com
        # secretName: my-tls   # bring-your-own; overrides issuerRef
```

The operator always adds the in-cluster Service FQDN
(`<name>.<namespace>.svc.cluster.local`) to the certificate's SANs so it can verify
Home Assistant over HTTPS. When cert-manager rotates the certificate, the pod is
rolled to pick up the new material.

The pod switches to HTTPS only **after** the certificate is issued, so enabling the
mode never leaves Home Assistant stuck without a certificate.

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

## Webhook TLS

The operator ships a validating admission webhook that checks the coherence of your
TLS configuration (for example, it rejects native TLS enabled without an `issuerRef`
or `secretName`). Its serving certificate is wired through the Helm chart:

```bash
# cert-manager-issued serving certificate (requires cert-manager)
helm upgrade ha-operator ... \
  --set webhook.enabled=true \
  --set webhook.certManager.enabled=true
```

| `webhook.enabled` | `webhook.certManager.enabled` | Serving certificate |
|-------------------|-------------------------------|---------------------|
| `false` (default) | —                             | Webhook not deployed; install needs no cert-manager |
| `true`            | `false` (default)             | Self-signed cert generated by the chart (no cert-manager) |
| `true`            | `true`                        | Issued by cert-manager, CA injected into the webhook config |

!!! info "Installation never requires cert-manager"
    The webhook is off by default, and even when enabled the default (self-signed)
    path needs no cert-manager. Enable the cert-manager path only when cert-manager
    is installed.

## Status conditions

The operator reflects TLS state in `status.conditions`:

| Condition | Meaning |
|-----------|---------|
| `CertManagerAvailable` | Whether cert-manager was detected on the cluster |
| `TLSReady` | Whether the certificate for the enabled TLS mode has been issued |
| `ExposureReady` | Whether the Ingress/Gateway exposure resources are reconciled |

```bash
kubectl get homeassistant home -o jsonpath='{.status.conditions}' | jq
```

## Behavior without cert-manager

If you enable a cert-manager-backed TLS mode while cert-manager is absent:

- The resource reports `CertManagerAvailable=False` (reason `CertManagerNotInstalled`)
  and `TLSReady=Unknown`, and emits a `CertManagerUnavailable` event.
- Home Assistant keeps serving over HTTP; exposure keeps working over HTTP.
- No error is raised and reconciliation does not loop.
- Once you install cert-manager, the operator provisions the certificate automatically.

See also: [Troubleshooting](../reference/troubleshooting.md) and the
[`config/samples/`](https://github.com/przemekhys/homeassistant-operator/tree/main/config/samples)
directory (`ha_v1_native_tls.yaml`, `ha_v1_ingress_tls.yaml`, `ha_v1_gateway_managed_tls.yaml`).
