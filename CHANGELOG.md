# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).


## [Unreleased]

### Added

- **HomeAssistantFloor CRD** (`hafloor`, `hafl`) — declarative management of Home Assistant floors via WebSocket registry API (`config/floor_registry/*`). Supports `name`, `level`, and `icon`. Adopts existing floors by name, finalizer-based cleanup on deletion.

- **HomeAssistantLabel CRD** (`halabel`, `halb`) — declarative management of Home Assistant labels via WebSocket registry API (`config/label_registry/*`). Supports `name`, `icon`, and `color`. Adopts existing labels by name, finalizer-based cleanup on deletion.

- **HomeAssistantArea CRD** (`haarea`, `haar`) — declarative management of Home Assistant areas via WebSocket registry API (`config/area_registry/*`). Supports `name`, `icon`, `floorName` (resolved to `floor_id` at reconcile time), and `labels[]` (resolved to `label_ids`). Requeues with `FloorNotFound` if referenced floor doesn't exist; missing labels produce a warning but don't block creation.

- **`SendWebSocketCommand` helper in haclient** — reusable one-shot WebSocket command pattern (connect → auth → command → response → close) used by Floor/Label/Area controllers. Refactored `CreateLongLivedToken` to use the same helper.

- **`spec.backup` for HomeAssistant CR** — declarative configuration of Home Assistant's built-in backup system via WebSocket API (`backup/config/info`, `backup/config/update`). Supports schedule (daily, per-day-of-week, never), time, retention (copies/days), and database inclusion. Requires bootstrap with API token enabled.
  - Idempotent: compares current HA config with desired state, only updates when drift detected
  - Condition: `BackupConfigured` with reasons: `BackupConfigured`, `BackupConfigFailed`, `TokenNotAvailable`
  - Events: `BackupConfigured` (Normal), `BackupConfigFailed` (Warning)

### Fixed

- **`!secret` YAML tags stripped during location injection** — `injectLocation` used `map[string]interface{}` for YAML round-trip, which discards custom tags like `!secret` and `!include`. Switched to `yaml.Node` tree which preserves all YAML tags through the unmarshal/marshal cycle. Only affected configs with `spec.bootstrap.location` set.

- **Bootstrap missing `integration` onboarding step** — `PerformBootstrap()` only completed 3 of 4 required HA onboarding steps (`user`, `core_config`, `analytics`), leaving out `integration`. This caused non-admin users to be blocked from accessing HA's websocket API and redirected to `/onboarding.html`.

- **Config Flow `description` as object** — `FlowField.Description` was typed as `*string`, but some integrations (e.g. OpenWeatherMap) return it as an object (`{"suggested_value": "..."}`), causing JSON unmarshal errors. Changed to `json.RawMessage` to accept both formats.

- **Enable automation 404 on HA 2025.x+** — the operator called `POST /api/config/automation/config/{id}/enable` after every PUT, but this endpoint no longer exists in HA 2025.x/2026.x. Automations created via API are enabled by default. Removed the `EnableAutomation` call; only `DisableAutomation` is used when `spec.enabled: false`.

### Changed

- Bump `k8s.io/api`, `k8s.io/apimachinery`, `k8s.io/client-go` from v0.35.2 to v0.35.3

## [v0.7.1] - 2026-03-19

### Fixed

- **`auto_include` strips `!include` tags after YAML round-trip** — when `injectLocation` re-serialised `configuration.yaml`, the `!include` YAML tag was lost (e.g. `automation: !include automations.yaml` became `automation: automations.yaml`). HA treated the bare filename as a literal string and disabled all automations. `ensureAutoIncludes` now detects bare filenames and restores the `!include` directive in-place.

## [v0.7.0] - 2026-03-17

### Added

