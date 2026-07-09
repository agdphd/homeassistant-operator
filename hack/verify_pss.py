#!/usr/bin/env python3
"""Pod Security Standards "restricted" compliance checker for the operator manifests.

Reads a multi-document YAML stream (`kustomize build` render) from stdin and verifies:
  - the operator namespace has the 6 PSA labels (enforce/audit/warn=restricted,
    *-version=latest),
  - the controller-manager Deployment: pod + containers satisfy restricted.

Scope: operator ONLY (namespace/Deployment labeled control-plane=controller-manager).
Home Assistant pods are out of scope and are not checked.
"""
import sys

import yaml

REQUIRED_NS_LABELS = {
    "pod-security.kubernetes.io/enforce": "restricted",
    "pod-security.kubernetes.io/enforce-version": "latest",
    "pod-security.kubernetes.io/audit": "restricted",
    "pod-security.kubernetes.io/audit-version": "latest",
    "pod-security.kubernetes.io/warn": "restricted",
    "pod-security.kubernetes.io/warn-version": "latest",
}
ALLOWED_ADD_CAPS = {"NET_BIND_SERVICE"}


def _labels(obj):
    return ((obj.get("metadata") or {}).get("labels") or {})


def _is_operator(obj):
    return _labels(obj).get("control-plane") == "controller-manager"


def check_namespace(ns, violations):
    labels = _labels(ns)
    name = (ns.get("metadata") or {}).get("name", "<unknown>")
    for key, want in REQUIRED_NS_LABELS.items():
        got = labels.get(key)
        if got != want:
            violations.append(
                f"Namespace/{name}: PSA label '{key}' = {got!r}, expected {want!r}"
            )


def check_deployment(dep, violations):
    name = (dep.get("metadata") or {}).get("name", "<unknown>")
    pod = (((dep.get("spec") or {}).get("template") or {}).get("spec") or {})

    for host in ("hostNetwork", "hostPID", "hostIPC"):
        if pod.get(host) is True:
            violations.append(f"Deployment/{name}: {host}=true is forbidden under restricted")

    for vol in (pod.get("volumes") or []):
        if vol.get("hostPath") is not None:
            vname = vol.get("name", "<unnamed>")
            violations.append(
                f"Deployment/{name}: hostPath volume {vname!r} is forbidden under restricted"
            )

    pod_sc = pod.get("securityContext") or {}
    if pod_sc.get("runAsNonRoot") is not True:
        violations.append(
            f"Deployment/{name}: spec.securityContext.runAsNonRoot must be true"
        )
    seccomp = (pod_sc.get("seccompProfile") or {}).get("type")
    if seccomp not in ("RuntimeDefault", "Localhost"):
        violations.append(
            f"Deployment/{name}: seccompProfile.type = {seccomp!r}, expected RuntimeDefault/Localhost"
        )

    containers = (pod.get("containers") or []) + (pod.get("initContainers") or [])
    if not containers:
        violations.append(f"Deployment/{name}: no containers to verify")
    for c in containers:
        cname = c.get("name", "<unnamed>")
        sc = c.get("securityContext") or {}
        if sc.get("allowPrivilegeEscalation") is not False:
            violations.append(
                f"Deployment/{name} container {cname}: allowPrivilegeEscalation must be false"
            )
        if sc.get("privileged") is True:
            violations.append(f"Deployment/{name} container {cname}: privileged=true is forbidden")
        caps = sc.get("capabilities") or {}
        drop = {str(x).upper() for x in (caps.get("drop") or [])}
        if "ALL" not in drop:
            violations.append(
                f"Deployment/{name} container {cname}: capabilities.drop must contain ALL"
            )
        add = {str(x).upper() for x in (caps.get("add") or [])}
        illegal = add - ALLOWED_ADD_CAPS
        if illegal:
            violations.append(
                f"Deployment/{name} container {cname}: disallowed capabilities.add {sorted(illegal)}"
            )
        if sc.get("runAsNonRoot") is False:
            violations.append(
                f"Deployment/{name} container {cname}: runAsNonRoot must not be false"
            )


def main():
    docs = [d for d in yaml.safe_load_all(sys.stdin) if d]
    violations = []
    seen_ns = seen_dep = False

    for obj in docs:
        kind = obj.get("kind")
        if kind == "Namespace" and _is_operator(obj):
            seen_ns = True
            check_namespace(obj, violations)
        elif kind == "Deployment" and _is_operator(obj):
            seen_dep = True
            check_deployment(obj, violations)

    if not seen_ns:
        violations.append("operator namespace not found (control-plane=controller-manager)")
    if not seen_dep:
        violations.append("operator Deployment not found (control-plane=controller-manager)")

    if violations:
        print("❌ verify-pss: operator manifests do NOT satisfy the restricted profile:", file=sys.stderr)
        for v in violations:
            print(f"  - {v}", file=sys.stderr)
        sys.exit(1)

    print("✅ verify-pss: operator manifests satisfy and enforce restricted (version latest)")


if __name__ == "__main__":
    main()
