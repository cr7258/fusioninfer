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
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/e2e-framework/klient/k8s"
	"sigs.k8s.io/e2e-framework/klient/k8s/resources"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
	"sigs.k8s.io/yaml"

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
	// metricsReaderRole is the ClusterRole that grants access to the metrics endpoint.
	metricsReaderRole = "fusioninfer-metrics-reader"
	// metricsPort is the HTTPS port of the metrics endpoint.
	metricsPort = 8443
	// curlPodName is the pod that calls the metrics endpoint from inside the cluster.
	curlPodName = "curl-metrics"
	// controllerSelector selects the controller-manager pods.
	controllerSelector = "control-plane=controller-manager"

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
		// Before running the tests, set up the environment by creating the namespace with the
		// restricted security policy, installing CRDs, and deploying the controller.
		WithSetup("deploy controller-manager", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			t.Log("creating manager namespace with the restricted security policy")
			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
				Name:   namespace,
				Labels: map[string]string{"pod-security.kubernetes.io/enforce": "restricted"},
			}}
			if err := cfg.Client().Resources().Create(ctx, ns); err != nil {
				t.Fatalf("Failed to create namespace: %v", err)
			}

			t.Log("installing CRDs")
			if _, err := utils.Run(exec.Command("make", "install")); err != nil {
				t.Fatalf("Failed to install CRDs: %v", err)
			}

			t.Log("deploying the controller-manager")
			if _, err := utils.Run(exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", projectImage))); err != nil {
				t.Fatalf("Failed to deploy the controller-manager: %v", err)
			}
			return ctx
		}).
		Assess("should run successfully", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			t.Log("validating that the controller-manager pod is running as expected")
			verifyControllerUp := func() error {
				var pods corev1.PodList
				err := cfg.Client().Resources(namespace).List(ctx, &pods, resources.WithLabelSelector(controllerSelector))
				if err != nil {
					return fmt.Errorf("failed to list controller-manager pods: %w", err)
				}
				// Pods being deleted belong to an earlier rollout.
				active := slices.DeleteFunc(pods.Items, func(pod corev1.Pod) bool {
					return pod.DeletionTimestamp != nil
				})
				if len(active) != 1 {
					return fmt.Errorf("expected 1 controller pod running, got %d", len(active))
				}
				controllerPodName = active[0].Name
				if phase := active[0].Status.Phase; phase != corev1.PodRunning {
					return fmt.Errorf("incorrect controller-manager pod status %q", phase)
				}
				return nil
			}
			if err := waitFor(ctx, defaultWaitTimeout, verifyControllerUp); err != nil {
				t.Errorf("controller-manager pod is not running: %v", err)
			}
			return ctx
		}).
		Assess("should ensure the metrics endpoint is serving metrics",
			func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
				r := cfg.Client().Resources()

				t.Log("creating a ClusterRoleBinding for the service account to allow access to metrics")
				binding := &rbacv1.ClusterRoleBinding{
					ObjectMeta: metav1.ObjectMeta{Name: metricsRoleBindingName},
					RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: metricsReaderRole},
					Subjects: []rbacv1.Subject{
						{Kind: rbacv1.ServiceAccountKind, Name: serviceAccountName, Namespace: namespace},
					},
				}
				if err := r.Create(ctx, binding); err != nil {
					t.Fatalf("Failed to create ClusterRoleBinding: %v", err)
				}

				t.Log("validating that the metrics service is available")
				if err := r.Get(ctx, metricsServiceName, namespace, &corev1.Service{}); err != nil {
					t.Errorf("Metrics service should exist: %v", err)
				}

				t.Log("getting the service account token")
				token, err := serviceAccountToken(ctx)
				if err != nil {
					t.Fatalf("Failed to get the service account token: %v", err)
				}

				t.Log("waiting for the metrics endpoint to be ready")
				verifyMetricsEndpointReady := func() error {
					var endpointSlices discoveryv1.EndpointSliceList
					err := cfg.Client().Resources(namespace).List(ctx, &endpointSlices,
						resources.WithLabelSelector(discoveryv1.LabelServiceName+"="+metricsServiceName))
					if err != nil {
						return err
					}
					for _, slice := range endpointSlices.Items {
						servesMetrics := slices.ContainsFunc(slice.Ports, func(port discoveryv1.EndpointPort) bool {
							return ptr.Deref(port.Port, 0) == metricsPort
						})
						ready := slices.ContainsFunc(slice.Endpoints, func(endpoint discoveryv1.Endpoint) bool {
							return ptr.Deref(endpoint.Conditions.Ready, false)
						})
						if servesMetrics && ready {
							return nil
						}
					}
					return errors.New("metrics endpoint is not ready")
				}
				if err := waitFor(ctx, defaultWaitTimeout, verifyMetricsEndpointReady); err != nil {
					t.Error(err)
				}

				t.Log("verifying that the controller manager is serving the metrics server")
				verifyMetricsServerStarted := func() error {
					logs, err := podLogs(ctx, controllerPodName)
					if err != nil {
						return err
					}
					if !strings.Contains(logs, "controller-runtime.metrics\tServing metrics server") {
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
				if err := r.Create(ctx, curlMetricsPod(token)); err != nil {
					t.Fatalf("Failed to create curl-metrics pod: %v", err)
				}

				t.Log("waiting for the curl-metrics pod to complete")
				verifyCurlDone := func() error {
					var pod corev1.Pod
					if err := r.Get(ctx, curlPodName, namespace, &pod); err != nil {
						return err
					}
					if pod.Status.Phase != corev1.PodSucceeded {
						return fmt.Errorf("curl pod in wrong status %q", pod.Status.Phase)
					}
					return nil
				}
				if err := waitFor(ctx, 5*time.Minute, verifyCurlDone); err != nil {
					t.Error(err)
				}

				t.Log("getting the metrics by checking curl-metrics logs")
				metricsOutput, err := podLogs(ctx, curlPodName)
				if err != nil {
					t.Errorf("Failed to retrieve logs from curl pod: %v", err)
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
		// After all assessments have been executed, collect debugging information, then clean
		// up by undeploying the controller, uninstalling CRDs, and deleting the namespace. The
		// collection runs here rather than in AfterEachFeature, which e2e-framework runs after
		// the teardown has removed the controller.
		WithTeardown("clean up", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			collectDebugInfo(ctx, t, cfg, controllerPodName)

			r := cfg.Client().Resources()
			t.Log("cleaning up the curl pod and the metrics ClusterRoleBinding")
			deleteIfExists(ctx, t, r, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: curlPodName, Namespace: namespace}})
			deleteIfExists(ctx, t, r, &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: metricsRoleBindingName}})

			t.Log("undeploying the controller-manager")
			_, _ = utils.Run(exec.Command("make", "undeploy"))

			t.Log("uninstalling CRDs")
			_, _ = utils.Run(exec.Command("make", "uninstall"))

			t.Log("removing manager namespace")
			deleteIfExists(ctx, t, r, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})
			return ctx
		}).
		Feature()

	testenv.Test(t, manager)
}