- **HomeAssistantIntegration CRD** (`haint`) — declarative management of Home Assistant integrations via Config Flow API. Does not deploy containers — only registers integrations in HA.
  - Supports single-step Config Flows (MQTT, ESPHome, and others)
  - `spec.configuration` fields support plain text values and `secretKeyRef` references to K8s Secrets
  - **Adopt pattern**: if integration already exists in HA (e.g. configured via UI), operator adopts the existing `entryID` without reconfiguring
  - **Day-2 reconfiguration**: changing `spec.configuration` triggers delete + re-create of the config entry
  - Finalizer-based cleanup: removes config entry from HA on CR deletion (best-effort)
  - Events: `IntegrationConfigured`, `IntegrationAdopted`, `IntegrationReconfigured`, `IntegrationRemoved`, `IntegrationFailed`
  - Condition: `IntegrationReady` with reasons: `IntegrationConfigured`, `AlreadyConfigured`, `TokenNotAvailable`, `HANotReady`, `ConfigFlowFailed`, `SecretResolutionFailed`
- **`spec.hostNetwork` for HomeAssistant CR** — enables host networking for IoT device discovery (mDNS/SSDP/DHCP). When enabled, sets `hostNetwork: true` and `dnsPolicy: ClusterFirstWithHostNet` on the pod.
- **Config Entry Flow API methods in haclient** — `ListConfigEntries`, `IsIntegrationConfigured`, `StartConfigFlow`, `SubmitConfigFlow`, `SubmitConfigFlowUntilDone`, `RemoveConfigEntry`.

### Fixed

- **Auto-inject `!include` directives** — the operator now automatically appends `automation: !include automations.yaml`, `scene: !include scenes.yaml`, and `script: !include scripts.yaml` to `configuration.yaml` if not already present. HA 2025.x requires explicit includes for PVC-managed files.

- **Recovery mode on first start** (`spec.storage.initContainer`) — HA was entering recovery mode with `Unable to read file /config/automations.yaml` because `auto_include.go` injects `!include automations.yaml` unconditionally, but the files are created by the automation/scene/script controller only after the pod is already running. An init container (`busybox` by default) now pre-creates empty `[]` files (`automations.yaml`, `scenes.yaml`, `scripts.yaml`) on the PVC before the main container starts. Image, tag, and repository are configurable via `spec.storage.initContainer`.


- **Location not set after bootstrap** — `SetCoreConfig` during the onboarding flow was silently ignored (HA returns an error when the endpoint is not yet ready), leaving `zone.home` at `latitude: 0, longitude: 0`. The configuration controller now injects `latitude`, `longitude`, `elevation`, `unit_system`, and `time_zone` into the `homeassistant:` section of `configuration.yaml` (only for keys not already defined by the user). The correct location is applied on the first configuration reconcile via hot-reload or restart.

### Removed

- **BREAKING CHANGE: HomeAssistantAddon CRD removed** — `HomeAssistantAddon` (`haad`) has been completely removed. Use Helm charts or standard Kubernetes resources (Deployment, Service, PVC) to deploy companion services like Mosquitto, MariaDB, or Node-RED. Use the new `HomeAssistantIntegration` CRD (`haint`) to register integrations declaratively.

## [0.6.0] - 2026-03-09

### Added

- **HomeAssistantAutomation / Scene / Script: individual management via HA REST API** — each CR is now managed individually via `POST /api/config/{type}/config/{id}` (create/update) and `DELETE /api/config/{type}/config/{id}` (removal). Home Assistant writes directly to `automations.yaml` / `scenes.yaml` / `scripts.yaml` on the PVC. The old ConfigMap aggregation approach (`<ha-name>-automations`, `<ha-name>-scenes`, `<ha-name>-scripts`) has been removed.
  - Status condition `ReloadReady` reflects the last API call result
  - When the bootstrap token is not yet available, the controller requeues with backoff (30s) and sets `ReasonTokenNotAvailable`
  - Deletion via finalizer calls DELETE to HA API (best-effort — continues even when HA is unavailable)

### Migration (v0.5.x → v0.6.0)

> **Note for existing deployments**: when upgrading from v0.5.x to v0.6.0, the operator will automatically remove the old aggregation ConfigMaps (`<ha-name>-automations`, `<ha-name>-scenes`, `<ha-name>-scripts`) and their volume mounts from the Home Assistant StatefulSet. **The HA pod will be restarted once** during this migration. After restart, existing automations/scenes/scripts CRs will be re-synced to HA via the REST API.

## [0.5.1] - 2026-03-07

### Fixed

