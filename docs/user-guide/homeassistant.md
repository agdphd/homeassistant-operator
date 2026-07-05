# Home Assistant CR

The `HomeAssistant` resource is the central CR — it creates the StatefulSet, Service, and PVC for a Home Assistant instance. All other CRDs reference it via `spec.homeAssistantRef.name`.

## Prerequisites

A `HomeAssistantConfiguration` CR with a matching `spec.homeAssistantRef.name` must exist before or alongside the `HomeAssistant` CR. Without it, the operator sets status to `WaitingForConfiguration` and requeues every 5 seconds.

## Minimal example

```yaml
apiVersion: ha.homeassistant.io/v1
kind: HomeAssistantConfiguration
metadata:
  name: home
spec:
  homeAssistantRef:
    name: home
  configuration: |
    default_config:
---
apiVersion: ha.homeassistant.io/v1
kind: HomeAssistant
metadata:
  name: home
spec:
  version: "2025.6"
  storage:
    size: 5Gi
  service:
    type: ClusterIP
```

## Spec reference

### `spec.version`

Home Assistant image tag. Accepts any tag published to `ghcr.io/home-assistant/home-assistant`.

```yaml
spec:
  version: "2025.6"     # specific version (recommended)
  # version: "stable"   # latest stable
```

### `spec.timezone`

Timezone passed to the container. Defaults to `UTC`.

```yaml
spec:
  timezone: "Europe/Warsaw"
```

### `spec.storage`

Configures the PersistentVolumeClaim for `/config`.

```yaml
spec:
  storage:
    size: 10Gi
    storageClassName: local-path   # optional; uses cluster default if omitted
    accessMode: ReadWriteOnce      # optional; default ReadWriteOnce
```

### `spec.service`

Controls the Kubernetes Service created for the HA pod.

```yaml
spec:
  service:
    type: ClusterIP      # ClusterIP | NodePort | LoadBalancer
    port: 8123
```

### `spec.ingress`

Optional Ingress resource.

```yaml
spec:
  ingress:
    enabled: true
    host: ha.example.com
    ingressClassName: nginx
    tls:
      secretName: ha-tls
```

### `spec.resources`

CPU and memory requests/limits for the HA container.

```yaml
spec:
  resources:
    requests:
      cpu: "100m"
      memory: "256Mi"
    limits:
      cpu: "1000m"
      memory: "1Gi"
```

### `spec.hostNetwork`

Enables host networking for mDNS/SSDP/DHCP device discovery on the local LAN. Off by default.

```yaml
spec:
  hostNetwork: true
```

!!! warning
    `hostNetwork: true` binds HA directly to the node's network interface. Use only on single-node clusters or when LAN device discovery is required.

### `spec.secretsFrom`

Direct reference to a Kubernetes Secret containing a `secrets.yaml` blob. Prefer `HomeAssistantSecrets` CR for managed secret composition.

```yaml
spec:
  secretsFrom:
    name: my-raw-secrets
```

## Status

```sh
kubectl get ha home
```
```
NAME   READY   STATUS    VERSION   AGE
home   True    Running   2025.6    5m
```

```sh
kubectl describe ha home
```
```
Status:
  Conditions:
    Type:    Ready
    Status:  True
  Bootstrap:
    Completed:       true
    API Token Ready: true
```

## IP ban self-recovery

When Home Assistant has `ip_ban_enabled: true`, it can ban the operator's own IP after repeated failed logins (HTTP `403`) — for example during bootstrap retries. A banned operator can no longer reach the HA API, which would normally require manual editing of `/config/ip_bans.yaml`. The operator recovers from this automatically, **without** needing the `pods/exec` RBAC permission.

### How it works

1. The operator detects it is banned (HTTP `403` from HA).
2. It deletes the HA pod. The `StatefulSet` recreates it with an `unban-operator-ip` init-container (reusing the HA image already cached on the node).
3. The init-container removes the operator's IP from `/config/ip_bans.yaml` **before** HA starts, then HA comes up unbanned.

The operator's IP is passed to the pod via the `<ha-name>-operator-ip` ConfigMap, sourced from the `POD_IP` downward-API environment variable on the operator Deployment (set by default in the Helm chart).

### Sliding-window protection

To avoid a restart loop, recovery is rate-limited:

- At most **3 pod restarts** within a **30-minute** window.
- A minimum **5-minute cooldown** between consecutive restarts.
- The window resets automatically after 30 minutes **or** on the first successful HA connection.

Once the limit is exceeded the operator stops restarting and sets the `BanRecoveryFailed=True` condition, requiring manual intervention:

```sh
kubectl describe ha home   # look for the BanRecoveryFailed condition
```

Manual recovery: remove the operator's IP from `/config/ip_bans.yaml` on the PVC and restart the pod (`kubectl delete pod home-0`).

### Status fields

| Field | Meaning |
|-------|---------|
| `status.selfUnbanCount` | Total number of self-unban restarts performed. |
| `status.lastSelfUnban` | Timestamp of the most recent self-unban. |


## Deleting an instance

!!! warning
    The PVC is **not** deleted automatically when the `HomeAssistant` CR is removed — this protects your HA configuration data. Delete the PVC manually if you want a clean teardown:
    ```sh
    kubectl delete ha home
    kubectl delete pvc home-config   # name: <ha-name>-config
    ```
