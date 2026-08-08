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
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hav1 "github.com/przemekhys/homeassistant-operator/api/v1"
)

func newAutomationValidator(t *testing.T, objs ...client.Object) *HomeAssistantAutomationCustomValidator {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := hav1.AddToScheme(scheme); err != nil {
		t.Fatalf("add hav1: %v", err)
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	return &HomeAssistantAutomationCustomValidator{Client: cl}
}

func automation(name, id, haRef string) *hav1.HomeAssistantAutomation {
	return &hav1.HomeAssistantAutomation{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", UID: types.UID(name)},
		Spec: hav1.HomeAssistantAutomationSpec{
			HomeAssistantRef: hav1.HomeAssistantReference{Name: haRef},
			ID:               id,
			Alias:            name,
		},
	}
}

func TestHomeAssistantAutomationCustomValidator_Validate(t *testing.T) {
	t.Run("no siblings admits", func(t *testing.T) {
		v := newAutomationValidator(t)
		if _, err := v.ValidateCreate(context.Background(), automation("first", "foo", "home")); err != nil {
			t.Errorf("unexpected rejection: %v", err)
		}
	})

	t.Run("explicit id collision with existing sibling is rejected and names it", func(t *testing.T) {
		existing := automation("first-automation", "morning_lights", "home")
		v := newAutomationValidator(t, existing)
		incoming := automation("second-automation", "morning_lights", "home")
		_, err := v.ValidateCreate(context.Background(), incoming)
		if err == nil {
			t.Fatal("expected rejection, got nil error")
		}
		if !strings.Contains(err.Error(), "first-automation") {
			t.Errorf("error %q does not name the conflicting sibling %q", err.Error(), "first-automation")
		}
	})

	t.Run("name-fallback id collision is rejected", func(t *testing.T) {
		// First resource omits spec.id, so its effective id is its own
		// metadata.name ("sharedname"). Second resource sets spec.id
		// explicitly to that same value — the check must catch this by
		// comparing effective ids, not just the raw spec.id field.
		existing := automation("sharedname", "", "home")
		v := newAutomationValidator(t, existing)
		incoming := automation("second-automation", "sharedname", "home")
		_, err := v.ValidateCreate(context.Background(), incoming)
		if err == nil {
			t.Fatal("expected rejection, got nil error")
		}
		if !strings.Contains(err.Error(), "sharedname") {
			t.Errorf("error %q does not reference the colliding effective id", err.Error())
		}
	})

	t.Run("same id, different HomeAssistant instance admits", func(t *testing.T) {
		existing := automation("first", "foo", "home")
		v := newAutomationValidator(t, existing)
		incoming := automation("second", "foo", "away")
		if _, err := v.ValidateCreate(context.Background(), incoming); err != nil {
			t.Errorf("unexpected rejection across different instances: %v", err)
		}
	})

	t.Run("sibling marked for deletion is not a conflict", func(t *testing.T) {
		existing := automation("first", "foo", "home")
		now := metav1.Now()
		existing.DeletionTimestamp = &now
		existing.Finalizers = []string{"ha.homeassistant.io/automation"}
		v := newAutomationValidator(t, existing)
		incoming := automation("second", "foo", "home")
		if _, err := v.ValidateCreate(context.Background(), incoming); err != nil {
			t.Errorf("unexpected rejection against a sibling pending deletion: %v", err)
		}
	})

	t.Run("update does not conflict with its own prior state", func(t *testing.T) {
		self := automation("first", "foo", "home")
		v := newAutomationValidator(t, self)
		if _, err := v.ValidateUpdate(context.Background(), self, self); err != nil {
			t.Errorf("unexpected self-conflict on update: %v", err)
		}
	})
}