// curlMetricsPod returns a pod that calls the metrics endpoint with token and prints the response.
func curlMetricsPod(token string) *corev1.Pod {
	url := fmt.Sprintf("https://%s.%s.svc.cluster.local:%d/metrics", metricsServiceName, namespace, metricsPort)
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: curlPodName, Namespace: namespace},
		Spec: corev1.PodSpec{
			ServiceAccountName: serviceAccountName,
			RestartPolicy:      corev1.RestartPolicyNever,
			Containers: []corev1.Container{{
				Name:    "curl",
				Image:   "curlimages/curl:latest",
				Command: []string{"/bin/sh", "-c"},
				Args:    []string{fmt.Sprintf("curl -v -k -H 'Authorization: Bearer %s' %s", token, url)},
				// The namespace enforces the restricted Pod Security Standard.
				SecurityContext: &corev1.SecurityContext{
					ReadOnlyRootFilesystem:   ptr.To(true),
					AllowPrivilegeEscalation: ptr.To(false),
					Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
					RunAsNonRoot:             ptr.To(true),
					RunAsUser:                ptr.To(int64(1000)),
					SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
				},
			}},
		},
	}
}

// serviceAccountToken requests a token for the controller-manager service account through the
// TokenRequest API.
func serviceAccountToken(ctx context.Context) (string, error) {
	var token string
	err := waitFor(ctx, defaultWaitTimeout, func() error {
		request, err := clientset.CoreV1().ServiceAccounts(namespace).CreateToken(
			ctx, serviceAccountName, &authenticationv1.TokenRequest{}, metav1.CreateOptions{})
		if err != nil {
			return err
		}
		if request.Status.Token == "" {
			return errors.New("the TokenRequest API returned an empty token")
		}
		token = request.Status.Token
		return nil
	})
	return token, err
}

