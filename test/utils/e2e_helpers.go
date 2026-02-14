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
	tmpFile := os.TempDir() + "/e2e-apply-" + RandomString(8) + ".yaml"
	if err := os.WriteFile(tmpFile, []byte(yamlContent), 0644); err != nil {
		return fmt.Errorf("failed to write YAML to temp file: %w", err)
	}
	defer func() { _ = os.Remove(tmpFile) }()

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
// If deletion fails, attempts force delete by removing finalizers.
func DeleteNamespace(namespace string) error {
	cmd := exec.Command("kubectl", "delete", "ns", namespace, "--ignore-not-found=true", "--timeout=2m")
	_, err := Run(cmd)
	if err != nil {
		// Attempt force delete by removing finalizers
		fmt.Printf("Warning: namespace %s deletion failed, attempting force delete...\n", namespace)
		if forceErr := ForceDeleteNamespace(namespace); forceErr != nil {
			return fmt.Errorf(
				"failed to delete namespace %s (normal delete failed: %v, force delete failed: %v)",
				namespace, err, forceErr,
			)
		}
	}
	return nil
}

// ForceDeleteNamespace forcefully deletes a namespace by removing all finalizers.
// This should only be used as a last resort when normal deletion hangs.
func ForceDeleteNamespace(namespace string) error {
	// First, try to remove finalizers from all resources in the namespace
	resourceTypes := []string{
		"homeassistants", "homeassistantconfigurations",
		"homeassistantsecrets", "homeassistantautomations",
	}

	for _, resourceType := range resourceTypes {
		// List all resources of this type
		listCmd := exec.Command("kubectl", "get", resourceType, "-n", namespace, "-o", "name", "--ignore-not-found=true")
		output, err := listCmd.CombinedOutput()
		if err != nil {
			// Ignore errors - resource type might not exist or no resources found
			continue
		}

		// Remove finalizers from each resource
		resources := strings.Split(strings.TrimSpace(string(output)), "\n")
		for _, resource := range resources {
			if resource == "" {
				continue
			}
			patchCmd := exec.Command("kubectl", "patch", resource, "-n", namespace,
				"--type", "json", "-p", `[{"op":"remove","path":"/metadata/finalizers"}]`)
			_, _ = patchCmd.CombinedOutput() // Ignore errors - resource might already be deleted
		}
	}

	// Now try to remove finalizers from the namespace itself
	patchCmd := exec.Command("kubectl", "patch", "namespace", namespace,
		"--type", "json", "-p", `[{"op":"remove","path":"/spec/finalizers"}]`)
	_, _ = patchCmd.CombinedOutput() // Ignore errors

	// Final delete attempt with short timeout
	deleteCmd := exec.Command("kubectl", "delete", "ns", namespace, "--ignore-not-found=true", "--timeout=30s")
	_, err := Run(deleteCmd)
	return err
}

// GetResourceStatus retrieves a specific status field from a Kubernetes resource using JSONPath.
// Parameters:
//   - resourceType: The resource type (e.g., "homeassistant", "configmap", "secret")
//   - resourceName: The name of the resource instance
//   - namespace: The namespace where the resource exists
//   - jsonPath: JSONPath expression (e.g., "{.status.phase}", "{.data.token}")
//
// Returns the value as string, or empty string if resource not found or jsonpath doesn't match.
func GetResourceStatus(resourceType, resourceName, namespace, jsonPath string) string {
	cmd := exec.Command(
		"kubectl", "get", resourceType, resourceName,
		"-n", namespace, "-o", fmt.Sprintf("jsonpath=%s", jsonPath),
	)
	output, err := Run(cmd)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(output)
}

// PatchResource applies a JSON patch to a Kubernetes resource.
// Parameters:
//   - resourceType: The resource type (e.g., "homeassistant", "secret", "configmap")
//   - resourceName: The name of the resource instance
//   - namespace: The namespace where the resource exists
//   - patchType: Patch type ("json", "merge", or "strategic")
//   - patch: The patch content as string (JSON format)
//
// Returns error if patch fails.
func PatchResource(resourceType, resourceName, namespace, patchType, patch string) error {
	cmd := exec.Command("kubectl", "patch", resourceType, resourceName, "-n", namespace, "--type", patchType, "-p", patch)
	_, err := Run(cmd)
	if err != nil {
		return fmt.Errorf("failed to patch %s/%s in namespace %s: %w", resourceType, resourceName, namespace, err)
	}
	return nil
}
