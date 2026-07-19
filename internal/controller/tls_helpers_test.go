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
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hav1 "github.com/przemekhys/homeassistant-operator/api/v1"
)

// restMapperWithCertManager returns a RESTMapper that either knows or does not
// know the cert-manager Certificate kind, to simulate cert-manager presence.
func restMapperWithCertManager(present bool) meta.RESTMapper {
	m := meta.NewDefaultRESTMapper(nil)
	if present {
		m.Add(schema.GroupVersionKind{
			Group:   certManagerGroup,
			Version: certManagerVersion,
			Kind:    certManagerKind,
		}, meta.RESTScopeNamespace)
	}
	return m
}

func newTLSTestReconciler(t *testing.T, certManagerPresent bool, objs ...client.Object) *HomeAssistantReconciler {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := hav1.AddToScheme(scheme); err != nil {
		t.Fatalf("add hav1 to scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add corev1 to scheme: %v", err)
	}
	// Register the cert-manager Certificate GVK as unstructured so the fake
	// client can create/get it (the operator uses unstructured in production too).
	scheme.AddKnownTypeWithName(certificateGVK, &unstructured.Unstructured{})
	listGVK := certificateGVK
	listGVK.Kind += "List"
	scheme.AddKnownTypeWithName(listGVK, &unstructured.UnstructuredList{})

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRESTMapper(restMapperWithCertManager(certManagerPresent)).
		WithObjects(objs...).
		WithStatusSubresource(&hav1.HomeAssistant{}).
		Build()
	return &HomeAssistantReconciler{
		Client:   cl,
		Scheme:   scheme,
		Recorder: events.NewFakeRecorder(16),
	}
}

func nativeTLSHA(name string) *hav1.HomeAssistant {
	return &hav1.HomeAssistant{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: hav1.HomeAssistantSpec{
			Alpha: &hav1.AlphaSpec{
				TLS: &hav1.TLSAlphaSpec{
					Native: &hav1.NativeTLSAlphaSpec{
						Enabled:   true,
						IssuerRef: &hav1.IssuerReference{Name: "test-issuer", Kind: "ClusterIssuer"},
					},
				},
			},
		},
	}
}

