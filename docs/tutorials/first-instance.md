# Your first Home Assistant instance

*Tutorial — a guided walkthrough. Follow every step in order; you will end up with a running Home Assistant you can log into from your browser.*

This walkthrough takes you from a Kubernetes cluster with nothing installed on it
to a Home Assistant you can log into. It makes every choice for you — installation
method, storage size, how the instance is exposed — so you can get to something
working before you decide anything. Later, the
[how-to guides](../how-to/index.md) show you how to change each of those choices.

Expect it to take about ten minutes, most of which is waiting for Home Assistant
to start.

## Before you start

You need:

- a Kubernetes cluster, v1.24 or newer, that you can reach with `kubectl`;
- a default StorageClass in that cluster (`kubectl get storageclass` should list
  one marked `(default)`) — Home Assistant keeps its state on a persistent volume;
- `kubectl` and `helm` on your machine.

You do **not** need Home Assistant experience, and you do not need to prepare
anything inside Home Assistant. The operator will create the admin account for
you.

## Step 1 — Install the operator

```sh
helm install homeassistant-operator \
  oci://ghcr.io/przemekhys/charts/homeassistant-operator \
  --namespace homeassistant-operator-system \
  --create-namespace \
  --set 'watchNamespaces={default}'
```

`watchNamespaces` tells the operator which namespaces to look at — here, the
`default` namespace, which is where the rest of this tutorial puts things.

Check that it came up:

```sh
kubectl get pods -n homeassistant-operator-system
```

You should see one pod named `homeassistant-operator-...` with status `Running`.
It usually spends its first half minute in `ContainerCreating`; run the command
again after a few seconds.

## Step 2 — Create the admin credentials

The operator creates the Home Assistant admin account for you, and it reads the
username and password from a Kubernetes Secret:

```sh
kubectl create secret generic ha-admin \
  --from-literal=username=admin \
  --from-literal=password=changeme-please \
  -n default
```

You should see `secret/ha-admin created`.

!!! note "Change this password later"
    `changeme-please` is fine for a first look, but it is the password to your
    whole smart home. Change it in the Home Assistant UI once you are logged in.

## Step 3 — Describe the configuration

Home Assistant needs a `configuration.yaml`. With this operator you never edit
that file directly — you describe it in a `HomeAssistantConfiguration` resource
and the operator generates the file.

Save this as `configuration.yaml`:

```yaml
apiVersion: ha.homeassistant.io/v1
kind: HomeAssistantConfiguration
metadata:
  name: home
  namespace: default
spec:
  homeAssistantRef:
    name: home
  reloadStrategy: auto
  configuration: |
    homeassistant:
      name: Home
      latitude: 52.237703
      longitude: 20.989075
      unit_system: metric
      time_zone: Europe/Warsaw
```

Those coordinates are Warsaw. Put your own in if you like — Home Assistant uses
them for sunrise and sunset times.

## Step 4 — Describe the instance

Save this as `homeassistant.yaml`:

```yaml
apiVersion: ha.homeassistant.io/v1
kind: HomeAssistant
metadata:
  name: home
  namespace: default
spec:
  version: "stable"
  storage:
    size: 5Gi
  service:
    type: NodePort
  bootstrap:
    enabled: true
    credentials:
      secretRef:
        name: ha-admin
    createApiToken: true
```

Two things here are worth naming, because they are what makes the rest of this
tutorial work without you touching the Home Assistant UI:

- `bootstrap.enabled: true` tells the operator to create the admin account and
  finish the onboarding wizard itself.
- `createApiToken: true` tells it to generate a long-lived access token and store
  it in a Secret, which is how the operator talks to Home Assistant afterwards.

`service.type: NodePort` is the simplest way to reach the instance from outside
the cluster. It is not what you would use in production — the
[TLS guide](../how-to/expose-with-tls.md) covers proper exposure — but it needs
no extra components, so it is what this tutorial uses.

## Step 5 — Apply both resources

```sh
kubectl apply -f configuration.yaml -f homeassistant.yaml
```

You should see two lines confirming both resources were created.

!!! note "Order does not matter"
    You can apply these in either order, or minutes apart. If the operator finds
    one without the other it waits rather than failing — see
    [the reconciliation model](../explanation/reconciliation-model.md).

## Step 6 — Watch it come up

```sh
kubectl get ha home -w
```

This takes two to three minutes on a first run, because the Home Assistant image
has to be pulled and Home Assistant needs to initialise its database. Press
++ctrl+c++ to stop watching once you see:

```
NAME   READY   VERSION   AGE
home   True    stable    4m
```

`READY: True` means the operator has waited for Home Assistant to start, created
your admin user, completed onboarding, and generated the API token.

If it stays `False` for more than five minutes, check what the operator is
waiting for:

```sh
kubectl describe ha home
```

The `Conditions` section names the current state; the
[troubleshooting guide](../how-to/troubleshoot.md) covers the common ones.

## Step 7 — Log in

Find the port Kubernetes assigned:

```sh
kubectl get svc home -o jsonpath='{.spec.ports[0].nodePort}'
```

Then find an address for a node in your cluster:

```sh
kubectl get nodes -o wide
```

Open `http://<node-address>:<port>` in a browser and log in with `admin` and the
password from step 2.

Home Assistant then asks you to finish a couple of setup screens — confirming
your location, and looking at the devices it discovered on your network. The
operator deliberately stops after creating the account: those two screens are
about your home, not about the deployment, so they are left to you.

**You are done.** You have a running Home Assistant with an admin account you
never had to create by hand, on storage that survives a pod restart.

## Where to go next

- Everything Home Assistant does can be a Kubernetes resource too:
  [manage automations](../how-to/manage-automations.md) is the natural next step.
- If you plan to keep this instance, read
  [who owns the generated resources](../explanation/resource-ownership.md)
  first — it explains why editing the generated ConfigMap by hand does not work.
- To expose the instance properly instead of over a NodePort, see
  [expose an instance with TLS](../how-to/expose-with-tls.md).

## Clean up

If this was just a look around:

```sh
kubectl delete -f homeassistant.yaml -f configuration.yaml
kubectl delete secret ha-admin -n default
helm uninstall homeassistant-operator -n homeassistant-operator-system
```

That really does remove everything, storage included: by default the volume is
owned by the `HomeAssistant` resource and goes with it. On an instance you intend
to keep, say so explicitly — see
[keep the data when the resource is deleted](../how-to/deploy-instance.md#keep-the-data-when-the-resource-is-deleted).
