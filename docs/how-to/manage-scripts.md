# Scripts

`HomeAssistantScript` manages a single Home Assistant script as a Kubernetes resource. Scripts are reusable sequences of actions that can accept input parameters and be called from automations, dashboards, or the HA UI.

## How it works

- **Create/Update**: `POST /api/config/script/config/{id}` — HA applies the script immediately; no separate reload call is made. The controller records `LastReloadMethod: api` on success.
- **Delete**: finalizer calls `DELETE /api/config/script/config/{id}`

Requires a bootstrap API token.

## Basic example

```yaml
apiVersion: ha.homeassistant.io/v1
kind: HomeAssistantScript
metadata:
  name: notify-mobile
spec:
  homeAssistantRef:
    name: home
  alias: "Notify Mobile Devices"
  description: "Send a notification to all mobile devices"
  icon: "mdi:cellphone-message"
  mode: queued
  max: 5
  fields:
    message:
      description: "The message to send"
      example: "Dinner is ready!"
      selector:
        text:
          multiline: false
    title:
      description: "Notification title"
      example: "Alert"
      selector:
        text:
          multiline: false
  sequence:
    - service: notify.mobile_app_phone
      data:
        title: "{{ title }}"
        message: "{{ message }}"
    - delay:
        seconds: 1
    - service: notify.mobile_app_tablet
      data:
        title: "{{ title }}"
        message: "{{ message }}"
  autoReload: true
```

## Advanced example — conditional sequence

```yaml
apiVersion: ha.homeassistant.io/v1
kind: HomeAssistantScript
metadata:
  name: arrive-home
spec:
  homeAssistantRef:
    name: home
  alias: "Arrive Home"
  description: "Actions to run when someone arrives home"
  icon: "mdi:home-account"
  mode: single
  sequence:
    - action: light.turn_on
      target:
        entity_id: light.entrance
      data:
        brightness: 255
    - action: climate.set_temperature
      target:
        entity_id: climate.living_room
      data:
        temperature: 21
    - condition: time
      after: "18:00:00"
      before: "23:00:00"
    - action: media_player.turn_on
      target:
        entity_id: media_player.living_room
  autoReload: true
```

## Spec reference

| Field | Type | Description |
|-------|------|-------------|
| `homeAssistantRef.name` | string | Name of the `HomeAssistant` CR |
| `alias` | string | Script name displayed in the HA UI |
| `description` | string | Optional description |
| `id` | string | Script ID in HA. Defaults to `metadata.name` |
| `icon` | string | Material Design icon |
| `mode` | string | `single`, `restart`, `queued`, `parallel` |
| `max` | int | Max concurrent/queued runs |
| `maxExceeded` | string | Log level when max exceeded: `silent`, `info`, `warning`, `error` |
| `fields` | map | Named input parameters with selector definitions |
| `sequence` | list | Sequence of HA actions |
| `autoReload` | bool | Hot-reload after changes. Default: `true` |

## Execution modes

| Mode | Behaviour |
|------|-----------|
| `single` | Only one run at a time; new triggers while running are ignored |
| `restart` | Stop current run and start fresh on each trigger |
| `queued` | Queue new runs; execute sequentially (up to `max`) |
| `parallel` | Allow multiple simultaneous runs (up to `max`) |

## Calling a script from an automation

```yaml
actions:
  - action: script.notify_mobile
    data:
      title: "Alert"
      message: "Front door opened"
```

## Status and events

```sh
kubectl get hascp notify-mobile
```
```
NAME            HOMEASSISTANT   READY   AGE
notify-mobile   home            True    2m
```

```sh
kubectl describe hascp notify-mobile
```
```
Conditions:
  ReloadReady: True
Events:
  ReloadSuccessful   Script reloaded successfully
```
