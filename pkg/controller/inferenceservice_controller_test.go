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

package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	lwsv1 "sigs.k8s.io/lws/api/leaderworkerset/v1"

	fusioninferiov1alpha1 "github.com/fusioninfer/fusioninfer/api/core/v1alpha1"
	"github.com/fusioninfer/fusioninfer/pkg/workload"
)

const (
	reconcileTimeout     = 30 * time.Second
	pollInterval         = 250 * time.Millisecond
	consistentlyDuration = 2 * time.Second
)

// createTemplateRaw creates a runtime.RawExtension from PodTemplateSpec
func createTemplateRaw(template corev1.PodTemplateSpec) *runtime.RawExtension {
	data, err := json.Marshal(template)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal template: %v", err))
	}
	return &runtime.RawExtension{Raw: data}
}

// createTestInferenceService creates a basic InferenceService for testing in the default namespace.
func createTestInferenceService(name string) *fusioninferiov1alpha1.InferenceService {
	template := corev1.PodTemplateSpec{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "vllm",
					Image: "vllm/vllm-openai:v0.13.0",
					Args:  []string{"--model", "Qwen/Qwen3-8B"},
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							"nvidia.com/gpu": resource.MustParse("1"),
						},
					},
				},
			},
		},
	}

	return &fusioninferiov1alpha1.InferenceService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: fusioninferiov1alpha1.InferenceServiceSpec{
			Roles: []fusioninferiov1alpha1.Role{
				{
					Name:          "inference",
					ComponentType: fusioninferiov1alpha1.ComponentTypeWorker,
					Replicas:      ptr.To(int32(1)),
					Template:      createTemplateRaw(template),
				},
			},
		},
	}
}

// createInferenceService creates inferSvc and deletes it when the test finishes.
func createInferenceService(t *testing.T, inferSvc *fusioninferiov1alpha1.InferenceService) {
	t.Helper()
	if err := k8sClient.Create(t.Context(), inferSvc); err != nil {
		t.Fatalf("create InferenceService %q: %v", inferSvc.Name, err)
	}
	t.Cleanup(func() {
		if err := client.IgnoreNotFound(k8sClient.Delete(context.Background(), inferSvc)); err != nil {
			t.Errorf("delete InferenceService %q: %v", inferSvc.Name, err)
		}
	})
}

// eventually retries condition every pollInterval until it returns nil, and fails the
// test with the last error if that does not happen within reconcileTimeout.
func eventually(t *testing.T, condition func() error) {
	t.Helper()
	var lastErr error
	err := wait.PollUntilContextTimeout(t.Context(), pollInterval, reconcileTimeout, true,
		func(context.Context) (bool, error) {
			lastErr = condition()
			return lastErr == nil, nil
		})
	if err != nil {
		t.Fatalf("condition not met within %v: %v", reconcileTimeout, lastErr)
	}
}

// consistently checks condition every pollInterval for consistentlyDuration and fails
// the test as soon as it returns an error.
func consistently(t *testing.T, condition func() error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), consistentlyDuration)
	defer cancel()
	var failure error
	_ = wait.PollUntilContextCancel(ctx, pollInterval, true, func(context.Context) (bool, error) {
		failure = condition()
		return failure != nil, nil
	})
	if failure != nil {
		t.Fatalf("condition stopped holding: %v", failure)
	}
}

func TestInferenceServiceBasicReconciliation(t *testing.T) {
	t.Run("should create LWS when InferenceService is created", func(t *testing.T) {
		ctx := t.Context()
		const resourceName = "test-basic-reconcile"

		t.Log("Creating InferenceService")
		createInferenceService(t, createTestInferenceService(resourceName))

		t.Log("Waiting for LWS to be created")
		lwsKey := types.NamespacedName{Name: resourceName + "-inference-0", Namespace: "default"}
		eventually(t, func() error {
			return k8sClient.Get(ctx, lwsKey, &lwsv1.LeaderWorkerSet{})
		})
	})
}