// podLogs returns the logs of a pod in the manager namespace.
func podLogs(ctx context.Context, name string) (string, error) {
	logs, err := clientset.CoreV1().Pods(namespace).GetLogs(name, &corev1.PodLogOptions{}).DoRaw(ctx)
	return string(logs), err
}

// deleteIfExists deletes obj and ignores the error if it does not exist.
func deleteIfExists(ctx context.Context, t *testing.T, r *resources.Resources, obj k8s.Object) {
	t.Helper()
	if err := r.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
		t.Logf("Failed to delete %T %q: %v", obj, obj.GetName(), err)
	}
}

// collectDebugInfo gathers the controller-manager pod logs and manifest, the events in the manager
// namespace, and the curl-metrics pod logs. It saves them to artifactsDir when that is set, and logs
// them when the test failed.
func collectDebugInfo(ctx context.Context, t *testing.T, cfg *envconf.Config, controllerPodName string) {
	t.Helper()

	if artifactsDir != "" {
		if err := os.MkdirAll(artifactsDir, 0o755); err != nil {
			t.Logf("Failed to create %s: %v", artifactsDir, err)
		}
	}
	for _, item := range []struct {
		file    string
		collect func() (string, error)
	}{
		{"controller-manager.log", func() (string, error) { return podLogs(ctx, controllerPodName) }},
		{"controller-manager-pod.yaml", func() (string, error) { return podManifest(ctx, cfg, controllerPodName) }},
		{"events.txt", func() (string, error) { return namespaceEvents(ctx, cfg) }},
		{"curl-metrics.log", func() (string, error) { return podLogs(ctx, curlPodName) }},
	} {
		output, err := item.collect()
		if err != nil {
			output = err.Error()
		}
		if t.Failed() {
			t.Logf("%s:\n%s", item.file, output)
		}
		if artifactsDir != "" {
			if err := os.WriteFile(filepath.Join(artifactsDir, item.file), []byte(output), 0o644); err != nil {
				t.Logf("Failed to save %s: %v", item.file, err)
			}
		}
	}
}

// podManifest returns a pod in the manager namespace as YAML, including its status but not its
// managed fields.
func podManifest(ctx context.Context, cfg *envconf.Config, name string) (string, error) {
	var pod corev1.Pod
	if err := cfg.Client().Resources().Get(ctx, name, namespace, &pod); err != nil {
		return "", err
	}
	pod.ManagedFields = nil
	manifest, err := yaml.Marshal(&pod)
	return string(manifest), err
}

// namespaceEvents returns the events in the manager namespace, oldest first, one per line.
func namespaceEvents(ctx context.Context, cfg *envconf.Config) (string, error) {
	var events corev1.EventList
	if err := cfg.Client().Resources(namespace).List(ctx, &events); err != nil {
		return "", err
	}
	slices.SortFunc(events.Items, func(a, b corev1.Event) int {
		return eventTime(a).Compare(eventTime(b))
	})
	var out strings.Builder
	for _, event := range events.Items {
		fmt.Fprintf(&out, "%s\t%s\t%s\t%s/%s\t%s\n", eventTime(event).Format(time.RFC3339), event.Type,
			event.Reason, event.InvolvedObject.Kind, event.InvolvedObject.Name, event.Message)
	}
	return out.String(), nil
}

// eventTime returns when the event last occurred. Events recorded through the events.k8s.io API
// set EventTime instead of LastTimestamp.
func eventTime(event corev1.Event) time.Time {
	switch {
	case !event.LastTimestamp.IsZero():
		return event.LastTimestamp.Time
	case !event.EventTime.IsZero():
		return event.EventTime.Time
	default:
		return event.CreationTimestamp.Time
	}
}
