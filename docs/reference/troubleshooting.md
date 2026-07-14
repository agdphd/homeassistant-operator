# Troubleshooting

## Operator does not reconcile resources in a namespace

If the operator ignores `HomeAssistant` (or other) CRs in a given namespace, it is likely running in **namespace-scoped** mode without that namespace in its watch list.

```sh
# Check which namespaces the operator watches
kubectl get deployment -n homeassistant-operator-system \
  -l control-plane=controller-manager \
  -o jsonpath='{.items[0].spec.template.spec.containers[0].env[?(@.name=="WATCH_NAMESPACES")].value}'
```

- Empty / not set → cluster-wide mode, watches all namespaces.
- Comma-separated list → only those namespaces are watched.

Fix: add the namespace to `watchNamespaces` in your Helm values and upgrade. Remember the operator's **own** namespace is not auto-included. See [Installation → Restrict watched namespaces](../getting-started/installation.md#restrict-watched-namespaces-watchnamespaces).

## Operator gets banned by Home Assistant (`BanRecoveryFailed`)

When HA has `ip_ban_enabled: true`, repeated failed logins can ban the operator's IP (HTTP `403`). The operator self-recovers by restarting the HA pod with the `unban-operator-ip` init-container — but this is rate-limited to **3 restarts per 30 minutes**. Once exceeded, it stops and sets a condition:

```sh
kubectl describe ha home | grep -A3 BanRecoveryFailed
```

Manual recovery:

```sh
# 1. Remove the operator IP from ip_bans.yaml on the PVC, e.g. via a debug pod
#    mounting <ha-name>-config, or edit /config/ip_bans.yaml directly.
# 2. Restart the HA pod so the init-container runs and HA starts unbanned:
kubectl delete pod home-0
```

The window resets automatically after 30 minutes or on the first successful HA connection. See [Home Assistant CR → IP ban self-recovery](../user-guide/homeassistant.md#ip-ban-self-recovery).


## HomeAssistant stuck in `WaitingForConfiguration`

A `HomeAssistant` CR requires a `HomeAssistantConfiguration` CR with a matching `spec.homeAssistantRef.name` in the same namespace (since v0.3.0). Without it the status stays `WaitingForConfiguration` and requeues every 5s.

```sh
# List HomeAssistantConfigurations and check spec.homeAssistantRef.name matches "home"
kubectl get homeassistantconfigurations -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.spec.homeAssistantRef.name}{"\n"}{end}'
```

## Finalizer blocks resource deletion

If a CR hangs on deletion, the operator was likely removed before the CR (finalizer cleanup could not run). Restore the operator, or force-remove the finalizer as a last resort:

```sh
kubectl patch <resource> <name> -p '{"metadata":{"finalizers":null}}' --type=merge
```

## TLS mode enabled but no certificate is issued

Check the status conditions ([TLS guide](../user-guide/tls.md)):

```sh
kubectl get homeassistant <name> -o jsonpath='{.status.conditions}' | jq
```

- **`CertManagerAvailable=False` (`CertManagerNotInstalled`)** — cert-manager is not
  installed. Home Assistant keeps serving over HTTP; install cert-manager and the
  operator picks it up automatically (no restart needed).
- **`TLSReady=False` (`CertificateNotIssued`)** — the `Certificate` exists but has not
  been issued. Inspect it: `kubectl describe certificate <name>-native-tls` (or
  `-ingress-tls` / `-gateway-tls`) and check the referenced `Issuer`/`ClusterIssuer`
  is Ready.
- **Native TLS pod not switching to HTTPS** — the pod switches only after the TLS
  Secret exists. Confirm the Secret (`<name>-native-tls`) is populated and that the
  StatefulSet rolled out.
