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
	"bytes"
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// unbanSelfFromHA removes podIP from /config/ip_bans.yaml inside the HA pod
// (<haName>-0) by exec-ing a Python3 one-liner. It does not restart the pod —
// the caller is responsible for deleting the pod so HA clears its in-memory ban.
// Returns nil if the entry was not present or was successfully removed.
func unbanSelfFromHA(
	ctx context.Context,
	restConfig *rest.Config,
	namespace, haName, podIP string,
) error {
	// Python3 is always available inside the HA container (it is a Python app).
	// The script is idempotent: exits 0 even if the file or the entry is absent.
	script := fmt.Sprintf(`
import yaml, os, sys
path = '/config/ip_bans.yaml'
if not os.path.exists(path):
    sys.exit(0)
with open(path) as f:
    content = f.read()
if not content.strip():
    sys.exit(0)
d = yaml.safe_load(content) or {}
if %q not in d:
    sys.exit(0)
del d[%q]
with open(path, 'w') as f:
    yaml.dump(d, f, default_flow_style=False)
print('removed')
`, podIP, podIP)

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}

	podName := haName + "-0"
	req := clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Command: []string{"python3", "-c", script},
			Stdin:   false,
			Stdout:  true,
			Stderr:  true,
			TTY:     false,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(restConfig, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("failed to create SPDY executor: %w", err)
	}

	var stdout, stderr bytes.Buffer
	if err := exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	}); err != nil {
		return fmt.Errorf("exec failed (stderr: %s): %w", stderr.String(), err)
	}

	if out := stdout.String(); out != "" {
		logf.FromContext(ctx).V(1).Info("ip_bans.yaml unban script output",
			"pod", haName+"-0", "output", out)
	}

	return nil
}
