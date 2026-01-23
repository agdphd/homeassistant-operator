package utils

import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"strings"
	"time"
)

// RandomString generates a random alphanumeric string of specified length.
func RandomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	seededRand := rand.New(rand.NewSource(time.Now().UnixNano()))
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[seededRand.Intn(len(charset))]
	}
	return string(b)
}

// Kubectl executes a kubectl command with given arguments and returns trimmed output.
// Returns empty string on error instead of panicking.
func Kubectl(args ...string) string {
	cmd := exec.Command("kubectl", args...)
	output, err := Run(cmd)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(output)
}

// ApplyYAML applies YAML content to specified namespace using kubectl apply.
func ApplyYAML(yamlContent, namespace string) error {
	tmpFile := "/tmp/e2e-apply-" + RandomString(8) + ".yaml"
	if err := os.WriteFile(tmpFile, []byte(yamlContent), 0644); err != nil {
		return fmt.Errorf("failed to write YAML to temp file: %w", err)
	}
	defer os.Remove(tmpFile)

	cmd := exec.Command("kubectl", "apply", "-f", tmpFile, "-n", namespace)
	_, err := Run(cmd)
	if err != nil {
		return fmt.Errorf("kubectl apply failed: %w", err)
	}
	return nil
}

// CreateNamespace creates a Kubernetes namespace.
func CreateNamespace(namespace string) error {
	cmd := exec.Command("kubectl", "create", "ns", namespace)
	_, err := Run(cmd)
	if err != nil {
		return fmt.Errorf("failed to create namespace %s: %w", namespace, err)
	}
	return nil
}

// DeleteNamespace deletes a Kubernetes namespace with timeout.
// Uses --ignore-not-found=true to gracefully handle non-existent namespaces.
func DeleteNamespace(namespace string) error {
	cmd := exec.Command("kubectl", "delete", "ns", namespace, "--ignore-not-found=true", "--timeout=5m")
	_, err := Run(cmd)
	if err != nil {
		return fmt.Errorf("failed to delete namespace %s: %w", namespace, err)
	}
	return nil
}