func TestCertManagerRequired(t *testing.T) {
	tests := []struct {
		name string
		ha   *hav1.HomeAssistant
		want bool
	}{
		{
			name: "no TLS requested",
			ha:   &hav1.HomeAssistant{},
			want: false,
		},
		{
			name: "native TLS enabled with issuer",
			ha:   nativeTLSHA("ha"),
			want: true,
		},
		{
			name: "native TLS enabled bring-your-own secret does not need cert-manager",
			ha: &hav1.HomeAssistant{Spec: hav1.HomeAssistantSpec{Alpha: &hav1.AlphaSpec{
				TLS: &hav1.TLSAlphaSpec{Native: &hav1.NativeTLSAlphaSpec{Enabled: true, SecretName: "byo"}},
			}}},
			want: false,
		},
		{
			name: "ingress TLS with issuerRef needs cert-manager",
			ha: &hav1.HomeAssistant{Spec: hav1.HomeAssistantSpec{Ingress: &hav1.IngressSpec{
				Enabled: true,
				TLS:     &hav1.IngressTLSSpec{Enabled: true, IssuerRef: &hav1.IssuerReference{Name: "i"}},
			}}},
			want: true,
		},
		{
			name: "ingress TLS with secretName is bring-your-own",
			ha: &hav1.HomeAssistant{Spec: hav1.HomeAssistantSpec{Ingress: &hav1.IngressSpec{
				Enabled: true,
				TLS:     &hav1.IngressTLSSpec{Enabled: true, SecretName: "byo"},
			}}},
			want: false,
		},
		{
			name: "gateway enabled without issuer does not need cert-manager",
			ha: &hav1.HomeAssistant{Spec: hav1.HomeAssistantSpec{
				Gateway: &hav1.GatewaySpec{Enabled: true, Host: "h"},
			}},
			want: false,
		},
		{
			name: "gateway enabled with issuer needs cert-manager",
			ha: &hav1.HomeAssistant{Spec: hav1.HomeAssistantSpec{
				Gateway: &hav1.GatewaySpec{Enabled: true, Host: "h", IssuerRef: &hav1.IssuerReference{Name: "i"}},
			}},
			want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := certManagerRequired(tc.ha); got != tc.want {
				t.Fatalf("certManagerRequired = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCertManagerAvailable(t *testing.T) {
	t.Run("not installed", func(t *testing.T) {
		r := newTLSTestReconciler(t, false)
		got, err := r.certManagerAvailable(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got {
			t.Fatal("expected cert-manager unavailable")
		}
	})

	t.Run("installed", func(t *testing.T) {
		r := newTLSTestReconciler(t, true)
		got, err := r.certManagerAvailable(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !got {
			t.Fatal("expected cert-manager available")
		}
	})

	t.Run("result is cached within TTL", func(t *testing.T) {
		r := newTLSTestReconciler(t, true)
		if _, err := r.certManagerAvailable(context.Background()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Flip the underlying mapper to "absent" but keep the cache warm; the
		// cached (true) result must be returned until the TTL elapses.
		r.Client = newTLSTestReconciler(t, false).Client
		got, err := r.certManagerAvailable(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !got {
			t.Fatal("expected cached availability to persist within TTL")
		}
	})
}

// TestReconcileTLSDegradation covers graceful degradation: TLS requested but cert-manager
// absent must degrade gracefully (condition + requeue, no error, no certificate).
func TestReconcileTLSDegradation(t *testing.T) {
	ha := nativeTLSHA("home")
	r := newTLSTestReconciler(t, false, ha)

	res, err := r.reconcileTLS(context.Background(), ha)
	if err != nil {
		t.Fatalf("reconcileTLS returned error: %v", err)
	}
	if res.RequeueAfter <= 0 {
		t.Fatalf("expected requeue to poll for cert-manager, got %v", res.RequeueAfter)
	}

	cond := meta.FindStatusCondition(ha.Status.Conditions, conditionCertManagerAvailable)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != reasonCertManagerNotInstalled {
		t.Fatalf("expected CertManagerAvailable=False/%s, got %+v", reasonCertManagerNotInstalled, cond)
	}
	tlsCond := meta.FindStatusCondition(ha.Status.Conditions, conditionTLSReady)
	if tlsCond == nil || tlsCond.Status != metav1.ConditionUnknown {
		t.Fatalf("expected TLSReady=Unknown, got %+v", tlsCond)
	}
}

func TestReconcileTLSNoopWhenNoTLSRequested(t *testing.T) {
	ha := &hav1.HomeAssistant{ObjectMeta: metav1.ObjectMeta{Name: "home", Namespace: "default"}}
	r := newTLSTestReconciler(t, false, ha)

	res, err := r.reconcileTLS(context.Background(), ha)
	if err != nil {
		t.Fatalf("reconcileTLS returned error: %v", err)
	}
	if res.RequeueAfter != 0 {
		t.Fatalf("expected no requeue when no TLS requested, got %v", res.RequeueAfter)
	}
	if len(ha.Status.Conditions) != 0 {
		t.Fatalf("expected no TLS conditions when no TLS requested, got %d", len(ha.Status.Conditions))
	}
}

// TestReconcileTLSAvailableSetsCondition verifies the available path records the
// CertManagerAvailable=True condition.
func TestReconcileTLSAvailableSetsCondition(t *testing.T) {
	ha := nativeTLSHA("home")
	r := newTLSTestReconciler(t, true, ha)

	if _, err := r.reconcileTLS(context.Background(), ha); err != nil {
		t.Fatalf("reconcileTLS returned error: %v", err)
	}
	cond := meta.FindStatusCondition(ha.Status.Conditions, conditionCertManagerAvailable)
	if cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != reasonCertManagerInstalled {
		t.Fatalf("expected CertManagerAvailable=True/%s, got %+v", reasonCertManagerInstalled, cond)
	}
}

// getCertificate fetches the operator-managed native TLS Certificate.
func getCertificate(
	t *testing.T, r *HomeAssistantReconciler, ha *hav1.HomeAssistant,
) (*unstructured.Unstructured, error) {
	t.Helper()
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(certificateGVK)
	err := r.Get(context.Background(), client.ObjectKey{Name: nativeTLSCertificateName(ha), Namespace: ha.Namespace}, u)
	return u, err
}

// TestReconcileTLSCreatesNativeCertificate (US2): with cert-manager available and
// native TLS on, the operator creates a Certificate (with the Service FQDN SAN)
// and reports TLSReady=False until it is issued.
func TestReconcileTLSCreatesNativeCertificate(t *testing.T) {
	ha := nativeTLSHA("home")
	r := newTLSTestReconciler(t, true, ha)

	res, err := r.reconcileTLS(context.Background(), ha)
	if err != nil {
		t.Fatalf("reconcileTLS error: %v", err)
	}
	if res.RequeueAfter <= 0 {
		t.Fatalf("expected requeue while waiting for issuance, got %v", res.RequeueAfter)
	}

	cert, err := getCertificate(t, r, ha)
	if err != nil {
		t.Fatalf("expected Certificate to be created: %v", err)
	}
	dnsNames, _, _ := unstructured.NestedStringSlice(cert.Object, "spec", "dnsNames")
	wantFQDN := "home.default.svc.cluster.local"
	found := false
	for _, d := range dnsNames {
		if d == wantFQDN {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected Service FQDN %q in dnsNames %v", wantFQDN, dnsNames)
	}
	issuer, _, _ := unstructured.NestedString(cert.Object, "spec", "issuerRef", "name")
	if issuer != "test-issuer" {
		t.Fatalf("expected issuerRef.name=test-issuer, got %q", issuer)
	}

	tlsCond := meta.FindStatusCondition(ha.Status.Conditions, conditionTLSReady)
	if tlsCond == nil || tlsCond.Status != metav1.ConditionFalse || tlsCond.Reason != reasonCertificateNotIssued {
		t.Fatalf("expected TLSReady=False/%s, got %+v", reasonCertificateNotIssued, tlsCond)
	}
}

// TestReconcileTLSNativeReady: a Certificate reporting Ready=True flips TLSReady.
func TestReconcileTLSNativeReady(t *testing.T) {
	ha := nativeTLSHA("home")
	cert := &unstructured.Unstructured{Object: map[string]interface{}{}}
	cert.SetGroupVersionKind(certificateGVK)
	cert.SetName(nativeTLSCertificateName(ha))
	cert.SetNamespace(ha.Namespace)
	cert.Object["spec"] = desiredNativeCertificateSpec(ha)
	_ = unstructured.SetNestedSlice(cert.Object, []interface{}{
		map[string]interface{}{"type": "Ready", "status": "True"},
	}, "status", "conditions")

	r := newTLSTestReconciler(t, true, ha, cert)
	if _, err := r.reconcileTLS(context.Background(), ha); err != nil {
		t.Fatalf("reconcileTLS error: %v", err)
	}
	tlsCond := meta.FindStatusCondition(ha.Status.Conditions, conditionTLSReady)
	if tlsCond == nil || tlsCond.Status != metav1.ConditionTrue || tlsCond.Reason != reasonTLSReady {
		t.Fatalf("expected TLSReady=True/%s, got %+v", reasonTLSReady, tlsCond)
	}
}

// TestReconcileTLSNativeBYO: bring-your-own Secret needs no cert-manager and
// creates no Certificate.
func TestReconcileTLSNativeBYO(t *testing.T) {
	ha := &hav1.HomeAssistant{
		ObjectMeta: metav1.ObjectMeta{Name: "home", Namespace: "default"},
		Spec: hav1.HomeAssistantSpec{Alpha: &hav1.AlphaSpec{TLS: &hav1.TLSAlphaSpec{
			Native: &hav1.NativeTLSAlphaSpec{Enabled: true, SecretName: "my-tls"},
		}}},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "my-tls", Namespace: "default"},
		Data:       map[string][]byte{"tls.crt": []byte("cert"), "tls.key": []byte("key")},
	}
	r := newTLSTestReconciler(t, false, ha, secret) // cert-manager absent — must not matter

	if _, err := r.reconcileTLS(context.Background(), ha); err != nil {
		t.Fatalf("reconcileTLS error: %v", err)
	}
	tlsCond := meta.FindStatusCondition(ha.Status.Conditions, conditionTLSReady)
	if tlsCond == nil || tlsCond.Status != metav1.ConditionTrue || tlsCond.Reason != reasonUsingProvidedSecret {
		t.Fatalf("expected TLSReady=True/%s, got %+v", reasonUsingProvidedSecret, tlsCond)
	}
	if _, err := getCertificate(t, r, ha); err == nil {
		t.Fatal("expected no operator-managed Certificate for bring-your-own secret")
	}
}

// TestReconcileTLSNativeBYOMissingSecret: a bring-your-own Secret that doesn't
// exist (or lacks tls.crt/tls.key) must not report TLSReady=True.
func TestReconcileTLSNativeBYOMissingSecret(t *testing.T) {
	ha := &hav1.HomeAssistant{
		ObjectMeta: metav1.ObjectMeta{Name: "home", Namespace: "default"},
		Spec: hav1.HomeAssistantSpec{Alpha: &hav1.AlphaSpec{TLS: &hav1.TLSAlphaSpec{
			Native: &hav1.NativeTLSAlphaSpec{Enabled: true, SecretName: "my-tls"},
		}}},
	}
	r := newTLSTestReconciler(t, false, ha) // Secret "my-tls" does not exist

	if _, err := r.reconcileTLS(context.Background(), ha); err != nil {
		t.Fatalf("reconcileTLS error: %v", err)
	}
	tlsCond := meta.FindStatusCondition(ha.Status.Conditions, conditionTLSReady)
	if tlsCond == nil || tlsCond.Status != metav1.ConditionFalse || tlsCond.Reason != reasonProvidedSecretInvalid {
		t.Fatalf("expected TLSReady=False/%s, got %+v", reasonProvidedSecretInvalid, tlsCond)
	}
}

// TestReconcileTLSCleanupOnDisable: disabling native TLS deletes the managed cert.
func TestReconcileTLSCleanupOnDisable(t *testing.T) {
	ha := &hav1.HomeAssistant{ObjectMeta: metav1.ObjectMeta{Name: "home", Namespace: "default"}}
	cert := &unstructured.Unstructured{Object: map[string]interface{}{}}
	cert.SetGroupVersionKind(certificateGVK)
	cert.SetName(nativeTLSCertificateName(ha))
	cert.SetNamespace(ha.Namespace)

	r := newTLSTestReconciler(t, true, ha, cert)
	if _, err := r.reconcileTLS(context.Background(), ha); err != nil {
		t.Fatalf("reconcileTLS error: %v", err)
	}
	if _, err := getCertificate(t, r, ha); err == nil {
		t.Fatal("expected orphaned Certificate to be deleted when native TLS is off")
	}
}

func withTLSReady(ha *hav1.HomeAssistant) *hav1.HomeAssistant {
	meta.SetStatusCondition(&ha.Status.Conditions, metav1.Condition{
		Type: conditionTLSReady, Status: metav1.ConditionTrue, Reason: reasonTLSReady,
	})
	return ha
}

// TestNativeTLSActiveAndScheme (US2 runtime): scheme flips to https only once the
// certificate is ready (TLSReady=True), so operator and HA switch together.
func TestNativeTLSActiveAndScheme(t *testing.T) {
	// Enabled but not yet ready → still http.
	pending := nativeTLSHA("home")
	if nativeTLSActive(pending) {
		t.Fatal("native TLS must not be active before TLSReady")
	}
	if haScheme(pending) != "http" {
		t.Fatalf("expected http before ready, got %s", haScheme(pending))
	}

	ready := withTLSReady(nativeTLSHA("home"))
	if !nativeTLSActive(ready) {
		t.Fatal("native TLS should be active when enabled and TLSReady=True")
	}
	if haScheme(ready) != "https" {
		t.Fatalf("expected https when active, got %s", haScheme(ready))
	}
	if got := buildHomeAssistantURL(ready); got != "https://home.default.svc.cluster.local:8123" {
		t.Fatalf("unexpected URL: %s", got)
	}
}

func TestLoadNativeTLSCA(t *testing.T) {
	ha := withTLSReady(nativeTLSHA("home"))
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: nativeTLSSecretName(ha), Namespace: ha.Namespace},
		Data:       map[string][]byte{"ca.crt": []byte("CA-PEM")},
	}
	r := newTLSTestReconciler(t, true, ha, secret)
	if ca := loadNativeTLSCA(context.Background(), r.Client, ha); string(ca) != "CA-PEM" {
		t.Fatalf("expected CA-PEM, got %q", string(ca))
	}

	// No secret → nil (fail closed to system roots, never InsecureSkipVerify).
	r2 := newTLSTestReconciler(t, true, nativeTLSHA("other"))
	if ca := loadNativeTLSCA(context.Background(), r2.Client, nativeTLSHA("other")); ca != nil {
		t.Fatalf("expected nil CA when secret absent, got %q", string(ca))
	}
}

// TestInjectNativeTLS (T016): http.ssl_certificate/ssl_key are injected into the
// configuration, preserving other http keys, and !include http sections untouched.
func TestInjectNativeTLS(t *testing.T) {
	t.Run("adds http section when missing", func(t *testing.T) {
		out, err := injectNativeTLS("default_config:\n")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "ssl_certificate: /config/ssl/tls.crt") ||
			!strings.Contains(out, "ssl_key: /config/ssl/tls.key") {
			t.Fatalf("missing ssl keys:\n%s", out)
		}
	})

	t.Run("preserves existing http keys", func(t *testing.T) {
		out, err := injectNativeTLS("http:\n  use_x_forwarded_for: true\n")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "use_x_forwarded_for: true") ||
			!strings.Contains(out, "ssl_certificate: /config/ssl/tls.crt") {
			t.Fatalf("unexpected output:\n%s", out)
		}
	})

	t.Run("converts an empty/null http scalar to a mapping", func(t *testing.T) {
		out, err := injectNativeTLS("default_config:\nhttp:\n")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "ssl_certificate: /config/ssl/tls.crt") ||
			!strings.Contains(out, "ssl_key: /config/ssl/tls.key") {
			t.Fatalf("expected ssl keys under a null http:\n%s", out)
		}
	})

	t.Run("preserves tagged-scalar http include", func(t *testing.T) {
		in := "http: !include http.yaml\n"
		out, err := injectNativeTLS(in)
		if err != nil {
			t.Fatal(err)
		}
		if out != in {
			t.Fatalf("expected include preserved, got:\n%s", out)
		}
	})
}
