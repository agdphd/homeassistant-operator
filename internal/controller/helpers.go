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

import (
	"context"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hav1alpha1 "github.com/przemekhys/homeassistant-operator/api/v1alpha1"
)

// getHomeAssistant retrieves a HomeAssistant CR by namespaced name
// This is a common helper used across multiple controllers
func getHomeAssistant(
	ctx context.Context,
	c client.Client,
	haRef types.NamespacedName,
) (*hav1alpha1.HomeAssistant, error) {
	ha := &hav1alpha1.HomeAssistant{}
	if err := c.Get(ctx, haRef, ha); err != nil {
		return nil, err
	}
	return ha, nil
}
