# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).


## [Unreleased]

### Added

- **`spec.hostNetwork` for HomeAssistant CR** — enables host networking for IoT device discovery (mDNS/SSDP/DHCP). When enabled, sets `hostNetwork: true` and `dnsPolicy: ClusterFirstWithHostNet` on the pod.
- **Config Entry Flow API methods in haclient** — `ListConfigEntries`, `IsIntegrationConfigured`, `StartConfigFlow`, `SubmitConfigFlow`, `RemoveConfigEntry` — preparation for the upcoming `HomeAssistantIntegration` CRD.

### Fixed

- **Auto-inject `!include` directives** — the operator now automatically appends `automation: !include automations.yaml`, `scene: !include scenes.yaml`, and `script: !include scripts.yaml` to `configuration.yaml` if not already present. HA 2025.x requires explicit includes for PVC-managed files.

### Removed

- **BREAKING CHANGE: HomeAssistantAddon CRD removed** — `HomeAssistantAddon` (`haad`) has been completely removed. Use Helm charts or standard Kubernetes resources (Deployment, Service, PVC) to deploy companion services like Mosquitto, MariaDB, or Node-RED. Automatic HA integration setup will be handled by the upcoming `HomeAssistantIntegration` CRD.

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

[Unreleased]: https://github.com/przemekhys/homeassistant-operator/compare/v0.6.0...HEAD
[0.6.0]: https://github.com/przemekhys/homeassistant-operator/releases/tag/v0.6.0
[0.5.1]: https://github.com/przemekhys/homeassistant-operator/releases/tag/v0.5.1
[0.5.0]: https://github.com/przemekhys/homeassistant-operator/releases/tag/v0.5.0
[0.4.0]: https://github.com/przemekhys/homeassistant-operator/releases/tag/v0.4.0
[0.3.0]: https://github.com/przemekhys/homeassistant-operator/releases/tag/v0.3.0
[0.2.0]: https://github.com/przemekhys/homeassistant-operator/releases/tag/v0.2.0
[0.1.0]: https://github.com/przemekhys/homeassistant-operator/releases/tag/v0.1.0
