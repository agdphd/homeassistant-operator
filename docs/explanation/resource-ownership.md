# Who owns the generated resources

*Explanation — why the operator overwrites hand-edited ConfigMaps, and what that means for a GitOps workflow. Nothing here needs a cluster.*

Every ConfigMap, Secret and workload the operator creates is derived state: it is
computed from a custom resource on each pass, and the custom resource is the only
input. Editing the derived object directly is therefore not "a change the
operator will merge" — it is a difference the next reconcile will erase, usually
within seconds.

This is what makes the whole setup safe to drive from Git. Because derived state
is never authoritative, the cluster cannot drift away from the repository: a
manual edit made in a hurry during an incident is undone automatically rather
than silently becoming the new truth that nobody remembers making.

## Configuration ConfigMap

The generated ConfigMap is owned exclusively by the operator. Any direct edits to the ConfigMap are detected and reverted on the next reconcile. **Always edit the `HomeAssistantConfiguration` CR**, not the ConfigMap.

Editing `home-configuration` directly is not "a change the operator will pick
up" — the next pass regenerates that ConfigMap from the
`HomeAssistantConfiguration` and your edit is gone. Change the resource instead:
[manage configuration](../how-to/manage-configuration.md).

## How secrets are composed

1. The operator reads all referenced Kubernetes Secrets
2. It merges their keys into a single YAML document
3. The result is stored in a `ConfigMap` named `<ha-name>-generated-secrets`
4. A SHA-256 hash of the content is written to the StatefulSet pod template annotation
5. Kubernetes detects the annotation change and performs a rolling restart (configurable)

## Generated secrets ConfigMap

The composed `secrets.yaml` is stored in:

The composed `secrets.yaml` lands in a ConfigMap named
`<ha-name>-generated-secrets`. It is derived state like any other: edit the
source Kubernetes Secrets or the `HomeAssistantSecrets` resource, never the
ConfigMap. See [manage secrets](../how-to/manage-secrets.md).

## What this costs you

The honest trade-off: you cannot make a quick fix in the cluster and have it
stick. During an incident that feels like an obstacle. It is the same property
that guarantees the cluster still matches your repository afterwards, when
nobody remembers what was changed at 3am — and that is worth more than the
convenience it takes away.
