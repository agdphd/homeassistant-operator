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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	hav1 "github.com/przemekhys/homeassistant-operator/api/v1"
)

// SetupHomeAssistantScriptWebhookWithManager registers the validating
// webhook for the HomeAssistantScript kind. It rejects a create/update whose
// effective identifier (spec.id, or metadata.name when unset) collides with
// a sibling HomeAssistantScript targeting the same HomeAssistant instance —
// without this, two colliding resources silently overwrite each other in
// Home Assistant's scripts.yaml while both keep reporting Ready.
func SetupHomeAssistantScriptWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &hav1.HomeAssistantScript{}).
		WithValidator(&HomeAssistantScriptCustomValidator{Client: mgr.GetClient()}).
		Complete()
}

// failurePolicy=ignore: a temporarily unavailable webhook (e.g. operator
// restart) never blocks create/update — this collision check is best-effort,
// matching the existing HomeAssistant webhook's own failure policy.
// +kubebuilder:webhook:path=/validate-ha-homeassistant-io-v1-homeassistantscript,mutating=false,failurePolicy=ignore,sideEffects=None,groups=ha.homeassistant.io,resources=homeassistantscripts,verbs=create;update,versions=v1,name=vhomeassistantscript-v1.kb.io,admissionReviewVersions=v1

// HomeAssistantScriptCustomValidator validates HomeAssistantScript resources
// on admission. Client is the manager's cached client — see the equivalent
// comment on HomeAssistantAutomationCustomValidator for why this Kind's warm
// reconciler-watch cache is used instead of an uncached reader.
type HomeAssistantScriptCustomValidator struct {
	Client client.Reader
}

var _ admission.Validator[*hav1.HomeAssistantScript] = &HomeAssistantScriptCustomValidator{}

func (v *HomeAssistantScriptCustomValidator) ValidateCreate(
	ctx context.Context, script *hav1.HomeAssistantScript,
) (admission.Warnings, error) {
	return nil, v.validate(ctx, script)
}

func (v *HomeAssistantScriptCustomValidator) ValidateUpdate(
	ctx context.Context, _, newObj *hav1.HomeAssistantScript,
) (admission.Warnings, error) {
	return nil, v.validate(ctx, newObj)
}

func (v *HomeAssistantScriptCustomValidator) ValidateDelete(
	_ context.Context, _ *hav1.HomeAssistantScript,
) (admission.Warnings, error) {
	return nil, nil
}

func (v *HomeAssistantScriptCustomValidator) validate(ctx context.Context, script *hav1.HomeAssistantScript) error {
	return checkIdentifierCollision(ctx, v.Client, script.Namespace, &hav1.HomeAssistantScriptList{},
		func(l *hav1.HomeAssistantScriptList) []siblingDescriptor {
			descriptors := make([]siblingDescriptor, 0, len(l.Items))
			for _, s := range l.Items {
				descriptors = append(descriptors, siblingDescriptor{
					Name:                 s.Name,
					UID:                  s.UID,
					DeletionTimestamp:    s.DeletionTimestamp,
					HomeAssistantRefName: s.Spec.HomeAssistantRef.Name,
					EffectiveID:          effectiveID(s.Spec.ID, s.Name),
				})
			}
			return descriptors
		},
		"HomeAssistantScript", script.UID, script.Name,
		script.Spec.HomeAssistantRef.Name, effectiveID(script.Spec.ID, script.Name))
}
