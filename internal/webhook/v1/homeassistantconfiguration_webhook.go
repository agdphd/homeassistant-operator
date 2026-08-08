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

package v1

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	hav1 "github.com/przemekhys/homeassistant-operator/api/v1"
)

// SetupHomeAssistantConfigurationWebhookWithManager registers the validating
// webhook for the HomeAssistantConfiguration kind. It never rejects — it only
// warns when spec.recorder sets both database and databaseSecretRef, so the
// pre-existing, already-documented precedence (databaseSecretRef wins, see
// resolveRecorderDB in the controller) is visible at apply time instead of
// silently invoked.
func SetupHomeAssistantConfigurationWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &hav1.HomeAssistantConfiguration{}).
		WithValidator(&HomeAssistantConfigurationCustomValidator{}).
		Complete()
}

// failurePolicy=ignore, matching every other webhook in this package — this
// validator never rejects, so a temporarily unavailable webhook only means a
// missed warning, never a blocked apply.
// +kubebuilder:webhook:path=/validate-ha-homeassistant-io-v1-homeassistantconfiguration,mutating=false,failurePolicy=ignore,sideEffects=None,groups=ha.homeassistant.io,resources=homeassistantconfigurations,verbs=create;update,versions=v1,name=vhomeassistantconfiguration-v1.kb.io,admissionReviewVersions=v1

// HomeAssistantConfigurationCustomValidator validates HomeAssistantConfiguration
// resources on admission. A pure function over the spec — no cluster access needed.
type HomeAssistantConfigurationCustomValidator struct{}

var _ admission.Validator[*hav1.HomeAssistantConfiguration] = &HomeAssistantConfigurationCustomValidator{}

func (v *HomeAssistantConfigurationCustomValidator) ValidateCreate(
	_ context.Context, config *hav1.HomeAssistantConfiguration,
) (admission.Warnings, error) {
	return validateRecorderPrecedence(&config.Spec), nil
}

func (v *HomeAssistantConfigurationCustomValidator) ValidateUpdate(
	_ context.Context, _, newObj *hav1.HomeAssistantConfiguration,
) (admission.Warnings, error) {
	return validateRecorderPrecedence(&newObj.Spec), nil
}

func (v *HomeAssistantConfigurationCustomValidator) ValidateDelete(
	_ context.Context, _ *hav1.HomeAssistantConfiguration,
) (admission.Warnings, error) {
	return nil, nil
}

// validateRecorderPrecedence warns when spec.recorder sets both database and
// databaseSecretRef, naming which one actually takes effect (resolveRecorderDB
// in the controller always prefers databaseSecretRef). Kept as a pure function
// over the spec for straightforward unit testing, mirroring
// validateHomeAssistantTLS in homeassistant_webhook.go.
func validateRecorderPrecedence(spec *hav1.HomeAssistantConfigurationSpec) admission.Warnings {
	rec := spec.Recorder
	if rec == nil || rec.Database == "" || rec.DatabaseSecretRef == nil {
		return nil
	}
	return admission.Warnings{
		"spec.recorder: both database and databaseSecretRef are set — databaseSecretRef takes effect, database is ignored",
	}
}
