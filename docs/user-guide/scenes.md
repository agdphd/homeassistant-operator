# Scenes

`HomeAssistantScene` manages a single Home Assistant scene as a Kubernetes resource. The operator pushes it to HA via the REST config API and hot-reloads the scene component after each change.

## How it works

- **Create/Update**: `POST /api/config/scene/config/{id}` — idempotent (HA uses POST, not PUT, for this endpoint)
- **Delete**: finalizer calls `DELETE /api/config/scene/config/{id}`
- **Hot-reload**: `POST /api/services/scene/reload` after each write

Requires a bootstrap API token. If missing, the operator requeues.

## Basic example

```yaml
apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantScene
metadata:
  name: movie-night
spec:
  homeAssistantRef:
    name: home
  name: "Movie Night"
  icon: "mdi:movie"
  entities:
    - entity_id: light.living_room
      state: "on"
      attributes:
        brightness: 30
        color_temp: 500
    - entity_id: cover.blinds
      state: "closed"
    - entity_id: media_player.tv
      state: "on"
  autoReload: true
```

## Advanced example — multiple rooms

```yaml
apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantScene
metadata:
  name: good-morning
spec:
  homeAssistantRef:
    name: home
  name: "Good Morning"
  icon: "mdi:weather-sunny"
  entities:
    - entity_id: light.bedroom
      state: "on"
      attributes:
        brightness: 80
        color_temp: 250
    - entity_id: light.kitchen
      state: "on"
      attributes:
        brightness: 255
    - entity_id: cover.bedroom_blinds
      state: "open"
    - entity_id: climate.living_room
      state: "heat"
      attributes:
        temperature: 21
  autoReload: true
```

## Spec reference

| Field | Type | Description |
|-------|------|-------------|
| `homeAssistantRef.name` | string | Name of the `HomeAssistant` CR |
| `name` | string | Scene name displayed in the HA UI |
| `id` | string | Scene ID in HA. Defaults to `metadata.name` |
| `icon` | string | Material Design icon (e.g. `mdi:sofa`) |
| `entities` | list | Entities and their desired states |
| `entities[].entity_id` | string | HA entity ID |
| `entities[].state` | string | Desired state (`on`, `off`, `open`, `closed`, etc.) |
| `entities[].attributes` | map | Additional state attributes (brightness, temperature, etc.) |
| `autoReload` | bool | Hot-reload after changes. Default: `true` |

## Status and events

```sh
kubectl get hasc movie-night
```
```
NAME          HOMEASSISTANT   READY   AGE
movie-night   home            True    1m
```

```sh
kubectl describe hasc movie-night
```
```
Conditions:
  ReloadReady: True
Events:
  ReloadSuccessful   Scene reloaded successfully
```

## Activating a scene

Scenes are activated from the HA UI, automations, or via the REST API:

```sh
curl -X POST http://<ha-host>:8123/api/services/scene/turn_on \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"entity_id": "scene.movie_night"}'
```

## Deleting a scene

```sh
kubectl delete hasc movie-night
```

The finalizer removes the scene from HA before deleting the CR.
