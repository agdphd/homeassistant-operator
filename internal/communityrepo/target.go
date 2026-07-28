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

package communityrepo

import "fmt"

// ConflictKey identifies what a HomeAssistantCommunityRepository would occupy on a
// given HomeAssistant instance: the same key from two different resources is a
// conflict, regardless of whether they reference the same or different source
// repositories.
type ConflictKey struct {
	HomeAssistantName string
	Category          Category
	ResolvedTarget    string
}

// String returns a stable, comparable representation suitable for use as a map key.
func (k ConflictKey) String() string {
	return fmt.Sprintf("%s/%s/%s", k.HomeAssistantName, k.Category, k.ResolvedTarget)
}

// NewConflictKey builds a ConflictKey from a resolved installation target.
func NewConflictKey(homeAssistantName string, category Category, r Resolved) ConflictKey {
	return ConflictKey{
		HomeAssistantName: homeAssistantName,
		Category:          category,
		ResolvedTarget:    r.ResolvedTarget,
	}
}
