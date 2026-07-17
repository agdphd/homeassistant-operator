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

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	hav1 "github.com/przemekhys/homeassistant-operator/api/v1"
)

// Gateway API GroupVersionKinds the operator manages as unstructured (avoiding a
// dependency on sigs.k8s.io/gateway-api and keeping graceful degradation simple —
// consistent with how cert-manager Certificates are handled).
const gatewayAPIGroup = "gateway.networking.k8s.io"

var (
	httpRouteGVK = schema.GroupVersionKind{Group: gatewayAPIGroup, Version: "v1", Kind: "HTTPRoute"}
	gatewayGVK   = schema.GroupVersionKind{Group: gatewayAPIGroup, Version: "v1", Kind: "Gateway"}
)

func ingressTLSCertificateName(ha *hav1.HomeAssistant) string { return ha.Name + "-ingress-tls" }
func gatewayTLSCertificateName(ha *hav1.HomeAssistant) string { return ha.Name + "-gateway-tls" }
func managedGatewayName(ha *hav1.HomeAssistant) string        { return ha.Name + "-gateway" }

func servicePort(ha *hav1.HomeAssistant) int32 {
	if ha.Spec.Service != nil && ha.Spec.Service.Port != 0 {
		return ha.Spec.Service.Port
	}
	return defaultHomeAssistantPort
}

// reconcileExposure manages Ingress and Gateway API exposure for Home Assistant,
// including the cert-manager Certificate backing the edge TLS. Missing cert-manager
// is never fatal: HTTP exposure is still created and the TLS condition reflects the
// degradation (handled in reconcileTLS). Disabled modes are cleaned up.
func (r *HomeAssistantReconciler) reconcileExposure(ctx context.Context, ha *hav1.HomeAssistant) error {
	available, err := r.certManagerAvailable(ctx)
	if err != nil {
		// Transient detection failure — treat cert-manager as unavailable (HTTP
		// exposure still works) and log, so it is distinguishable from "absent".
		logf.FromContext(ctx).V(1).Info("cert-manager detection failed during exposure", "error", err)
	}
	exposed := false

	// --- Ingress ---
	if ha.Spec.Ingress != nil && ha.Spec.Ingress.Enabled {
		if err := r.reconcileIngress(ctx, ha, available); err != nil {
			return err
		}
		exposed = true
	} else {
		if err := r.deleteIngress(ctx, ha); err != nil {
			return err
		}
		if err := r.deleteCertificate(ctx, ha, ingressTLSCertificateName(ha)); err != nil {
			return err
		}
	}

	// --- Gateway API ---
	if ha.Spec.Gateway != nil && ha.Spec.Gateway.Enabled {
		routed, err := r.reconcileGatewayRoute(ctx, ha, available)
		if err != nil {
			return err
		}
		// Only count as exposed when a route was actually attached (a parentRef or
		// managed Gateway existed) — an enabled Gateway with no attach point is not.
		exposed = exposed || routed
	} else {
		if err := r.deleteExposureResource(ctx, ha, httpRouteGVK, ha.Name); err != nil {
			return err
		}
		if err := r.deleteExposureResource(ctx, ha, gatewayGVK, managedGatewayName(ha)); err != nil {
			return err
		}
		if err := r.deleteCertificate(ctx, ha, gatewayTLSCertificateName(ha)); err != nil {
			return err
		}
	}

	return r.updateExposureReady(ctx, ha, exposed)
}

// updateExposureReady sets ExposureReady=True when exposure resources are active,
// and clears a previously-set condition when exposure is disabled (so a stale True
// does not linger). It adds no condition for resources that never enabled exposure.
func (r *HomeAssistantReconciler) updateExposureReady(ctx context.Context, ha *hav1.HomeAssistant, exposed bool) error {
	if exposed {
		var changed bool
		if err := r.updateHAStatusWithRetry(ctx, ha, func(h *hav1.HomeAssistant) bool {
			changed = meta.SetStatusCondition(&h.Status.Conditions, metav1.Condition{
				Type:               conditionExposureReady,
				Status:             metav1.ConditionTrue,
				ObservedGeneration: h.Generation,
				Reason:             reasonExposureReady,
				Message:            "Exposure resources reconciled",
			})
			return changed
		}); err != nil {
			return err
		}
		if changed {
			r.Recorder.Eventf(ha, nil, corev1.EventTypeNormal, eventExposureConfigured, eventExposureConfigured,
				"Exposure resources reconciled for %q", ha.Name)
		}
		return nil
	}
	return r.updateHAStatusWithRetry(ctx, ha, func(h *hav1.HomeAssistant) bool {
		return meta.RemoveStatusCondition(&h.Status.Conditions, conditionExposureReady)
	})
}

