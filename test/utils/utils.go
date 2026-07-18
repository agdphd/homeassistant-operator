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

package utils

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2" // nolint:revive,staticcheck
)

const (
	prometheusOperatorVersion = "v0.77.1"
	prometheusOperatorURL     = "https://github.com/prometheus-operator/prometheus-operator/" +
		"releases/download/%s/bundle.yaml"

	certmanagerVersionDefault = "v1.21.0"
	certmanagerURLTmpl        = "https://github.com/cert-manager/cert-manager/releases/download/%s/cert-manager.yaml"
)

// certmanagerVersion returns the cert-manager release to install in E2E tests.
// Set CERT_MANAGER_VERSION in CI (kept current by Renovate via
// .github/workflows/test-e2e-parallel.yml) to pin a specific release;
// defaults to certmanagerVersionDefault for local runs.
func certmanagerVersion() string {
	if v := os.Getenv("CERT_MANAGER_VERSION"); v != "" {
		return v
	}
	return certmanagerVersionDefault
}

func warnError(err error) {
	_, _ = fmt.Fprintf(GinkgoWriter, "warning: %v\n", err)
}

// Run executes the provided command within this context
func Run(cmd *exec.Cmd) (string, error) {
	dir, _ := GetProjectDir()
	cmd.Dir = dir

	if err := os.Chdir(cmd.Dir); err != nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "chdir dir: %q\n", err)
	}

	cmd.Env = append(os.Environ(), "GO111MODULE=on")
	command := strings.Join(cmd.Args, " ")
	_, _ = fmt.Fprintf(GinkgoWriter, "running: %q\n", command)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("%q failed with error %q: %w", command, string(output), err)
	}

	return string(output), nil
}

// InstallPrometheusOperator installs the prometheus Operator to be used to export the enabled metrics.
func InstallPrometheusOperator() error {
	url := fmt.Sprintf(prometheusOperatorURL, prometheusOperatorVersion)
	cmd := exec.Command("kubectl", "create", "-f", url)
	_, err := Run(cmd)
	return err
}

// UninstallPrometheusOperator uninstalls the prometheus
func UninstallPrometheusOperator() {
	url := fmt.Sprintf(prometheusOperatorURL, prometheusOperatorVersion)
	cmd := exec.Command("kubectl", "delete", "-f", url)
	if _, err := Run(cmd); err != nil {
		warnError(err)
	}
}

// IsPrometheusCRDsInstalled checks if any Prometheus CRDs are installed
// by verifying the existence of key CRDs related to Prometheus.
func IsPrometheusCRDsInstalled() bool {
	// List of common Prometheus CRDs
	prometheusCRDs := []string{
		"prometheuses.monitoring.coreos.com",
		"prometheusrules.monitoring.coreos.com",
		"prometheusagents.monitoring.coreos.com",
	}

	cmd := exec.Command("kubectl", "get", "crds", "-o", "custom-columns=NAME:.metadata.name")
	output, err := Run(cmd)
	if err != nil {
		return false
	}
	crdList := GetNonEmptyLines(output)
	for _, crd := range prometheusCRDs {
		for _, line := range crdList {
			if strings.Contains(line, crd) {
				return true
			}
		}
	}

	return false
}

// UninstallCertManager uninstalls the cert manager
func UninstallCertManager() {
	url := fmt.Sprintf(certmanagerURLTmpl, certmanagerVersion())
	cmd := exec.Command("kubectl", "delete", "-f", url)
	if _, err := Run(cmd); err != nil {
		warnError(err)
	}
}

// InstallCertManager installs the cert manager bundle.
func InstallCertManager() error {
	url := fmt.Sprintf(certmanagerURLTmpl, certmanagerVersion())
	cmd := exec.Command("kubectl", "apply", "-f", url)
	if _, err := Run(cmd); err != nil {
		return err
	}
	// Wait for cert-manager-webhook to be ready, which can take time if cert-manager
	// was re-installed after uninstalling on a cluster.
	cmd = exec.Command("kubectl", "wait", "deployment.apps/cert-manager-webhook",
		"--for", "condition=Available",
		"--namespace", "cert-manager",
		"--timeout", "5m",
	)

	_, err := Run(cmd)
	return err
}

// IsCertManagerCRDsInstalled checks if any Cert Manager CRDs are installed
// by verifying the existence of key CRDs related to Cert Manager.
func IsCertManagerCRDsInstalled() bool {
	// List of common Cert Manager CRDs
	certManagerCRDs := []string{
		"certificates.cert-manager.io",
		"issuers.cert-manager.io",
		"clusterissuers.cert-manager.io",
		"certificaterequests.cert-manager.io",
		"orders.acme.cert-manager.io",
		"challenges.acme.cert-manager.io",
	}

	// Execute the kubectl command to get all CRDs
	cmd := exec.Command("kubectl", "get", "crds")
	output, err := Run(cmd)
	if err != nil {
		return false
	}

	// Check if any of the Cert Manager CRDs are present
	crdList := GetNonEmptyLines(output)
	for _, crd := range certManagerCRDs {
		for _, line := range crdList {
			if strings.Contains(line, crd) {
				return true
			}
		}
	}

	return false
}

