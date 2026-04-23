# Configuration Management

`HomeAssistantConfiguration` manages `configuration.yaml` as a Kubernetes resource. The operator generates a ConfigMap from `spec.configuration` and mounts it into the HA pod. When the configuration changes, the operator either hot-reloads specific components or triggers a rolling restart — depending on which sections changed and the chosen `reloadStrategy`.

## Example

```yaml
apiVersion: ha.homeassistant.io/v1alpha1
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
