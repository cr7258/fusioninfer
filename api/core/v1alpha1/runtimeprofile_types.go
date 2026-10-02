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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RuntimeBackend selects the inference engine adapter of a runtime.
// +kubebuilder:validation:Enum=vllm;sglang;trtllm
type RuntimeBackend string

const (
	// RuntimeBackendVLLM runs vLLM.
	RuntimeBackendVLLM RuntimeBackend = "vllm"

	// RuntimeBackendSGLang runs SGLang.
	RuntimeBackendSGLang RuntimeBackend = "sglang"

	// RuntimeBackendTRTLLM runs TensorRT-LLM.
	RuntimeBackendTRTLLM RuntimeBackend = "trtllm"
)

// LoRALoadingMode is when a runtime loads the LoRA adapters bound to it.
// +kubebuilder:validation:Enum=preload;dynamic
type LoRALoadingMode string

const (
	// LoRALoadingModePreload mounts every bound adapter before the engine starts. Changing the
	// bindings produces a new workload revision.
	LoRALoadingModePreload LoRALoadingMode = "preload"

	// LoRALoadingModeDynamic loads and unloads adapters in the running engine without restarting it.
	LoRALoadingModeDynamic LoRALoadingMode = "dynamic"
)

// RuntimeProfileSpec declares a reusable inference runtime: the backend, the LoRA loading
// capability, and either an aggregated role or a prefiller and a decoder. It describes one
// logical replica per role and neither sets replica counts nor references a Model.
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable"
// +kubebuilder:validation:XValidation:rule="has(self.aggregated) || has(self.prefiller) || has(self.decoder)",message="set aggregated, or prefiller and decoder"
// +kubebuilder:validation:XValidation:rule="!has(self.aggregated) || (!has(self.prefiller) && !has(self.decoder))",message="aggregated cannot be combined with prefiller or decoder"
// +kubebuilder:validation:XValidation:rule="has(self.prefiller) == has(self.decoder)",message="prefiller and decoder must be set together"
type RuntimeProfileSpec struct {
	// Backend selects the inference engine adapter. All roles use the same backend.
	// +required
	Backend RuntimeBackend `json:"backend"`

	// LoRA declares that the runtime can serve LoRA adapters and how it loads them.
	// When omitted, the runtime does not accept LoRA bindings.
	// +optional
	LoRA *RuntimeLoRASpec `json:"lora,omitempty"`

	// Aggregated declares the role of aggregated inference, where each replica runs both prefill and decode.
	// +optional
	Aggregated *RuntimeComponentSpec `json:"aggregated,omitempty"`

	// Prefiller declares the prefill role of Prefill/Decode disaggregation.
	// +optional
	Prefiller *RuntimeComponentSpec `json:"prefiller,omitempty"`

	// Decoder declares the decode role of Prefill/Decode disaggregation.
	// +optional
	Decoder *RuntimeComponentSpec `json:"decoder,omitempty"`
}

// RuntimeLoRASpec declares how a runtime loads the LoRA adapters that an InferenceDeployment binds.
// All roles of the runtime use the same loading mode.
type RuntimeLoRASpec struct {
	// LoadingMode is when the runtime loads the adapters.
	// +required
	LoadingMode LoRALoadingMode `json:"loadingMode"`

	// MaxLoadedAdapters is the maximum number of adapters that one InferenceDeployment can bind.
	// It is a control-plane limit and does not replace the capacity settings of the engine.
	// +kubebuilder:validation:Minimum=1
	// +required
	MaxLoadedAdapters int32 `json:"maxLoadedAdapters"`
}

// RuntimeComponentSpec declares one role of a runtime: the Pod template of a logical replica and,
// when the replica spans several nodes, the node count.
type RuntimeComponentSpec struct {
	// PodTemplate is the Pod template of the role.
	// +required
	PodTemplate corev1.PodTemplateSpec `json:"podTemplate"`

	// Multinode spreads each logical replica over several nodes: a leader and nodeCount - 1 workers,
	// all created from podTemplate. When omitted, each logical replica is a single Pod.
	// +optional
	Multinode *MultinodeSpec `json:"multinode,omitempty"`
}

// MultinodeSpec declares a logical replica that spans several nodes.
type MultinodeSpec struct {
	// NodeCount is the number of nodes in each logical replica, including the leader.
	// +kubebuilder:validation:Minimum=2
	// +required
	NodeCount int32 `json:"nodeCount"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced
// +genclient

// RuntimeProfile declares a namespaced, reusable inference runtime template.
type RuntimeProfile struct {
	metav1.TypeMeta `json:",inline"`

	// Metadata is the standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// Spec declares the runtime.
	// +required
	Spec RuntimeProfileSpec `json:"spec"`
}

// +kubebuilder:object:root=true

// RuntimeProfileList contains a list of RuntimeProfile objects.
type RuntimeProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RuntimeProfile `json:"items"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// +genclient
// +genclient:nonNamespaced

// ClusterRuntimeProfile declares a cluster-scoped inference runtime template that InferenceDeployments
// in every namespace can use.
type ClusterRuntimeProfile struct {
	metav1.TypeMeta `json:",inline"`

	// Metadata is the standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// Spec declares the runtime.
	// +required
	Spec RuntimeProfileSpec `json:"spec"`
}

// +kubebuilder:object:root=true

// ClusterRuntimeProfileList contains a list of ClusterRuntimeProfile objects.
type ClusterRuntimeProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterRuntimeProfile `json:"items"`
}

func init() {
	SchemeBuilder.Register(
		&RuntimeProfile{},
		&RuntimeProfileList{},
		&ClusterRuntimeProfile{},
		&ClusterRuntimeProfileList{},
	)
}
