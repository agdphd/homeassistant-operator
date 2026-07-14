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

package haclient

import (
	"net/http"
	"testing"
)

// TestWithRootCAs verifies the native-TLS trust plumbing: a CA pool is installed
// on the transport and certificate verification is never disabled.
func TestWithRootCAs(t *testing.T) {
	c := NewClient("https://home.default.svc.cluster.local:8123").
		WithRootCAs([]byte("-----BEGIN CERTIFICATE-----\ninvalid\n-----END CERTIFICATE-----"))

	tr, ok := c.httpClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", c.httpClient.Transport)
	}
	if tr.TLSClientConfig == nil {
		t.Fatal("expected TLSClientConfig to be configured")
	}
	if tr.TLSClientConfig.RootCAs == nil {
		t.Fatal("expected RootCAs pool to be set")
	}
	if tr.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("InsecureSkipVerify must never be enabled for native TLS")
	}
}
