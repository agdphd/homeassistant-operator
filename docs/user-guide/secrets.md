# Secrets Management

`HomeAssistantSecrets` composes multiple Kubernetes Secrets into a single `secrets.yaml` that Home Assistant mounts at `/config/secrets.yaml`. This lets you store credentials (MQTT passwords, database URLs, API keys) in native Kubernetes Secrets and reference them in `configuration.yaml` with `!secret`.

## How it works

1. The operator reads all referenced Kubernetes Secrets
2. It merges their keys into a single YAML document
3. The result is stored in a `ConfigMap` named `<ha-name>-generated-secrets`
4. A SHA-256 hash of the content is written to the StatefulSet pod template annotation
5. Kubernetes detects the annotation change and performs a rolling restart (configurable)

## Example

```yaml
# Source secrets (created separately)
# kubectl create secret generic mqtt-credentials \
#   --from-literal=mqtt_user=homeassistant \
#   --from-literal=mqtt_password=supersecret

apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantSecrets
metadata:
  name: home-secrets
spec:
  homeAssistantRef:
    name: home
  autoRestart: true
  secretRefs:
    - name: mqtt-credentials
      keys:
        - mqtt_user
        - mqtt_password
    - name: database-credentials
      keys:
        - db_url
    # Include all keys from a secret (no keys list):
    - name: ha-extra-secrets
```

Then reference in `configuration.yaml`:

```yaml
mqtt:
  broker: !secret mqtt_broker
  username: !secret mqtt_user
  password: !secret mqtt_password
```

## Spec reference

### `spec.homeAssistantRef.name`

Name of the `HomeAssistant` CR this resource belongs to.

### `spec.secretRefs`

List of Kubernetes Secrets to merge.

| Field | Description |
|-------|-------------|
| `name` | Name of the Kubernetes Secret |
| `keys` | Specific keys to include. If omitted, **all keys** from the Secret are included |

### `spec.autoRestart`

When `true` (default), any change to the referenced Secrets triggers a rolling restart of the HA pod via a hash annotation on the StatefulSet.

Set to `false` if you manage restarts externally (e.g. [Stakater Reloader](https://github.com/stakater/Reloader)).

```yaml
spec:
  autoRestart: false
```

## Status

```sh
kubectl get hasec home-secrets
```
```
NAME            HOMEASSISTANT   READY   AGE
home-secrets    home            True    2m
```

```sh
kubectl describe hasec home-secrets
```
```
Status:
  Conditions:
    Type:    Ready
    Status:  True
  Secrets Hash: sha256:abc123...
  Last Updated: 2026-04-23T10:00:00Z
```

## Updating secrets

When a source Kubernetes Secret changes, the operator automatically detects it, regenerates `secrets.yaml`, and triggers a rolling restart (if `autoRestart: true`). No manual intervention is needed.

```sh
# Rotate MQTT password
kubectl create secret generic mqtt-credentials \
  --from-literal=mqtt_user=homeassistant \
  --from-literal=mqtt_password=newpassword \
  --dry-run=client -o yaml | kubectl apply -f -
# operator picks it up within seconds
```

## Generated ConfigMap

The composed `secrets.yaml` is stored in:

```sh
kubectl get configmap home-generated-secrets -o jsonpath='{.data.secrets\.yaml}'
```

!!! warning
    Do not edit this ConfigMap directly — the operator overwrites it on every reconcile. Edit the source Kubernetes Secrets or the `HomeAssistantSecrets` CR instead.
