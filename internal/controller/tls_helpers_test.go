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
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

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
	return newTLSTestReconcilerWithFuncs(t, certManagerPresent, interceptor.Funcs{}, objs...)
}

func newTLSTestReconcilerWithFuncs(
	t *testing.T, certManagerPresent bool, funcs interceptor.Funcs, objs ...client.Object,
) *HomeAssistantReconciler {
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
		WithInterceptorFuncs(funcs).
		Build()
	return &HomeAssistantReconciler{
		Client:   cl,
		Scheme:   scheme,
		Recorder: events.NewFakeRecorder(16),
	}
}

// ingressTLSHA returns a HomeAssistant that requests edge (Ingress) TLS via an
// issuerRef — i.e. a mode that needs cert-manager.
func ingressTLSHA(name string) *hav1.HomeAssistant {
	return &hav1.HomeAssistant{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", UID: types.UID(name + "-uid")},
		Spec: hav1.HomeAssistantSpec{
			Ingress: &hav1.IngressSpec{
				Enabled: true,
				TLS: &hav1.IngressTLSSpec{
					Enabled:   true,
					IssuerRef: &hav1.IssuerReference{Name: "test-issuer", Kind: "ClusterIssuer"},
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
			name: "ingress TLS with issuerRef needs cert-manager",
			ha:   ingressTLSHA("ha"),
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

// TestReconcileTLSDegradation covers graceful degradation: an edge TLS mode is
// requested but cert-manager is absent — degrade gracefully (condition + requeue,
// no error).
func TestReconcileTLSDegradation(t *testing.T) {
	ha := ingressTLSHA("home")
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
	ha := ingressTLSHA("home")
	r := newTLSTestReconciler(t, true, ha)

	if _, err := r.reconcileTLS(context.Background(), ha); err != nil {
		t.Fatalf("reconcileTLS returned error: %v", err)
	}
	cond := meta.FindStatusCondition(ha.Status.Conditions, conditionCertManagerAvailable)
	if cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != reasonCertManagerInstalled {
		t.Fatalf("expected CertManagerAvailable=True/%s, got %+v", reasonCertManagerInstalled, cond)
	}
}

// nativeCert builds the (now-obsolete) operator-managed native TLS Certificate
// object for ha, with the controller owner reference the operator would have set,
// as a transition-era instance would still have on the cluster.
func nativeCert(ha *hav1.HomeAssistant) *unstructured.Unstructured {
	c := foreignCert(ha)
	c.SetOwnerReferences([]metav1.OwnerReference{{
		APIVersion: hav1.GroupVersion.String(),
		Kind:       "HomeAssistant",
		Name:       ha.Name,
		UID:        ha.UID,
		Controller: ptr.To(true),
	}})
	return c
}

// foreignCert is a Certificate sharing the native-TLS name but NOT owned by the
// operator — a user-managed resource the cleanup must leave alone.
func foreignCert(ha *hav1.HomeAssistant) *unstructured.Unstructured {
	c := &unstructured.Unstructured{Object: map[string]interface{}{}}
	c.SetGroupVersionKind(certificateGVK)
	c.SetName(nativeTLSCertificateName(ha))
	c.SetNamespace(ha.Namespace)
	return c
}

func withCondition(ha *hav1.HomeAssistant, condType string) *hav1.HomeAssistant {
	meta.SetStatusCondition(&ha.Status.Conditions, metav1.Condition{
		Type: condType, Status: metav1.ConditionTrue, Reason: "Test",
	})
	return ha
}

func certGone(t *testing.T, r *HomeAssistantReconciler, ha *hav1.HomeAssistant) bool {
	t.Helper()
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(certificateGVK)
	err := r.Get(context.Background(), client.ObjectKey{Name: nativeTLSCertificateName(ha), Namespace: ha.Namespace}, u)
	return err != nil
}

func warningEvents(r *HomeAssistantReconciler) []string {
	fr := r.Recorder.(*events.FakeRecorder)
	var got []string
	for {
		select {
		case e := <-fr.Events:
			got = append(got, e)
		default:
			return got
		}
	}
}

// TestReconcileNativeTLSRemoval covers the transitional cleanup of the removed
// native TLS feature.
func TestReconcileNativeTLSRemoval(t *testing.T) {
	ctx := context.Background()

	t.Run("removes condition + certificate and emits one warning", func(t *testing.T) {
		ha := withCondition(&hav1.HomeAssistant{
			ObjectMeta: metav1.ObjectMeta{Name: "home", Namespace: "default", UID: "home-uid"},
		}, conditionTLSReady)
		r := newTLSTestReconciler(t, true, ha, nativeCert(ha))

		if err := r.reconcileNativeTLSRemoval(ctx, ha); err != nil {
			t.Fatalf("reconcileNativeTLSRemoval error: %v", err)
		}
		if meta.FindStatusCondition(ha.Status.Conditions, conditionTLSReady) != nil {
			t.Fatal("expected TLSReady condition to be removed")
		}
		if !certGone(t, r, ha) {
			t.Fatal("expected native TLS Certificate to be deleted")
		}
		evs := warningEvents(r)
		count := 0
		for _, e := range evs {
			if strings.Contains(e, eventNativeTLSRemoved) {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("expected exactly one %s event, got %d (%v)", eventNativeTLSRemoved, count, evs)
		}
	})

	t.Run("silent no-op for an instance that never used native TLS", func(t *testing.T) {
		ha := &hav1.HomeAssistant{ObjectMeta: metav1.ObjectMeta{Name: "plain", Namespace: "default"}}
		r := newTLSTestReconciler(t, true, ha)

		if err := r.reconcileNativeTLSRemoval(ctx, ha); err != nil {
			t.Fatalf("reconcileNativeTLSRemoval error: %v", err)
		}
		for _, e := range warningEvents(r) {
			if strings.Contains(e, eventNativeTLSRemoved) {
				t.Fatalf("unexpected %s event for plain instance: %s", eventNativeTLSRemoved, e)
			}
		}
	})

	t.Run("does not delete a same-named Certificate the operator does not own", func(t *testing.T) {
		ha := &hav1.HomeAssistant{ObjectMeta: metav1.ObjectMeta{Name: "home", Namespace: "default", UID: "home-uid"}}
		r := newTLSTestReconciler(t, true, ha, foreignCert(ha))

		if err := r.reconcileNativeTLSRemoval(ctx, ha); err != nil {
			t.Fatalf("reconcileNativeTLSRemoval error: %v", err)
		}
		if certGone(t, r, ha) {
			t.Fatal("a user-managed Certificate sharing the native-TLS name must not be deleted")
		}
		for _, e := range warningEvents(r) {
			if strings.Contains(e, eventNativeTLSRemoved) {
				t.Fatalf("unexpected %s event for a foreign Certificate", eventNativeTLSRemoved)
			}
		}
	})

	t.Run("Certificate replaced between the validating Get and the Delete is not removed", func(t *testing.T) {
		ha := withCondition(&hav1.HomeAssistant{
			ObjectMeta: metav1.ObjectMeta{Name: "home", Namespace: "default", UID: "home-uid"},
		}, conditionTLSReady)
		owned := nativeCert(ha)
		owned.SetUID("native-cert-uid")

		var gotUID *types.UID
		funcs := interceptor.Funcs{
			Delete: func(
				_ context.Context, _ client.WithWatch, _ client.Object, opts ...client.DeleteOption,
			) error {
				do := &client.DeleteOptions{}
				do.ApplyOptions(opts)
				if do.Preconditions != nil {
					gotUID = do.Preconditions.UID
				}
				// Simulate the object having been deleted and recreated with a
				// different identity since reconcileNativeTLSRemoval fetched it:
				// the API server rejects the UID precondition with a Conflict.
				return apierrors.NewConflict(
					certificateGVK.GroupVersion().WithResource("certificates").GroupResource(),
					owned.GetName(), errors.New("uid precondition mismatch"))
			},
		}
		r := newTLSTestReconcilerWithFuncs(t, true, funcs, ha, owned)

		if err := r.reconcileNativeTLSRemoval(ctx, ha); err != nil {
			t.Fatalf("reconcileNativeTLSRemoval must swallow the precondition Conflict, got: %v", err)
		}
		if gotUID == nil || *gotUID != "native-cert-uid" {
			t.Fatalf("delete must be pinned to the validated Certificate UID, got %v", gotUID)
		}
		if meta.FindStatusCondition(ha.Status.Conditions, conditionTLSReady) != nil {
			t.Fatal("cleanup should still strip TLSReady after a tolerated delete Conflict")
		}
	})

	t.Run("keeps CertManagerAvailable when an edge TLS mode still needs it", func(t *testing.T) {
		ha := withCondition(ingressTLSHA("edge"), conditionTLSReady)
		withCondition(ha, conditionCertManagerAvailable)
		r := newTLSTestReconciler(t, true, ha, nativeCert(ha))

		if err := r.reconcileNativeTLSRemoval(ctx, ha); err != nil {
			t.Fatalf("reconcileNativeTLSRemoval error: %v", err)
		}
		if meta.FindStatusCondition(ha.Status.Conditions, conditionTLSReady) != nil {
			t.Fatal("expected TLSReady removed")
		}
		if meta.FindStatusCondition(ha.Status.Conditions, conditionCertManagerAvailable) == nil {
			t.Fatal("expected CertManagerAvailable kept for an active edge TLS mode")
		}
	})

	t.Run("idempotent: second run is a no-op with no extra event", func(t *testing.T) {
		ha := withCondition(&hav1.HomeAssistant{
			ObjectMeta: metav1.ObjectMeta{Name: "home", Namespace: "default", UID: "home-uid"},
		}, conditionTLSReady)
		r := newTLSTestReconciler(t, true, ha, nativeCert(ha))

		if err := r.reconcileNativeTLSRemoval(ctx, ha); err != nil {
			t.Fatalf("first run error: %v", err)
		}
		_ = warningEvents(r) // drain
		if err := r.reconcileNativeTLSRemoval(ctx, ha); err != nil {
			t.Fatalf("second run error: %v", err)
		}
		for _, e := range warningEvents(r) {
			if strings.Contains(e, eventNativeTLSRemoved) {
				t.Fatalf("second run must not emit %s: %s", eventNativeTLSRemoved, e)
			}
		}
	})
}
