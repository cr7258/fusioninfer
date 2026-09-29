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

package cel

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	fusioninferiov1alpha1 "github.com/fusioninfer/fusioninfer/api/core/v1alpha1"
)

const (
	vllmImage   = "vllm/vllm-openai:v0.27.1"
	sglangImage = "lmsysorg/sglang:v0.5.4"

	// Messages of the RuntimeProfile validation rules.
	msgNoRole          = "set aggregated, or prefiller and decoder"
	msgAggregatedAndPD = "aggregated cannot be combined with prefiller or decoder"
	msgPDTogether      = "prefiller and decoder must be set together"
	msgImmutableSpec   = "spec is immutable"
)

// runtimeProfileObject returns a RuntimeProfile in the default namespace or a cluster-scoped
// ClusterRuntimeProfile.
func runtimeProfileObject(kind, name string, spec fusioninferiov1alpha1.RuntimeProfileSpec) client.Object {
	switch kind {
	case "RuntimeProfile":
		return &fusioninferiov1alpha1.RuntimeProfile{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			Spec:       spec,
		}
	case "ClusterRuntimeProfile":
		return &fusioninferiov1alpha1.ClusterRuntimeProfile{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec:       spec,
		}
	default:
		panic(fmt.Sprintf("unknown kind %q", kind))
	}
}

// profileSpecOf returns the spec of a RuntimeProfile or ClusterRuntimeProfile, which share
// RuntimeProfileSpec.
func profileSpecOf(object client.Object) *fusioninferiov1alpha1.RuntimeProfileSpec {
	switch object := object.(type) {
	case *fusioninferiov1alpha1.RuntimeProfile:
		return &object.Spec
	case *fusioninferiov1alpha1.ClusterRuntimeProfile:
		return &object.Spec
	default:
		panic(fmt.Sprintf("%T is not a RuntimeProfile or ClusterRuntimeProfile", object))
	}
}

// engineRole returns a role whose Pod template runs image in an engine container with an http port.
func engineRole(image string) *fusioninferiov1alpha1.RuntimeComponentSpec {
	template, err := json.Marshal(corev1.PodTemplateSpec{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:  "engine",
				Image: image,
				Args:  []string{"$(FUSION_MODEL_PATH)"},
				Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: 8000}},
			}},
		},
	})
	if err != nil {
		panic(err)
	}
	return &fusioninferiov1alpha1.RuntimeComponentSpec{PodTemplate: runtime.RawExtension{Raw: template}}
}

// multinodeRole returns an engine role whose logical replica spans nodeCount nodes.
func multinodeRole(image string, nodeCount int32) *fusioninferiov1alpha1.RuntimeComponentSpec {
	role := engineRole(image)
	role.Multinode = &fusioninferiov1alpha1.MultinodeSpec{NodeCount: nodeCount}
	return role
}

// aggregatedSpec returns a vLLM runtime with a single-node aggregated role.
func aggregatedSpec() fusioninferiov1alpha1.RuntimeProfileSpec {
	return fusioninferiov1alpha1.RuntimeProfileSpec{
		Backend:    fusioninferiov1alpha1.RuntimeBackendVLLM,
		Aggregated: engineRole(vllmImage),
	}
}

// disaggregatedSpec returns a vLLM runtime with single-node prefiller and decoder roles.
func disaggregatedSpec() fusioninferiov1alpha1.RuntimeProfileSpec {
	return fusioninferiov1alpha1.RuntimeProfileSpec{
		Backend:   fusioninferiov1alpha1.RuntimeBackendVLLM,
		Prefiller: engineRole(vllmImage),
		Decoder:   engineRole(vllmImage),
	}
}

// withLoRA returns spec with the given LoRA loading mode and adapter limit.
func withLoRA(
	spec fusioninferiov1alpha1.RuntimeProfileSpec, mode fusioninferiov1alpha1.LoRALoadingMode, maxLoadedAdapters int32,
) fusioninferiov1alpha1.RuntimeProfileSpec {
	spec.LoRA = &fusioninferiov1alpha1.RuntimeLoRASpec{LoadingMode: mode, MaxLoadedAdapters: maxLoadedAdapters}
	return spec
}