// TestInferenceServiceSpecHashUpdates verifies that reconciliation triggers on spec changes.
func TestInferenceServiceSpecHashUpdates(t *testing.T) {
	t.Run("should create new LWS when replicas increase", func(t *testing.T) {
		ctx := t.Context()
		inferSvcName := "test-replicas-change"
		typeNamespacedName := types.NamespacedName{Name: inferSvcName, Namespace: "default"}

		t.Log("Creating InferenceService with 1 replica")
		inferSvc := createTestInferenceService(inferSvcName)
		createInferenceService(t, inferSvc)

		t.Log("Waiting for first LWS to be created")
		lwsKey := types.NamespacedName{Name: inferSvcName + "-inference-0", Namespace: "default"}
		createdLWS := &lwsv1.LeaderWorkerSet{}
		eventually(t, func() error {
			return k8sClient.Get(ctx, lwsKey, createdLWS)
		})

		t.Log("Verifying LWS has spec-hash label")
		if createdLWS.Labels[workload.LabelSpecHash] == "" {
			t.Errorf("LWS %q has no %s label", lwsKey.Name, workload.LabelSpecHash)
		}

		t.Log("Increasing replicas to 2")
		eventually(t, func() error {
			if err := k8sClient.Get(ctx, typeNamespacedName, inferSvc); err != nil {
				return err
			}
			inferSvc.Spec.Roles[0].Replicas = ptr.To(int32(2))
			return k8sClient.Update(ctx, inferSvc)
		})

		t.Log("Waiting for second LWS to be created")
		newLWSKey := types.NamespacedName{Name: inferSvcName + "-inference-1", Namespace: "default"}
		eventually(t, func() error {
			return k8sClient.Get(ctx, newLWSKey, &lwsv1.LeaderWorkerSet{})
		})
	})

	t.Run("should update LWS when container image changes", func(t *testing.T) {
		ctx := t.Context()
		inferSvcName := "test-image-change"
		typeNamespacedName := types.NamespacedName{Name: inferSvcName, Namespace: "default"}

		t.Log("Creating InferenceService")
		inferSvc := createTestInferenceService(inferSvcName)
		createInferenceService(t, inferSvc)

		t.Log("Waiting for LWS to be created")
		lwsKey := types.NamespacedName{Name: inferSvcName + "-inference-0", Namespace: "default"}
		createdLWS := &lwsv1.LeaderWorkerSet{}
		eventually(t, func() error {
			return k8sClient.Get(ctx, lwsKey, createdLWS)
		})

		initialSpecHash := createdLWS.Labels[workload.LabelSpecHash]
		if initialSpecHash == "" {
			t.Fatalf("LWS %q has no %s label", lwsKey.Name, workload.LabelSpecHash)
		}

		t.Log("Updating container image")
		eventually(t, func() error {
			if err := k8sClient.Get(ctx, typeNamespacedName, inferSvc); err != nil {
				return err
			}
			// Create new template with updated image
			newTemplate := corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "vllm",
							Image: "vllm/vllm-openai:v0.14.0", // Changed
							Args:  []string{"--model", "Qwen/Qwen3-8B"},
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									"nvidia.com/gpu": resource.MustParse("1"),
								},
							},
						},
					},
				},
			}
			inferSvc.Spec.Roles[0].Template = createTemplateRaw(newTemplate)
			return k8sClient.Update(ctx, inferSvc)
		})

		t.Log("Waiting for LWS spec-hash to change")
		eventually(t, func() error {
			if err := k8sClient.Get(ctx, lwsKey, createdLWS); err != nil {
				return err
			}
			if createdLWS.Labels[workload.LabelSpecHash] == initialSpecHash {
				return fmt.Errorf("spec-hash should change when image changes, still %q", initialSpecHash)
			}
			return nil
		})
	})

	t.Run("should NOT update LWS when only metadata changes", func(t *testing.T) {
		ctx := t.Context()
		inferSvcName := "test-no-spec-change"
		typeNamespacedName := types.NamespacedName{Name: inferSvcName, Namespace: "default"}

		t.Log("Creating InferenceService")
		inferSvc := createTestInferenceService(inferSvcName)
		createInferenceService(t, inferSvc)

		t.Log("Waiting for LWS to be created")
		lwsKey := types.NamespacedName{Name: inferSvcName + "-inference-0", Namespace: "default"}
		createdLWS := &lwsv1.LeaderWorkerSet{}
		eventually(t, func() error {
			return k8sClient.Get(ctx, lwsKey, createdLWS)
		})

		initialSpecHash := createdLWS.Labels[workload.LabelSpecHash]
		initialResourceVersion := createdLWS.ResourceVersion

		t.Log("Updating only InferenceService annotations (not spec)")
		eventually(t, func() error {
			if err := k8sClient.Get(ctx, typeNamespacedName, inferSvc); err != nil {
				return err
			}
			if inferSvc.Annotations == nil {
				inferSvc.Annotations = make(map[string]string)
			}
			inferSvc.Annotations["test-annotation"] = "test-value"
			return k8sClient.Update(ctx, inferSvc)
		})

		t.Log("Verifying LWS was NOT updated")
		consistently(t, func() error {
			if err := k8sClient.Get(ctx, lwsKey, createdLWS); err != nil {
				return err
			}
			if createdLWS.ResourceVersion != initialResourceVersion {
				return fmt.Errorf("LWS ResourceVersion should not change when spec doesn't change: %q -> %q",
					initialResourceVersion, createdLWS.ResourceVersion)
			}
			if createdLWS.Labels[workload.LabelSpecHash] != initialSpecHash {
				return fmt.Errorf("spec-hash should remain the same: %q -> %q",
					initialSpecHash, createdLWS.Labels[workload.LabelSpecHash])
			}
			return nil
		})
	})
}