// K3dClusterName returns the k3d cluster name from K3D_CLUSTER env var or the default.
func K3dClusterName() string {
	if v, ok := os.LookupEnv("K3D_CLUSTER"); ok {
		return v
	}
	return "homeassistant-operator-test-e2e"
}

// K3dContextName returns the kubectl context name for the k3d cluster.
func K3dContextName() string {
	return "k3d-" + K3dClusterName()
}

// IsolateKubeconfigForK3d copies the current kubeconfig to a temp file, sets KUBECONFIG
// env var to that file, and switches the context to the k3d test cluster within the copy.
// This ensures that any context changes made by the user in their terminal do not affect
// the test process, and vice versa — the test never touches ~/.kube/config.
// Returns the path to the temp file so the caller can delete it in AfterSuite.
func IsolateKubeconfigForK3d() (string, error) {
	// Locate the source kubeconfig (respects KUBECONFIG if already set)
	src := os.Getenv("KUBECONFIG")
	if src == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot determine home dir: %w", err)
		}
		src = filepath.Join(home, ".kube", "config")
	}

	// Copy to a temp file
	in, err := os.Open(src)
	if err != nil {
		return "", fmt.Errorf("cannot open kubeconfig %q: %w", src, err)
	}
	defer in.Close() //nolint:errcheck

	tmp, err := os.CreateTemp("", "e2e-kubeconfig-*.yaml")
	if err != nil {
		return "", fmt.Errorf("cannot create temp kubeconfig: %w", err)
	}
	if _, err := io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return "", fmt.Errorf("cannot copy kubeconfig: %w", err)
	}
	_ = tmp.Close()

	// Point this process (and all child kubectl calls) at the copy
	if err := os.Setenv("KUBECONFIG", tmp.Name()); err != nil {
		_ = os.Remove(tmp.Name())
		return "", fmt.Errorf("cannot set KUBECONFIG: %w", err)
	}

	// Switch context inside the copy — never touches ~/.kube/config
	ctx := K3dContextName()
	cmd := exec.Command("kubectl", "config", "use-context", ctx)
	out, err := Run(cmd)
	if err != nil {
		_ = os.Remove(tmp.Name())
		return "", fmt.Errorf("failed to switch kubectl context to %q: %v\nOutput: %s", ctx, err, out)
	}

	return tmp.Name(), nil
}

// SwitchToK3dContext switches the kubectl context to the k3d test cluster.
// It returns an error if the context does not exist (cluster not running).
func SwitchToK3dContext() error {
	ctx := K3dContextName()
	cmd := exec.Command("kubectl", "config", "use-context", ctx)
	out, err := Run(cmd)
	if err != nil {
		return fmt.Errorf("failed to switch kubectl context to %q: %v\nOutput: %s", ctx, err, out)
	}
	return nil
}

// LoadImageToK3dClusterWithName loads a local docker image to the k3d cluster
func LoadImageToK3dClusterWithName(name string) error {
	cluster := K3dClusterName()
	k3dOptions := []string{"image", "import", name, "-c", cluster}
	cmd := exec.Command("k3d", k3dOptions...)
	_, err := Run(cmd)
	return err
}

// GetNonEmptyLines converts given command output string into individual objects
// according to line breakers, and ignores the empty elements in it.
func GetNonEmptyLines(output string) []string {
	var res []string
	elements := strings.Split(output, "\n")
	for _, element := range elements {
		if element != "" {
			res = append(res, element)
		}
	}

	return res
}

// GetProjectDir will return the directory where the project is
func GetProjectDir() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return wd, fmt.Errorf("failed to get current working directory: %w", err)
	}
	wd = strings.ReplaceAll(wd, "/test/e2e", "")
	return wd, nil
}

// UncommentCode searches for target in the file and remove the comment prefix
// of the target content. The target content may span multiple lines.
func UncommentCode(filename, target, prefix string) error {
	// false positive
	// nolint:gosec
	content, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("failed to read file %q: %w", filename, err)
	}
	strContent := string(content)

	idx := strings.Index(strContent, target)
	if idx < 0 {
		return fmt.Errorf("unable to find the code %q to be uncomment", target)
	}

	out := new(bytes.Buffer)
	_, err = out.Write(content[:idx])
	if err != nil {
		return fmt.Errorf("failed to write to output: %w", err)
	}

	scanner := bufio.NewScanner(bytes.NewBufferString(target))
	if !scanner.Scan() {
		return nil
	}
	for {
		if _, err = out.WriteString(strings.TrimPrefix(scanner.Text(), prefix)); err != nil {
			return fmt.Errorf("failed to write to output: %w", err)
		}
		// Avoid writing a newline in case the previous line was the last in target.
		if !scanner.Scan() {
			break
		}
		if _, err = out.WriteString("\n"); err != nil {
			return fmt.Errorf("failed to write to output: %w", err)
		}
	}

	if _, err = out.Write(content[idx+len(target):]); err != nil {
		return fmt.Errorf("failed to write to output: %w", err)
	}

	// false positive
	// nolint:gosec
	if err = os.WriteFile(filename, out.Bytes(), 0644); err != nil {
		return fmt.Errorf("failed to write file %q: %w", filename, err)
	}

	return nil
}
