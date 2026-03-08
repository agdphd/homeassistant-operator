/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

// Shared constants used across multiple controllers

const (
	// Annotations
	// configHashAnnotationKey - Used by both HomeAssistant and
	// HomeAssistantConfiguration controllers
	configHashAnnotationKey = "ha.homeassistant.io/config-hash"

	// Reload method names for status tracking
	// Used by Configuration and Automation controllers
	reloadMethodRestart   = "restart"
	reloadMethodHotReload = "hot-reload"
	reloadMethodNone      = "none"

	// Home Assistant defaults
	// Used across multiple controllers
	defaultHomeAssistantPort = 8123
	apiTokenSecretSuffix     = "-api-token"

	// Error messages shared across controllers
	errMsgTokenNotAvailable = "API token not found - bootstrap may not be configured"

	// Condition reasons for ReloadReady
	reasonTokenNotAvailable = "TokenNotAvailable"

	// HomeAssistantAddon resource name suffixes
	addonConfigMapSuffix = "-config"
)
