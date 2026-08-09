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
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// siblingDescriptor is the minimal shape needed to detect an identifier
// collision between two resources of the same Kind: shared by the
// HomeAssistantAutomation/Scene/Script webhooks, which otherwise List and
// type-convert their own distinct *List types before calling
// findIdentifierCollision.
type siblingDescriptor struct {
	Name                 string
	UID                  types.UID
	DeletionTimestamp    *metav1.Time
	HomeAssistantRefName string
	EffectiveID          string
}

// effectiveID returns id if non-empty, otherwise name — the same fallback
// already applied by this repo's Automation/Scene/Script reconcilers when
// writing into Home Assistant's config files, so the collision check
// compares exactly what would actually collide there.
func effectiveID(id, name string) string {
	if id != "" {
		return id
	}
	return name
}

// findIdentifierCollision returns the name of the first sibling colliding
// with (selfUID, selfHomeAssistantRef, selfEffectiveID) — same target
// HomeAssistant instance, same effective identifier, not itself, and not
// already marked for deletion — or "" when there is no collision.
func findIdentifierCollision(
	siblings []siblingDescriptor, selfUID types.UID, selfHomeAssistantRef, selfEffectiveID string,
) string {
	for _, s := range siblings {
		if s.UID == selfUID {
			continue
		}
		if s.DeletionTimestamp != nil {
			continue
		}
		if s.HomeAssistantRefName != selfHomeAssistantRef {
			continue
		}
		if s.EffectiveID != selfEffectiveID {
			continue
		}
		return s.Name
	}
	return ""
}

// checkIdentifierCollision is the one piece of logic shared verbatim by the
// HomeAssistantAutomation/Scene/Script collision webhooks: List siblings of
// the given Kind via list (a pointer to that Kind's own *XList, e.g.
// &hav1.HomeAssistantAutomationList{}), convert them to descriptors via
// toDescriptors, and return a formatted error naming the conflicting sibling
// if the incoming object's effective id collides with one of them. A List
// failure is logged and treated as "no collision found" rather than
// rejected — the collision check is best-effort, consistent with
// failurePolicy=ignore on the webhook itself.
func checkIdentifierCollision[L client.ObjectList](
	ctx context.Context, cl client.Reader, namespace string, list L, toDescriptors func(L) []siblingDescriptor,
	kind string, selfUID types.UID, selfName, selfHomeAssistantRef, selfEffectiveID string,
) error {
	if err := cl.List(ctx, list, client.InNamespace(namespace)); err != nil {
		logf.Log.WithName(kind+"-webhook").Error(
			err, "failed to list siblings for collision check", "kind", kind, "name", selfName)
		return nil
	}

	conflict := findIdentifierCollision(toDescriptors(list), selfUID, selfHomeAssistantRef, selfEffectiveID)
	if conflict == "" {
		return nil
	}
	return fmt.Errorf(
		"%s %q has effective id %q, which collides with existing %s %q for HomeAssistant %q",
		kind, selfName, selfEffectiveID, kind, conflict, selfHomeAssistantRef)
}
