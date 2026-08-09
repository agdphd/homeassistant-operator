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

// SetupHomeAssistantAutomationWebhookWithManager registers the validating
// webhook for the HomeAssistantAutomation kind. It rejects a create/update
// whose effective identifier (spec.id, or metadata.name when unset) collides
// with a sibling HomeAssistantAutomation targeting the same HomeAssistant
// instance — without this, two colliding resources silently overwrite each
// other in Home Assistant's automations.yaml while both keep reporting
// Ready.
func SetupHomeAssistantAutomationWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &hav1.HomeAssistantAutomation{}).
		WithValidator(&HomeAssistantAutomationCustomValidator{Client: mgr.GetClient()}).
		Complete()
}

// failurePolicy=ignore: a temporarily unavailable webhook (e.g. operator
// restart) never blocks create/update — this collision check is best-effort,
// matching the existing HomeAssistant webhook's own failure policy.
// +kubebuilder:webhook:path=/validate-ha-homeassistant-io-v1-homeassistantautomation,mutating=false,failurePolicy=ignore,sideEffects=None,groups=ha.homeassistant.io,resources=homeassistantautomations,verbs=create;update,versions=v1,name=vhomeassistantautomation-v1.kb.io,admissionReviewVersions=v1

// HomeAssistantAutomationCustomValidator validates HomeAssistantAutomation
// resources on admission. Client is the manager's cached client
// (mgr.GetClient()), deliberately not the uncached APIReader
// homeassistant_webhook.go's PriorityClass check uses: the manager already
// runs a continuous watch on this Kind for the existing reconciler, so the
// cache is warm and kept current by the same mechanism reconciliation
// already relies on — an uncached read here would add avoidable API server
// load on a Kind with materially more create/update traffic than
// PriorityClass.
type HomeAssistantAutomationCustomValidator struct {
	Client client.Reader
}

var _ admission.Validator[*hav1.HomeAssistantAutomation] = &HomeAssistantAutomationCustomValidator{}

func (v *HomeAssistantAutomationCustomValidator) ValidateCreate(
	ctx context.Context, automation *hav1.HomeAssistantAutomation,
) (admission.Warnings, error) {
	return nil, v.validate(ctx, automation)
}

func (v *HomeAssistantAutomationCustomValidator) ValidateUpdate(
	ctx context.Context, _, newObj *hav1.HomeAssistantAutomation,
) (admission.Warnings, error) {
	return nil, v.validate(ctx, newObj)
}

func (v *HomeAssistantAutomationCustomValidator) ValidateDelete(
	_ context.Context, _ *hav1.HomeAssistantAutomation,
) (admission.Warnings, error) {
	return nil, nil
}

func (v *HomeAssistantAutomationCustomValidator) validate(
	ctx context.Context, automation *hav1.HomeAssistantAutomation,
) error {
	return checkIdentifierCollision(ctx, v.Client, automation.Namespace, &hav1.HomeAssistantAutomationList{},
		func(l *hav1.HomeAssistantAutomationList) []siblingDescriptor {
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
		"HomeAssistantAutomation", automation.UID, automation.Name,
		automation.Spec.HomeAssistantRef.Name, effectiveID(automation.Spec.ID, automation.Name))
}