// TestRuntimeProfileAcceptsSupportedRuntimes checks the valid combinations of backend, roles,
// multinode and LoRA loading.
func TestRuntimeProfileAcceptsSupportedRuntimes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		desc string
		name string
		spec fusioninferiov1alpha1.RuntimeProfileSpec
	}{
		{"aggregated", "profile-aggregated", aggregatedSpec()},
		{"prefill/decode", "profile-pd", disaggregatedSpec()},
		{"multinode aggregated", "profile-multinode", fusioninferiov1alpha1.RuntimeProfileSpec{
			Backend:    fusioninferiov1alpha1.RuntimeBackendVLLM,
			Aggregated: multinodeRole(vllmImage, 4),
		}},
		{"multinode prefill/decode", "profile-multinode-pd", fusioninferiov1alpha1.RuntimeProfileSpec{
			Backend:   fusioninferiov1alpha1.RuntimeBackendSGLang,
			Prefiller: multinodeRole(sglangImage, 2),
			Decoder:   engineRole(sglangImage),
		}},
		{"preload LoRA", "profile-lora-preload",
			withLoRA(aggregatedSpec(), fusioninferiov1alpha1.LoRALoadingModePreload, 4)},
		{"dynamic LoRA", "profile-lora-dynamic",
			withLoRA(disaggregatedSpec(), fusioninferiov1alpha1.LoRALoadingModeDynamic, 8)},
		{"TensorRT-LLM", "profile-trtllm", fusioninferiov1alpha1.RuntimeProfileSpec{
			Backend:    fusioninferiov1alpha1.RuntimeBackendTRTLLM,
			Aggregated: engineRole("nvcr.io/nvidia/tensorrt-llm/release:1.0.0"),
		}},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			t.Parallel()
			createObject(t, runtimeProfileObject("RuntimeProfile", tt.name, tt.spec))
		})
	}
}

// TestRuntimeProfileRejectsInvalidRoles checks that a runtime sets either aggregated or both
// prefiller and decoder.
func TestRuntimeProfileRejectsInvalidRoles(t *testing.T) {
	t.Parallel()
	tests := []struct {
		desc    string
		name    string
		spec    fusioninferiov1alpha1.RuntimeProfileSpec
		message string
	}{
		{"no role", "bad-roles-none",
			fusioninferiov1alpha1.RuntimeProfileSpec{Backend: fusioninferiov1alpha1.RuntimeBackendVLLM}, msgNoRole},
		{"prefiller without decoder", "bad-roles-prefiller", fusioninferiov1alpha1.RuntimeProfileSpec{
			Backend:   fusioninferiov1alpha1.RuntimeBackendVLLM,
			Prefiller: engineRole(vllmImage),
		}, msgPDTogether},
		{"decoder without prefiller", "bad-roles-decoder", fusioninferiov1alpha1.RuntimeProfileSpec{
			Backend: fusioninferiov1alpha1.RuntimeBackendVLLM,
			Decoder: engineRole(vllmImage),
		}, msgPDTogether},
		{"aggregated with prefiller and decoder", "bad-roles-all", fusioninferiov1alpha1.RuntimeProfileSpec{
			Backend:    fusioninferiov1alpha1.RuntimeBackendVLLM,
			Aggregated: engineRole(vllmImage),
			Prefiller:  engineRole(vllmImage),
			Decoder:    engineRole(vllmImage),
		}, msgAggregatedAndPD},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			t.Parallel()
			object := runtimeProfileObject("RuntimeProfile", tt.name, tt.spec)
			expectInvalid(t, k8sClient.Create(t.Context(), object), tt.message)
		})
	}
}

// TestRuntimeProfileSpecIsImmutable checks that metadata can change after creation but spec cannot.
func TestRuntimeProfileSpecIsImmutable(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"RuntimeProfile", "ClusterRuntimeProfile"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			name := "immutable-" + strings.ToLower(kind)
			createObject(t, runtimeProfileObject(kind, name, aggregatedSpec()))

			latest := runtimeProfileObject(kind, name, fusioninferiov1alpha1.RuntimeProfileSpec{})
			if err := updateObject(ctx, latest, func(object client.Object) {
				object.SetLabels(map[string]string{"fusioninfer.io/test": "metadata-update"})
			}); err != nil {
				t.Errorf("update metadata: %v", err)
			}

			rejected := []struct {
				desc   string
				mutate func(client.Object)
			}{
				{"backend", func(object client.Object) {
					profileSpecOf(object).Backend = fusioninferiov1alpha1.RuntimeBackendSGLang
				}},
				{"lora", func(object client.Object) {
					profileSpecOf(object).LoRA = &fusioninferiov1alpha1.RuntimeLoRASpec{
						LoadingMode:       fusioninferiov1alpha1.LoRALoadingModeDynamic,
						MaxLoadedAdapters: 4,
					}
				}},
				{"multinode", func(object client.Object) {
					profileSpecOf(object).Aggregated.Multinode = &fusioninferiov1alpha1.MultinodeSpec{NodeCount: 2}
				}},
				{"podTemplate", func(object client.Object) {
					profileSpecOf(object).Aggregated = engineRole("vllm/vllm-openai:v0.28.0")
				}},
				{"roles", func(object client.Object) {
					spec := profileSpecOf(object)
					spec.Aggregated, spec.Prefiller, spec.Decoder = nil, engineRole(vllmImage), engineRole(vllmImage)
				}},
			}
			for _, update := range rejected {
				t.Run(update.desc, func(t *testing.T) {
					latest := runtimeProfileObject(kind, name, fusioninferiov1alpha1.RuntimeProfileSpec{})
					expectInvalid(t, updateObject(ctx, latest, update.mutate), msgImmutableSpec)
				})
			}
		})
	}
}
