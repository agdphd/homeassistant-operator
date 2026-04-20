# Testing

## Philosophy

**Test behavior, not implementation.** Focus on what the code does, not how it does it.
Quality over coverage — 50% coverage with meaningful tests beats 90% coverage with trivial tests.
Every reconciliation test should verify idempotent behavior.

**What NOT to test:**

- Kubernetes API behaviors (tested by k8s upstream)
- controller-runtime internals (tested by upstream)
- Simple getters/setters without logic
- Generated code (`zz_generated.deepcopy.go`)

## Test Pyramid

```
              ┌─────────────┐
              │    E2E      │  ← One critical path (all CRDs, sequential)
              ├─────────────┤
              │    Unit     │  ← Controller logic + pure functions (envtest)
              └─────────────┘
```

**Golden rule**: if you can test it with envtest, don't use E2E.

---

## Unit Tests (envtest)

**Location**: `internal/controller/*_test.go`
**Framework**: Ginkgo v2 + Gomega + envtest (fake API server, no real cluster)

```bash
make test                                          # All unit tests
go test ./internal/controller -run TestName -v    # Specific test
```

### Mock HA API pattern

Controllers for Automation/Scene/Script/Integration have a `NewHAClient` field for dependency injection. Tests replace it with an `httptest.Server`:

```go
mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    // handle PUT /api/config/automation/config/...
    w.WriteHeader(http.StatusOK)
}))

reconciler = &HomeAssistantAutomationReconciler{
    NewHAClient: func(_ string) *haclient.Client {
        return haclient.NewClient(mockServer.URL)
    },
}
```

### Key patterns

**Eventually** — async state assertions:
```go
Eventually(func(g Gomega) {
    resource := &hav1alpha1.HomeAssistantAutomation{}
    g.Expect(k8sClient.Get(ctx, key, resource)).To(Succeed())
    g.Expect(resource.Status.Ready).To(BeTrue())
}, timeout, interval).Should(Succeed())
```

**Consistently** — assert nothing changed:
```go
Consistently(func(g Gomega) {
    sts := &appsv1.StatefulSet{}
    g.Expect(k8sClient.Get(ctx, stsKey, sts)).To(Succeed())
    _, hasHash := sts.Spec.Template.Annotations["ha.homeassistant.io/secrets-hash"]
    g.Expect(hasHash).To(BeFalse())
}, time.Second*2, interval).Should(Succeed())
```

**Two-phase** — detect changes across reconcile calls:
```go
// Phase 1: create, capture initial hash
reconciler.Reconcile(ctx, req)
var initialHash string
Eventually(func(g Gomega) {
    g.Expect(k8sClient.Get(ctx, key, resource)).To(Succeed())
    initialHash = resource.Status.ConfigHash
    g.Expect(initialHash).NotTo(BeEmpty())
}, timeout, interval).Should(Succeed())

// Phase 2: update, verify hash changed
Eventually(func() error {
    Expect(k8sClient.Get(ctx, key, resource)).To(Succeed())
    resource.Spec.Configuration = "updated: true"
    return k8sClient.Update(ctx, resource)
}, timeout, interval).Should(Succeed())
reconciler.Reconcile(ctx, req)

Eventually(func(g Gomega) {
    g.Expect(k8sClient.Get(ctx, key, resource)).To(Succeed())
    g.Expect(resource.Status.ConfigHash).NotTo(Equal(initialHash))
}, timeout, interval).Should(Succeed())
```

---

## E2E Tests

**Location**: `test/e2e/e2e_critical_path_test.go`
**Framework**: Ginkgo v2 + real k3d cluster
**Strategy**: One ordered suite, one bootstrap, one `It` block per CRD

The entire E2E suite shares a single Home Assistant bootstrap (~30 min). Tests run sequentially (`Ordered`) and continue even if one fails (`ContinueOnFailure`) so later CRDs are still exercised.

### Running E2E

```bash
make test-e2e    # Creates fresh k3d cluster (12 GB RAM), runs all tests, tears down (~40-50 min)
```

Under the hood:
1. `make setup-test-e2e` — deletes any existing cluster, creates a fresh one, imports HA image
2. Ginkgo runs the suite with `--timeout=60m`
3. `make cleanup-test-e2e` — tears down the cluster regardless of result

!!! warning "Memory requirement"
    `make test-e2e` requires **12 GB RAM** for the k3d cluster. On machines with less memory:
    ```bash
    K3D_MEMORY_E2E=4g make test-e2e
    ```

### Critical path tests (11 tests)

| # | CRD | What is verified |
|---|-----|-----------------|
| 1 | `HomeAssistant` | Pod running, Service created, bootstrap completed |
| 2 | `HomeAssistantConfiguration` | ConfigMap generated, hot-reload on config change |
| 3 | `HomeAssistantSecrets` | Secret aggregated, hash annotation set |
| 4 | `HomeAssistantAutomation` | PUT to REST API, reload, DELETE via finalizer |
| 5 | `HomeAssistantScene` | PUT to REST API, reload, DELETE via finalizer |
| 6 | `HomeAssistantScript` | PUT to REST API, reload, DELETE via finalizer |
| 7 | `HomeAssistantIntegration` | Config Flow started, `entryID` stored in status |
| 8 | `HomeAssistantFloor` | Created via WebSocket registry API, deleted |
| 9 | `HomeAssistantLabel` | Created via WebSocket registry API, deleted |
| 10 | `HomeAssistantArea` | Created via WebSocket registry API, deleted |
