//go:build e2e
// +build e2e

/*
Copyright 2025.

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

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"github.com/fusioninfer/fusioninfer/test/utils"
)

// namespace where the project is deployed in
const namespace = "fusioninfer-system"

// serviceAccountName created for the project
const serviceAccountName = "fusioninfer-controller-manager"

// metricsServiceName is the name of the metrics service of the project
const metricsServiceName = "fusioninfer-controller-manager-metrics-service"

// metricsRoleBindingName is the name of the RBAC that will be created to allow get the metrics data
const metricsRoleBindingName = "fusioninfer-metrics-binding"

const (
	defaultWaitTimeout  = 2 * time.Minute
	defaultPollInterval = time.Second
)

// waitFor calls condition every defaultPollInterval until it returns nil, and returns the
// last error if that does not happen within timeout.
func waitFor(ctx context.Context, timeout time.Duration, condition func() error) error {
	var lastErr error
	err := wait.For(func(context.Context) (bool, error) {
		lastErr = condition()
		return lastErr == nil, nil
	}, wait.WithContext(ctx), wait.WithTimeout(timeout), wait.WithInterval(defaultPollInterval), wait.WithImmediate())
	if err != nil {
		return errors.Join(err, lastErr)
	}
	return nil
}

func TestManager(t *testing.T) {
	var controllerPodName string

	// Steps that create resources stop the feature with t.Fatal when they fail, because the
	// checks after them depend on those resources. Checks report failures with t.Error so
	// that one failed check does not hide the others.
	manager := features.New("Manager").
		// Before running the tests, set up the environment by creating the namespace,
		// enforce the restricted security policy to the namespace, installing CRDs,
		// and deploying the controller.
		WithSetup("deploy controller-manager", func(ctx context.Context, t *testing.T, _ *envconf.Config) context.Context {
			t.Log("creating manager namespace")
			cmd := exec.Command("kubectl", "create", "ns", namespace)
			if _, err := utils.Run(cmd); err != nil {
				t.Fatalf("Failed to create namespace: %v", err)
			}

			t.Log("labeling the namespace to enforce the restricted security policy")
			cmd = exec.Command("kubectl", "label", "--overwrite", "ns", namespace,
				"pod-security.kubernetes.io/enforce=restricted")
			if _, err := utils.Run(cmd); err != nil {
				t.Fatalf("Failed to label namespace with restricted policy: %v", err)
			}

			t.Log("installing CRDs")
			cmd = exec.Command("make", "install")
			if _, err := utils.Run(cmd); err != nil {
				t.Fatalf("Failed to install CRDs: %v", err)
			}

			t.Log("deploying the controller-manager")
			cmd = exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", projectImage))
			if _, err := utils.Run(cmd); err != nil {
				t.Fatalf("Failed to deploy the controller-manager: %v", err)
			}
			return ctx
		}).
		Assess("should run successfully", func(ctx context.Context, t *testing.T, _ *envconf.Config) context.Context {
			t.Log("validating that the controller-manager pod is running as expected")
			verifyControllerUp := func() error {
				// Get the name of the controller-manager pod
				cmd := exec.Command("kubectl", "get",
					"pods", "-l", "control-plane=controller-manager",
					"-o", "go-template={{ range .items }}"+
						"{{ if not .metadata.deletionTimestamp }}"+
						"{{ .metadata.name }}"+
						"{{ \"\\n\" }}{{ end }}{{ end }}",
					"-n", namespace,
				)

				podOutput, err := utils.Run(cmd)
				if err != nil {
					return fmt.Errorf("failed to retrieve controller-manager pod information: %w", err)
				}
				podNames := utils.GetNonEmptyLines(podOutput)
				if len(podNames) != 1 {
					return fmt.Errorf("expected 1 controller pod running, got %d: %v", len(podNames), podNames)
				}
				controllerPodName = podNames[0]
				if !strings.Contains(controllerPodName, "controller-manager") {
					return fmt.Errorf("unexpected controller pod name %q", controllerPodName)
				}

				// Validate the pod's status
				cmd = exec.Command("kubectl", "get",
					"pods", controllerPodName, "-o", "jsonpath={.status.phase}",
					"-n", namespace,
				)
				output, err := utils.Run(cmd)
				if err != nil {
					return err
				}
				if output != "Running" {
					return fmt.Errorf("incorrect controller-manager pod status %q", output)
				}
				return nil
			}
			if err := waitFor(ctx, defaultWaitTimeout, verifyControllerUp); err != nil {
				t.Errorf("controller-manager pod is not running: %v", err)
			}
			return ctx
		}).
		Assess("should ensure the metrics endpoint is serving metrics",
			func(ctx context.Context, t *testing.T, _ *envconf.Config) context.Context {
				t.Log("creating a ClusterRoleBinding for the service account to allow access to metrics")
				cmd := exec.Command("kubectl", "create", "clusterrolebinding", metricsRoleBindingName,
					"--clusterrole=fusioninfer-metrics-reader",
					fmt.Sprintf("--serviceaccount=%s:%s", namespace, serviceAccountName),
				)
				if _, err := utils.Run(cmd); err != nil {
					t.Fatalf("Failed to create ClusterRoleBinding: %v", err)
				}

				t.Log("validating that the metrics service is available")
				cmd = exec.Command("kubectl", "get", "service", metricsServiceName, "-n", namespace)
				if _, err := utils.Run(cmd); err != nil {
					t.Errorf("Metrics service should exist: %v", err)
				}

				t.Log("getting the service account token")
				token, err := serviceAccountToken(ctx)
				if err != nil {
					t.Fatalf("Failed to get the service account token: %v", err)
				}
				if token == "" {
					t.Fatal("The service account token is empty")
				}

				t.Log("waiting for the metrics endpoint to be ready")
				verifyMetricsEndpointReady := func() error {
					cmd := exec.Command("kubectl", "get", "endpoints", metricsServiceName, "-n", namespace)
					output, err := utils.Run(cmd)
					if err != nil {
						return err
					}
					if !strings.Contains(output, "8443") {
						return errors.New("metrics endpoint is not ready")
					}
					return nil
				}
				if err := waitFor(ctx, defaultWaitTimeout, verifyMetricsEndpointReady); err != nil {
					t.Error(err)
				}

				t.Log("verifying that the controller manager is serving the metrics server")
				verifyMetricsServerStarted := func() error {
					cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
					output, err := utils.Run(cmd)
					if err != nil {
						return err
					}
					if !strings.Contains(output, "controller-runtime.metrics\tServing metrics server") {
						return errors.New("metrics server not yet started")
					}
					return nil
				}
				if controllerPodName == "" {
					t.Error("Skipping the metrics server check because the controller-manager pod name is unknown")
				} else if err := waitFor(ctx, defaultWaitTimeout, verifyMetricsServerStarted); err != nil {
					t.Error(err)
				}

				t.Log("creating the curl-metrics pod to access the metrics endpoint")
				cmd = exec.Command("kubectl", "run", "curl-metrics", "--restart=Never",
					"--namespace", namespace,
					"--image=curlimages/curl:latest",
					"--overrides",
					fmt.Sprintf(`{
					"spec": {
						"containers": [{
							"name": "curl",
							"image": "curlimages/curl:latest",
							"command": ["/bin/sh", "-c"],
							"args": ["curl -v -k -H 'Authorization: Bearer %s' https://%s.%s.svc.cluster.local:8443/metrics"],
							"securityContext": {
								"readOnlyRootFilesystem": true,
								"allowPrivilegeEscalation": false,
								"capabilities": {
									"drop": ["ALL"]
								},
								"runAsNonRoot": true,
								"runAsUser": 1000,
								"seccompProfile": {
									"type": "RuntimeDefault"
								}
							}
						}],
						"serviceAccountName": "%s"
					}
				}`, token, metricsServiceName, namespace, serviceAccountName))
				if _, err := utils.Run(cmd); err != nil {
					t.Fatalf("Failed to create curl-metrics pod: %v", err)
				}

				t.Log("waiting for the curl-metrics pod to complete.")
				verifyCurlUp := func() error {
					cmd := exec.Command("kubectl", "get", "pods", "curl-metrics",
						"-o", "jsonpath={.status.phase}",
						"-n", namespace)
					output, err := utils.Run(cmd)
					if err != nil {
						return err
					}
					if output != "Succeeded" {
						return fmt.Errorf("curl pod in wrong status %q", output)
					}
					return nil
				}
				if err := waitFor(ctx, 5*time.Minute, verifyCurlUp); err != nil {
					t.Error(err)
				}

				t.Log("getting the metrics by checking curl-metrics logs")
				metricsOutput, err := getMetricsOutput()
				if err != nil {
					t.Error(err)
					return ctx
				}
				if !strings.Contains(metricsOutput, "< HTTP/1.1 200 OK") {
					t.Errorf("Metrics endpoint did not return 200 OK:\n%s", metricsOutput)
				}
				if !strings.Contains(metricsOutput, "controller_runtime_reconcile_total") {
					t.Errorf("Metrics output does not contain controller_runtime_reconcile_total:\n%s", metricsOutput)
				}
				return ctx
			}).
		// After all assessments have been executed, collect debugging information if any of
		// them failed, then clean up by undeploying the controller, uninstalling CRDs, and
		// deleting the namespace. The collection runs here rather than in AfterEachFeature,
		// which e2e-framework runs after the teardown has removed the controller.
		WithTeardown("clean up", func(ctx context.Context, t *testing.T, _ *envconf.Config) context.Context {
			if t.Failed() {
				collectDebugInfo(t, controllerPodName)
			}

			t.Log("cleaning up the curl pod for metrics")
			cmd := exec.Command("kubectl", "delete", "pod", "curl-metrics", "-n", namespace)
			_, _ = utils.Run(cmd)

			t.Log("undeploying the controller-manager")
			cmd = exec.Command("make", "undeploy")
			_, _ = utils.Run(cmd)

			t.Log("uninstalling CRDs")
			cmd = exec.Command("make", "uninstall")
			_, _ = utils.Run(cmd)

			t.Log("removing manager namespace")
			cmd = exec.Command("kubectl", "delete", "ns", namespace)
			_, _ = utils.Run(cmd)
			return ctx
		}).
		Feature()

	testenv.Test(t, manager)
}

// collectDebugInfo logs the controller-manager pod logs and description, the Kubernetes events,
// and the curl-metrics pod logs.
func collectDebugInfo(t *testing.T, controllerPodName string) {
	t.Helper()

	t.Log("Fetching controller manager pod logs")
	cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
	controllerLogs, err := utils.Run(cmd)
	if err == nil {
		t.Logf("Controller logs:\n %s", controllerLogs)
	} else {
		t.Logf("Failed to get Controller logs: %s", err)
	}

	t.Log("Fetching Kubernetes events")
	cmd = exec.Command("kubectl", "get", "events", "-n", namespace, "--sort-by=.lastTimestamp")
	eventsOutput, err := utils.Run(cmd)
	if err == nil {
		t.Logf("Kubernetes events:\n%s", eventsOutput)
	} else {
		t.Logf("Failed to get Kubernetes events: %s", err)
	}

	t.Log("Fetching curl-metrics logs")
	cmd = exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
	metricsOutput, err := utils.Run(cmd)
	if err == nil {
		t.Logf("Metrics logs:\n %s", metricsOutput)
	} else {
		t.Logf("Failed to get curl-metrics logs: %s", err)
	}

	t.Log("Fetching controller manager pod description")
	cmd = exec.Command("kubectl", "describe", "pod", controllerPodName, "-n", namespace)
	podDescription, err := utils.Run(cmd)
	if err == nil {
		t.Logf("Pod description:\n %s", podDescription)
	} else {
		t.Log("Failed to describe controller pod")
	}
}

// serviceAccountToken returns a token for the specified service account in the given namespace.
// It uses the Kubernetes TokenRequest API to generate a token by directly sending a request
// and parsing the resulting token from the API response.
func serviceAccountToken(ctx context.Context) (string, error) {
	const tokenRequestRawString = `{
		"apiVersion": "authentication.k8s.io/v1",
		"kind": "TokenRequest"
	}`

	// Temporary file to store the token request
	secretName := fmt.Sprintf("%s-token-request", serviceAccountName)
	tokenRequestFile := filepath.Join("/tmp", secretName)
	err := os.WriteFile(tokenRequestFile, []byte(tokenRequestRawString), os.FileMode(0o644))
	if err != nil {
		return "", err
	}

	var out string
	verifyTokenCreation := func() error {
		// Execute kubectl command to create the token
		cmd := exec.Command("kubectl", "create", "--raw", fmt.Sprintf(
			"/api/v1/namespaces/%s/serviceaccounts/%s/token",
			namespace,
			serviceAccountName,
		), "-f", tokenRequestFile)

		output, err := cmd.CombinedOutput()
		if err != nil {
			return err
		}

		// Parse the JSON output to extract the token
		var token tokenRequest
		if err := json.Unmarshal(output, &token); err != nil {
			return err
		}

		out = token.Status.Token
		return nil
	}
	err = waitFor(ctx, defaultWaitTimeout, verifyTokenCreation)

	return out, err
}

// getMetricsOutput retrieves and returns the logs from the curl pod used to access the metrics endpoint.
func getMetricsOutput() (string, error) {
	cmd := exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
	metricsOutput, err := utils.Run(cmd)
	if err != nil {
		return "", fmt.Errorf("failed to retrieve logs from curl pod: %w", err)
	}
	return metricsOutput, nil
}

// tokenRequest is a simplified representation of the Kubernetes TokenRequest API response,
// containing only the token field that we need to extract.
type tokenRequest struct {
	Status struct {
		Token string `json:"token"`
	} `json:"status"`
}
