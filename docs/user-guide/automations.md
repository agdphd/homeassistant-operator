# Automations

`HomeAssistantAutomation` manages a single Home Assistant automation as a Kubernetes resource. The operator pushes it directly to HA via the REST config API — no ConfigMap aggregation. HA stores the automation in `automations.yaml` on the PVC, so it survives pod restarts.

## How it works

- **Create/Update**: `PUT /api/config/automation/config/{id}` — idempotent
- **Delete**: finalizer calls `DELETE /api/config/automation/config/{id}`
- **Hot-reload**: `POST /api/services/automation/reload` after each write

Requires a bootstrap API token (`<ha-name>-api-token` Secret). If the token is missing, the operator requeues until it becomes available.

## Basic example

```yaml
apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAutomation
metadata:
  name: lights-at-sunset
spec:
  homeAssistantRef:
    name: home
  alias: "Turn on lights at sunset"
  description: "Turns on living room lights 30 minutes before sunset"
  triggers:
    - trigger: sun
      event: sunset
      offset: "-00:30:00"
  conditions:
    - condition: state
      entity_id: binary_sensor.someone_home
      state: "on"
  actions:
    - action: light.turn_on
      target:
        entity_id: light.living_room
      data:
        brightness: 200
        color_temp: 400
  mode: single
  autoReload: true
  enabled: true
```

## Advanced example — multiple triggers and actions

```yaml
apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAutomation
metadata:
  name: security-alert
spec:
  homeAssistantRef:
    name: home
  alias: "Security alert"
  triggers:
    - trigger: state
      entity_id: binary_sensor.motion_detector
      from: "off"
      to: "on"
    - trigger: state
      entity_id: binary_sensor.front_door
      from: "off"
      to: "on"
  conditions:
    - condition: state
      entity_id: binary_sensor.someone_home
      state: "off"
    - condition: time
      after: "22:00:00"
      before: "06:00:00"
  actions:
    - action: light.turn_on
      target:
        entity_id: all
      data:
        brightness: 255
    - action: notify.mobile_app
      data:
        title: "Security Alert!"
        message: "Motion detected while nobody is home"
    - delay:
        minutes: 5
    - action: light.turn_off
      target:
        entity_id: all
  mode: restart
```

## Spec reference

| Field | Type | Description |
|-------|------|-------------|
| `homeAssistantRef.name` | string | Name of the `HomeAssistant` CR |
| `alias` | string | Human-readable name shown in the HA UI |
| `description` | string | Optional description |
| `id` | string | Automation ID in HA. Defaults to `metadata.name` if empty |
| `triggers` | list | Trigger objects (HA trigger syntax) |
| `conditions` | list | Condition objects (all must pass) |
| `actions` | list | Action objects to execute |
| `mode` | string | `single`, `restart`, `queued`, `parallel` |
| `max` | int | Max concurrent runs (for `queued`/`parallel`) |
| `maxExceeded` | string | Log level when max exceeded: `silent`, `info`, `warning`, `error` |
| `initialState` | bool | State on HA startup |
| `autoReload` | bool | Hot-reload after changes. Default: `true` |
| `enabled` | bool | Enable/disable the automation. Default: `true` |

## Status and events

```sh
kubectl get haauto lights-at-sunset
```
```
NAME               HOMEASSISTANT   READY   AGE
lights-at-sunset   home            True    3m
```

```sh
kubectl describe haauto lights-at-sunset
```
```
Conditions:
  ReloadReady: True
Events:
  ReloadSuccessful   Automation reloaded successfully (ReloadID: abc123)
```

## Disabling without deleting

```yaml
spec:
  enabled: false
```

The automation remains in HA but is disabled. Re-enable by setting `enabled: true`.

## Deleting an automation

```sh
kubectl delete haauto lights-at-sunset
```

The finalizer calls `DELETE /api/config/automation/config/lights-at-sunset` before removing the CR. If HA is unavailable, the finalizer still completes (best-effort) to avoid stuck CRs.