// reconcileIngress creates/updates the operator-managed Ingress and its
// cert-manager Certificate (when an issuerRef is set and cert-manager is present).
func (r *HomeAssistantReconciler) reconcileIngress(
	ctx context.Context, ha *hav1.HomeAssistant, cmAvailable bool,
) error {
	in := ha.Spec.Ingress
	host := in.Host

	// TLS Secret: bring-your-own wins; otherwise the operator-issued Secret.
	var tlsSecret string
	if in.TLS != nil && in.TLS.Enabled {
		switch {
		case in.TLS.SecretName != "":
			tlsSecret = in.TLS.SecretName
		case in.TLS.IssuerRef != nil:
			tlsSecret = ingressTLSCertificateName(ha)
			if cmAvailable {
				_, err := r.ensureCertificate(ctx, ha, ingressTLSCertificateName(ha), []string{host}, in.TLS.IssuerRef)
				if err != nil {
					return err
				}
			}
		}
	}

	ingress := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: ha.Name, Namespace: ha.Namespace}}
	pathType := networkingv1.PathTypePrefix
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, ingress, func() error {
		ingress.Annotations = in.Annotations
		ingress.Spec.IngressClassName = in.IngressClassName
		ingress.Spec.Rules = []networkingv1.IngressRule{{
			Host: host,
			IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{
				Paths: []networkingv1.HTTPIngressPath{{
					Path:     "/",
					PathType: &pathType,
					Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{
						Name: ha.Name,
						Port: networkingv1.ServiceBackendPort{Number: servicePort(ha)},
					}},
				}},
			}},
		}}
		if tlsSecret != "" {
			ingress.Spec.TLS = []networkingv1.IngressTLS{{Hosts: []string{host}, SecretName: tlsSecret}}
		} else {
			ingress.Spec.TLS = nil
		}
		return controllerutil.SetControllerReference(ha, ingress, r.Scheme)
	})
	return err
}

func (r *HomeAssistantReconciler) deleteIngress(ctx context.Context, ha *hav1.HomeAssistant) error {
	ingress := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: ha.Name, Namespace: ha.Namespace}}
	if err := r.Delete(ctx, ingress); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

// reconcileGatewayRoute creates/updates the HTTPRoute (and, when manageGateway is
// set, a Gateway) plus the cert-manager Certificate for the listener. It returns
// true only when a route was actually attached to a parent; an enabled Gateway
// with neither parentRef nor manageGateway has no attach point and returns false.
func (r *HomeAssistantReconciler) reconcileGatewayRoute(
	ctx context.Context, ha *hav1.HomeAssistant, cmAvailable bool,
) (bool, error) {
	g := ha.Spec.Gateway

	var tlsSecret string
	if g.SecretName != "" {
		tlsSecret = g.SecretName
	} else if g.IssuerRef != nil {
		tlsSecret = gatewayTLSCertificateName(ha)
		if cmAvailable {
			_, err := r.ensureCertificate(ctx, ha, gatewayTLSCertificateName(ha), []string{g.Host}, g.IssuerRef)
			if err != nil {
				return false, err
			}
		}
	}

	// Determine the parent Gateway to attach the route to.
	parent := map[string]interface{}{}
	switch {
	case g.ParentRef != nil:
		parent["name"] = g.ParentRef.Name
		if g.ParentRef.Namespace != "" {
			parent["namespace"] = g.ParentRef.Namespace
		}
		if g.ParentRef.SectionName != "" {
			parent["sectionName"] = g.ParentRef.SectionName
		}
	case g.ManageGateway:
		if err := r.ensureManagedGateway(ctx, ha, tlsSecret); err != nil {
			return false, err
		}
		parent["name"] = managedGatewayName(ha)
		parent["sectionName"] = "https"
	default:
		// No parent to attach to — nothing to route yet.
		return false, nil
	}

	route := &unstructured.Unstructured{}
	route.SetGroupVersionKind(httpRouteGVK)
	route.SetName(ha.Name)
	route.SetNamespace(ha.Namespace)
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, route, func() error {
		route.Object["spec"] = map[string]interface{}{
			"parentRefs": []interface{}{parent},
			"hostnames":  []interface{}{g.Host},
			"rules": []interface{}{map[string]interface{}{
				"backendRefs": []interface{}{map[string]interface{}{
					"name": ha.Name,
					"port": int64(servicePort(ha)),
				}},
			}},
		}
		return controllerutil.SetControllerReference(ha, route, r.Scheme)
	})
	if err != nil {
		return false, err
	}
	return true, nil
}

// ensureManagedGateway creates/updates a Gateway with an HTTPS listener when the
// user opts into operator-managed Gateway (spec.gateway.manageGateway).
func (r *HomeAssistantReconciler) ensureManagedGateway(
	ctx context.Context, ha *hav1.HomeAssistant, tlsSecret string,
) error {
	gw := &unstructured.Unstructured{}
	gw.SetGroupVersionKind(gatewayGVK)
	gw.SetName(managedGatewayName(ha))
	gw.SetNamespace(ha.Namespace)
	listener := map[string]interface{}{
		"name":     "https",
		"protocol": "HTTPS",
		"port":     int64(443),
		"hostname": ha.Spec.Gateway.Host,
	}
	if tlsSecret != "" {
		listener["tls"] = map[string]interface{}{
			"mode":            "Terminate",
			"certificateRefs": []interface{}{map[string]interface{}{"name": tlsSecret}},
		}
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, gw, func() error {
		gw.Object["spec"] = map[string]interface{}{
			"gatewayClassName": ha.Name,
			"listeners":        []interface{}{listener},
		}
		return controllerutil.SetControllerReference(ha, gw, r.Scheme)
	})
	return err
}

func (r *HomeAssistantReconciler) deleteExposureResource(
	ctx context.Context, ha *hav1.HomeAssistant, gvk schema.GroupVersionKind, name string,
) error {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvk)
	u.SetName(name)
	u.SetNamespace(ha.Namespace)
	if err := r.Delete(ctx, u); err != nil && !apierrors.IsNotFound(err) && !meta.IsNoMatchError(err) {
		return err
	}
	return nil
}