// TestInferenceServiceSpecChangePropagation verifies that InferenceService spec changes propagate to LWS.
func TestInferenceServiceSpecChangePropagation(t *testing.T) {
	t.Run("should update LWS when InferenceService spec changes", func(t *testing.T) {
		ctx := t.Context()
		inferSvcName := "test-spec-change-triggers-update"
		typeNamespacedName := types.NamespacedName{Name: inferSvcName, Namespace: "default"}

		t.Log("Creating InferenceService")
		inferSvc := createTestInferenceService(inferSvcName)
		createInferenceService(t, inferSvc)

		t.Log("Waiting for LWS to be created")
		lwsKey := types.NamespacedName{Name: inferSvcName + "-inference-0", Namespace: "default"}
		createdLWS := &lwsv1.LeaderWorkerSet{}
		eventually(t, func() error {
			return k8sClient.Get(ctx, lwsKey, createdLWS)
		})

		initialSpecHash := createdLWS.Labels[workload.LabelSpecHash]
		if initialSpecHash == "" {
			t.Fatalf("LWS %q has no %s label", lwsKey.Name, workload.LabelSpecHash)
		}

		t.Log("Modifying InferenceService spec (changing args)")
		eventually(t, func() error {
			if err := k8sClient.Get(ctx, typeNamespacedName, inferSvc); err != nil {
				return err
			}
			// Create new template with different args
			newTemplate := corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "vllm",
							Image: "vllm/vllm-openai:v0.13.0",
							Args:  []string{"--model", "Qwen/Qwen3-8B", "--max-model-len", "4096"}, // Added arg
							Resources: corev1.ResourceRequirements{
								Limits: corev1.ResourceList{
									"nvidia.com/gpu": resource.MustParse("1"),
								},
							},
						},
					},
				},
			}
			inferSvc.Spec.Roles[0].Template = createTemplateRaw(newTemplate)
			return k8sClient.Update(ctx, inferSvc)
		})

		t.Log("Waiting for controller to detect hash mismatch and update LWS")
		eventually(t, func() error {
			if err := k8sClient.Get(ctx, lwsKey, createdLWS); err != nil {
				return err
			}
			// Controller should detect hash mismatch (desired != existing label) and update
			if createdLWS.Labels[workload.LabelSpecHash] == initialSpecHash {
				return fmt.Errorf("controller should update LWS when InferenceService spec changes, spec-hash still %q",
					initialSpecHash)
			}
			return nil
		})
	})
}