- **HomeAssistantConfiguration: restart not triggered after adding new integration (e.g. `prometheus:`)** — when a reconcile attempt updated the ConfigMap content but failed before saving status, a subsequent retry would read `oldConfig == newConfig` and incorrectly choose hot-reload instead of restart. The controller now defaults to restart when the ConfigMap is already synced but status hash is stale.

- **HomeAssistantAddon mosquitto profile: conflict with Flux GitOps and HA 2025.x incompatibility** — the mosquitto profile was writing `mqtt: broker: ...` to `HomeAssistantConfiguration` CR on every reconcile. HA 2025.x dropped support for the `broker` key in `configuration.yaml` (returns `'broker' is an invalid option`), and Flux GitOps would immediately revert the change, causing an infinite reconcile loop. The `HAIntegration` has been removed from the mosquitto profile. Configure the MQTT broker via the Home Assistant UI: **Settings → Integrations → MQTT**. Automatic setup via Config Flow API is planned in Phase 6.


## [0.5.0] - 2026-03-01

### Added

- **HomeAssistantAddon CRD**: Declarative addon management for Home Assistant
  - Profile system with built-in profiles: `mosquitto`, `mariadb`, `node-red` with sensible defaults
  - User overrides: user-provided fields take priority over profile defaults
  - Automatic Home Assistant integration (`spec.haIntegration`) — adds integration section to HomeAssistantConfiguration CR
  - Auto-provisioning of K8s resources: Deployment/StatefulSet, Service, PVC, ConfigMap, Ingress
  - Configuration via ConfigMap (`spec.config`) — mount configuration files into the addon container
  - Finalizer-based cleanup — removes integration section from HomeAssistantConfiguration on CR deletion
  - Status tracking: phase (Pending/Running/Failed), resolvedImage, workloadType, serviceName
  - Short names: `haaddon`, `haad`


## [0.4.0] - 2026-02-21

### Added

- **Prometheus metrics for hot-reload operations** — three new domain-specific metrics exposed at `/metrics`:
  - `homeassistant_reload_total{component, result}` — counter per component (automation/scene/script) and result (success/failed/skipped)
  - `homeassistant_reload_duration_seconds{component}` — histogram (buckets: 0.5s–30s) for reload latency percentiles
  - `homeassistant_reload_retries_total{component}` — extra retry attempts beyond the first; non-zero value indicates the reload required more than one attempt
  - All data sourced from existing `ReloadResult` fields — no additional API calls required

- **HomeAssistantAutomation CRD**: Declarative automation management with hot-reload capabilities
  - Full automation definition via CRD with triggers, conditions, and actions
  - Uses `runtime.RawExtension` for flexible YAML compatibility with Home Assistant syntax
  - Aggregates multiple automation CRs into single ConfigMap (`<name>-automations`)
  - Finalizer-based deletion: regenerates ConfigMap without removed automation before CR deletion
  - Enable/disable without deletion via `spec.enabled` field
  - Short names: `haautomation`, `haauto`

- **HomeAssistantScene CRD**: Declarative scene management for Home Assistant
  - Aggregation pattern - multiple CR instances → single `scenes.yaml`
  - Entity validation with pattern regex (`domain.object_id`)
  - Flexible entity attributes support via `runtime.RawExtension`
  - Short names: `hascene`, `hasc`
  - Status tracking: Ready, LastActivated, LastReloadTime
  - Finalizer-based cleanup - regenerates ConfigMap without deleted scene
  - Auto-reload control via `spec.autoReload` (default: true)

- **HomeAssistantScript CRD**: Declarative script management for Home Assistant
  - Aggregation pattern - multiple CR instances → single `scripts.yaml`
  - Flexible sequence definition via `runtime.RawExtension`
  - Input parameters support via `spec.fields` map
  - Short names: `hascript`, `hascp`
  - Status tracking: Ready, LastReloadTime, LastReloadMethod
  - Finalizer-based cleanup - regenerates ConfigMap without deleted script
  - Auto-reload control via `spec.autoReload` (default: true)


## [0.3.0] - 2026-01-27

### Added

