# Configuration Management

`HomeAssistantConfiguration` manages `configuration.yaml` as a Kubernetes resource. The operator generates a ConfigMap from `spec.configuration` and mounts it into the HA pod. When the configuration changes, the operator either hot-reloads specific components or triggers a rolling restart — depending on which sections changed and the chosen `reloadStrategy`.

## Example

```yaml
apiVersion: ha.homeassistant.io/v1
kind: HomeAssistantConfiguration
metadata:
  name: home
spec:
  homeAssistantRef:
    name: home
  reloadStrategy: auto
  configuration: |
    homeassistant:
      name: My Home
      latitude: 52.237703
      longitude: 20.989075
      unit_system: metric
      currency: PLN
      country: PL
      time_zone: Europe/Warsaw

    default_config:

    logger:
      default: info
      logs:
        homeassistant.components.mqtt: warning

    http:
      use_x_forwarded_for: true
      trusted_proxies:
        - 10.42.0.0/16
```

## Spec reference

### `spec.homeAssistantRef.name`

Name of the `HomeAssistant` CR this configuration belongs to.

### `spec.configuration`

Raw `configuration.yaml` content as a multi-line string. Any valid Home Assistant configuration is accepted.

### `spec.reloadStrategy`

Controls how changes are applied.

| Value | Behaviour |
|-------|-----------|
| `auto` (default) | Hot-reload for `automation`, `script`, `scene`, `logger`, `input_*`, `template`, `zone`. Restart for `homeassistant`, `http`, `mqtt`, and unknown sections. |
| `hot-reload` | Always attempt hot-reload, regardless of which sections changed. |
| `restart` | Always trigger a rolling restart. |

### `spec.autoReload`

Set to `false` to disable automatic reload/restart on configuration changes. Default: `true`.

## Reload behaviour

When `reloadStrategy: auto` is set, the operator parses the YAML diff between the old and new configuration:

- **Hot-reload** (no restart): `automation`, `script`, `scene`, `logger`, `input_boolean`, `input_number`, `input_text`, `input_select`, `input_datetime`, `template`, `zone`
- **Restart** (rolling restart): `homeassistant`, `http`, `mqtt`, and any unknown top-level key

If a single change touches both categories, the operator restarts (safer path).

## Auto-include

The operator automatically appends the following lines to `configuration.yaml` if they are missing:

```yaml
automation: !include automations.yaml
scene: !include scenes.yaml
script: !include scripts.yaml
```

These lines are required for Home Assistant to load resources managed by `HomeAssistantAutomation`, `HomeAssistantScene`, and `HomeAssistantScript`. You do not need to add them manually.

## HTTP configuration on Home Assistant 2026.8+

Home Assistant 2026.8 moved the `http` integration out of `configuration.yaml`
into its own store, managed from the UI under **Settings → System → Network**.
The YAML `http:` block is ignored after Home Assistant's one-time import, and
stops working entirely in Home Assistant 2027.2.0.

You keep writing `http:` in `spec.configuration` exactly as before — nothing in
your resource changes. On a Home Assistant that supports the new API the operator:

- applies the `http:` section (plus its default `trusted_proxies` /
  `use_x_forwarded_for` for Ingress/Gateway-exposed instances) through that API;
- **omits `http:` from the generated `configuration.yaml`**, so Home Assistant
  does not raise a "remove the http: block" repair issue;
- reports which channel it used in `status.httpConfigSource` (`Api` or `Yaml`)
  and whether it took effect in the `HTTPConfigReady` condition.

On older Home Assistant the `http:` block still goes into `configuration.yaml`
as it always has. Switching Home Assistant versions in either direction is
picked up automatically, with no operator restart.

!!! warning "The resource is the source of truth"
    The operator enforces the `http` configuration from the resource. A change
    made in the Home Assistant UI (Settings → System → Network) to a setting the
    resource covers is **reverted on the next reconcile** — even though Home
    Assistant itself points you at that UI. Edit `spec.configuration`, not the
    Home Assistant UI, for these settings. The operator never confirms a pending
    change it did not send, so a change you start in the UI is left for you (or
    Home Assistant's 5-minute auto-revert) to resolve.

    If your `http:` section is an `!include` of a separate file, the operator
    cannot read it to deliver via the API — it stays in `configuration.yaml` and
    Home Assistant's warning remains until you inline the keys.

## GitOps ownership

The generated ConfigMap is owned exclusively by the operator. Any direct edits to the ConfigMap are detected and reverted on the next reconcile. **Always edit the `HomeAssistantConfiguration` CR**, not the ConfigMap.

```sh
# Correct workflow
kubectl edit haconfig home

# Will be overwritten on next reconcile:
kubectl edit configmap home-configuration
```

## Status

```sh
kubectl get haconfig home
```
```
NAME   HOMEASSISTANT   STRATEGY   READY   AGE
home   home            auto       True    10m
```

```sh
kubectl describe haconfig home
```
```
Status:
  Config Hash:          sha256:abc123...
  Last Reload Time:     2026-04-23T10:00:00Z
  Last Reload Method:   hot-reload
  Last Error:           <none>
  Observed Generation:  3
```

## Multiple strategy examples

=== "Auto (recommended)"

    ```yaml
    spec:
      reloadStrategy: auto
    ```

=== "Always hot-reload"

    ```yaml
    spec:
      reloadStrategy: hot-reload
    ```

=== "Always restart"

    ```yaml
    spec:
      reloadStrategy: restart
    ```
