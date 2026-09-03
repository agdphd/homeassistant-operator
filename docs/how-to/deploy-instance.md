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

#### Default trusted proxies

Home Assistant rejects every request with `400 Bad Request` unless it is told
to trust the proxy in front of it. Whenever `spec.ingress.enabled` or
`spec.gateway.enabled` is `true`, the operator automatically supplies the
following, unless the keys are already present:

```yaml
http:
  use_x_forwarded_for: true
  trusted_proxies:
    - 10.0.0.0/8
    - 172.16.0.0/12
    - 192.168.0.0/16
```

On Home Assistant 2026.8+ these are delivered through the http config API rather
than written into `configuration.yaml` (see
[Configuration → HTTP configuration](configuration.md#http-configuration-on-home-assistant-20268)),
with no change to the outcome. On older Home Assistant they go into the generated
`configuration.yaml`.

These are the RFC1918 private address ranges — a conservative default, not an
autodetection of the real cluster pod/service CIDR (which cannot be reliably
read from the Kubernetes API). Each key is added independently: if you have
already set either `http.use_x_forwarded_for` or `http.trusted_proxies`
yourself in `HomeAssistantConfiguration`, the operator leaves your value
untouched and only fills in the missing key. If `http:` itself is an
externally managed tagged block (for example `http: !include http.yaml`),
the operator leaves it completely untouched — set the keys in that included
file, or move the section into `HomeAssistantConfiguration.spec.configuration`
directly, if you want the operator to manage them.

**Security note**: because these are broad RFC1918 ranges, in most Kubernetes
clusters they cover every pod on the network, not just your actual Ingress
controller or Gateway. Any reachable workload can then set its own
`X-Forwarded-For` header and have Home Assistant trust it as the real client
IP, weakening IP-based bans, rate limiting, and audit-log attribution. If
other workloads in the cluster aren't trusted, replace the default
`trusted_proxies` with the actual CIDR of your Ingress/Gateway proxy (for
example, the ingress controller's pod or Service CIDR) in
`HomeAssistantConfiguration`, or disable the defaults below and configure
`http.trusted_proxies`/`http.use_x_forwarded_for` yourself.

To opt out entirely (for example, if your cluster's pod/service network isn't
RFC1918, or you want to set narrower proxy ranges yourself), set:

```yaml
spec:
  disableDefaultTrustedProxies: true
```

The `HomeAssistant` resource's `ExposureReady` condition message reports
which of the three states applies: `default trusted proxies applied`, `using
user-configured trusted proxies`, or `default trusted proxies disabled
(opt-out)`.

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

    It also weakens `spec.alpha.networkPolicy.enabled` (see below): NetworkPolicy operates on pod IPs, so it does not restrict traffic arriving via the host's network interface. Combining both gives only partial isolation.

### `spec.scheduling`

Controls where the Home Assistant pod is eligible to run and how it's treated under resource contention, using Kubernetes' own scheduling primitives directly — every field is copied verbatim onto the generated pod template. All fields are optional; leaving `spec.scheduling` unset preserves today's freely-schedulable, default-priority behavior exactly. Unlike other risky capabilities in this operator, this ships on the stable spec, not `spec.alpha.*`: the operator only exposes Kubernetes' own long-stable scheduling API, it doesn't implement any new scheduling behavior of its own.

```yaml
spec:
  scheduling:
    nodeSelector:
      ha-device-node: zigbee
    affinity:
      nodeAffinity:
        preferredDuringSchedulingIgnoredDuringExecution:
          - weight: 1
            preference:
              matchExpressions:
                - key: ha-storage
                  operator: In
                  values: ["nvme"]
    tolerations:
      - key: ha-dedicated
        operator: Equal
        value: "true"
        effect: NoSchedule
    priorityClassName: ha-critical
```

Fields:

- `nodeSelector` (optional): restricts the pod to nodes matching all of these labels — the simplest way to pin the pod to a specific node (see `spec.alpha.devices` below).
- `affinity` (optional): node affinity/anti-affinity and pod affinity/anti-affinity rules, using Kubernetes' own `Affinity` semantics unchanged (`nodeAffinity`, `podAffinity`, `podAntiAffinity`).
- `tolerations` (optional): allows the pod onto nodes with matching taints that would otherwise repel it (e.g. a node pool dedicated to hardware-attached workloads).
- `priorityClassName` (optional): assigns a `PriorityClass` to the pod, influencing preemption/eviction order under resource contention. Must name an existing `PriorityClass` — the operator rejects the resource at admission time if it doesn't exist.

The `HomeAssistant` resource's `SchedulingReady` status condition reports whether the pod's declared constraints are currently satisfiable (mirroring the pod's own `PodScheduled` condition), so an impossible-to-satisfy `nodeSelector`/`affinity` combination is diagnosable straight from `kubectl describe homeassistant` instead of a generic "not ready".

!!! note
    Kubernetes only evaluates scheduling constraints when a pod is placed — editing `spec.scheduling` on an already-running instance triggers a pod recreation so the new constraint actually takes effect; it does not live-migrate the running pod.

### `spec.alpha.networkPolicy`

!!! note "Alpha"
    Opt-in, off by default. Fields under `spec.alpha` are experimental and may change or be removed without a deprecation notice.

When enabled, the operator creates a `NetworkPolicy` restricting ingress to the Home Assistant pod to the same namespace and the operator's own namespace, on the Service port. Egress is left unrestricted — Home Assistant needs broad, unpredictable egress to IoT devices, cloud APIs, and MQTT brokers.

```yaml
spec:
  alpha:
    networkPolicy:
      enabled: true
```

!!! warning
    The operator-namespace ingress peer is only added when the controller knows its own namespace via the `OPERATOR_NAMESPACE` environment variable (set automatically by the shipped manifests). If it is unset, the operator silently omits that peer and only logs a warning — the resulting policy blocks the operator from reaching the HA API, breaking bootstrap, hot-reload, and health checks. Ensure `OPERATOR_NAMESPACE` is set on the controller before enabling this.

### `spec.alpha.devices`

!!! note "Alpha"
    Opt-in, off by default. Fields under `spec.alpha` are experimental and may change or be removed without a deprecation notice.

Mounts one or more host device nodes (e.g. `/dev/ttyACM0` for a Zigbee/Z-Wave USB coordinator such as a Conbee2 or SkyConnect) into the Home Assistant container, so integrations like Zigbee2MQTT, Z-Wave JS, or ZHA can open the serial port. Each entry is mounted as a `hostPath` volume typed as a character device — the operator never sets `privileged: true` on the pod for this.

```yaml
spec:
  alpha:
    devices:
      - hostPath: /dev/ttyACM0
        # containerPath defaults to hostPath when omitted
```

Fields per entry:

- `hostPath` (required): the device node's path on the host. Must be an absolute path under `/dev`.
- `containerPath` (optional): the path the device is mounted at inside the container. Defaults to `hostPath`.

!!! warning
    This does **not** by itself pin the pod to the node the device is physically attached to. A USB coordinator only exists on one specific node, so declaring it here is only useful once you've separately pinned the pod there via [`spec.scheduling.nodeSelector`](#specscheduling) (label the node, then match that label). If the declared device isn't present on whichever node the pod lands on, the pod fails to start and the `HomeAssistant` resource's `DevicesReady` status condition names the missing path.

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