- **HomeAssistantConfiguration CRD**: Declarative configuration management with intelligent hot-reload capabilities
  - Full `configuration.yaml` management via `spec.configuration` field
  - Smart reload strategy: automatically determines if changes require restart or can be hot-reloaded
  - Zero-downtime updates for reloadable sections (automations, scripts, logger, input helpers, etc.)
  - Three reload strategies: `auto` (default, analyzes changes), `hot-reload` (force REST API), `restart` (force pod restart)
  - Requires bootstrap-generated API token for hot-reload functionality
  - Short names: `haconfig`, `hacfg`

### Changed

- **BREAKING CHANGE**: HomeAssistantConfiguration CRD now REQUIRED for every HomeAssistant instance
  - Every `HomeAssistant` CR must have a corresponding `HomeAssistantConfiguration` CR
  - Controller validates HomeAssistantConfiguration exists before creating StatefulSet
  - ConfigMap auto-generated from HomeAssistantConfiguration spec (pattern: `<name>-configuration`)
- **BREAKING CHANGE**: PVC naming convention changed from `<name>-config` to `<name>-data`
  - The PersistentVolumeClaim for Home Assistant data storage now uses the suffix `-data` instead of `-config`


## [0.2.0] - 2026-01-11

### Added

- **Zero-Touch Bootstrap**: Automatic Home Assistant onboarding without manual UI interaction
  - Creates admin user with credentials from Kubernetes Secret
  - Configures location, timezone, units, and currency (`spec.bootstrap.location`)
  - Sets analytics preferences (`spec.bootstrap.analytics`)
  - Generates long-lived API token via WebSocket API
  - Stores API token in Kubernetes Secret for programmatic access
- **HomeAssistantSecrets CRD**: Declarative secrets management for Home Assistant
  - References existing Kubernetes Secrets
  - Auto-generates `secrets.yaml` file
  - Automatic pod restart on secret changes (configurable via `spec.autoRestart`)
- **New haclient package**: Native Go HTTP/WebSocket client for Home Assistant API

### Changed

- Service naming simplified: now uses `<name>` instead of `<name>-homeassistant`


## [0.1.0] - 2026-01-06

### Added

- Initial release of Home Assistant Operator
- `HomeAssistant` Custom Resource Definition (CRD) with support for:
  - Version/image configuration (`spec.version`, `spec.image`)
  - Storage configuration with PVC (`spec.storage`)
  - Service configuration (ClusterIP, NodePort, LoadBalancer) (`spec.service`)
  - Ingress configuration with TLS support (`spec.ingress`)
  - Resource limits and requests (`spec.resources`)
  - Timezone configuration (`spec.timezone`)
  - External ConfigMap for `configuration.yaml` (`spec.configurationFrom`)
  - External Secret for `secrets.yaml` (`spec.secretsFrom`)
- Reconciliation controller that manages:
  - StatefulSet for Home Assistant deployment
  - PersistentVolumeClaim for data storage
  - Service for network access
  - Ingress for external access (optional)
- Health checks (liveness and readiness probes)
- Status reporting with phase, conditions, and ready state
- Multi-architecture Docker images (amd64, arm64)
- CI/CD with GitHub Actions (lint, test, e2e tests)
- k3d support for local testing

### Target Environments

- Primary: k3s on Raspberry Pi 4/5 (ARM64)
- Also supported: Any Kubernetes cluster (AMD64/ARM64)

[Unreleased]: https://github.com/przemekhys/homeassistant-operator/compare/v0.7.1...HEAD
[v0.7.1]: https://github.com/przemekhys/homeassistant-operator/compare/v0.7.0...v0.7.1
[v0.7.0]: https://github.com/przemekhys/homeassistant-operator/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/przemekhys/homeassistant-operator/releases/tag/v0.6.0
[0.5.1]: https://github.com/przemekhys/homeassistant-operator/releases/tag/v0.5.1
[0.5.0]: https://github.com/przemekhys/homeassistant-operator/releases/tag/v0.5.0
[0.4.0]: https://github.com/przemekhys/homeassistant-operator/releases/tag/v0.4.0
[0.3.0]: https://github.com/przemekhys/homeassistant-operator/releases/tag/v0.3.0
[0.2.0]: https://github.com/przemekhys/homeassistant-operator/releases/tag/v0.2.0
[0.1.0]: https://github.com/przemekhys/homeassistant-operator/releases/tag/v0.1.0
