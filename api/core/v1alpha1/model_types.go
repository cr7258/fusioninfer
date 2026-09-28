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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Condition types reported in Model and ClusterModel status.
const (
	// ModelConditionAccessible reports whether the source can be accessed with the configured credentials.
	ModelConditionAccessible = "Accessible"

	// ModelConditionPrefetched reports whether every node selected by prefetch has downloaded the model.
	ModelConditionPrefetched = "Prefetched"
)

// Condition reasons reported in Model and ClusterModel status.
const (
	// ModelReasonVerified means the source was accessed successfully.
	ModelReasonVerified = "Verified"

	// ModelReasonAuthenticationFailed means the source rejected the configured credentials.
	ModelReasonAuthenticationFailed = "AuthenticationFailed"

	// ModelReasonNotFound means the source or the referenced Secret does not exist.
	ModelReasonNotFound = "NotFound"

	// ModelReasonUnreachable means the source could not be reached.
	ModelReasonUnreachable = "Unreachable"

	// ModelReasonDownloadFailed means at least one node selected by prefetch failed to download the model.
	ModelReasonDownloadFailed = "DownloadFailed"
)

// ModelSpec declares a model artifact and the nodes to prefetch it to.
// +kubebuilder:validation:XValidation:rule="has(self.lora) == has(oldSelf.lora)",message="lora cannot be added or removed"
type ModelSpec struct {
	// Source identifies where the model artifact is stored.
	// +required
	Source ModelSource `json:"source"`

	// LoRA identifies this artifact as a LoRA adapter and names its Base Model.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="lora is immutable"
	// +optional
	LoRA *LoRAArtifactSpec `json:"lora,omitempty"`

	// Prefetch declares the nodes to download the model to before it is needed.
	// When omitted, the model is downloaded on demand.
	// +optional
	Prefetch *PrefetchSpec `json:"prefetch,omitempty"`
}

// ModelSource declares where a model artifact is stored and the Secret used to access it.
type ModelSource struct {
	// URI is the model artifact location. The version, when present, is part of the URI:
	// hf://<owner>/<repo>[@<revision>], s3://<bucket>/<prefix>, or
	// oci://<registry>/<repository>[:<tag>|@sha256:<digest>].
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="uri is immutable"
	// +kubebuilder:validation:XValidation:rule="self.matches('^(hf|s3|oci)://.+$')",message="uri must use a supported lowercase scheme and identify an artifact"
	// +kubebuilder:validation:XValidation:rule="self.matches('^[A-Za-z0-9._~:/@!$&()*+,;=%-]+$')",message="uri must use valid URI characters; encode spaces and non-ASCII characters"
	// +kubebuilder:validation:XValidation:rule="!self.contains('?') && !self.contains('#')",message="uri must not contain a query string or fragment"
	// +kubebuilder:validation:XValidation:rule="!self.contains('/./') && !self.contains('/../') && !self.endsWith('/.') && !self.endsWith('/..')",message="uri must not contain dot path segments"
	// +kubebuilder:validation:XValidation:rule="!self.matches('%2[eEfF]')",message="uri must not percent-encode dots or slashes"
	// +kubebuilder:validation:XValidation:rule="!self.startsWith('hf://') || (self.matches('^hf://[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?/[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?(@[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*)?$') && !self.contains('..'))",message="hf uri must be hf://<owner>/<repo> with an optional @<revision>"
	// +kubebuilder:validation:XValidation:rule="!self.startsWith('s3://') || self.matches('^s3://[^/@:]+/[^/]+(/[^/]+)*$')",message="s3 uri must be s3://<bucket>/<prefix>"
	// +kubebuilder:validation:XValidation:rule="!self.startsWith('oci://') || self.matches('^oci://[A-Za-z0-9.-]+(:[0-9]+)?/[a-z0-9]+(([.]|__|_|-+)[a-z0-9]+)*(/[a-z0-9]+(([.]|__|_|-+)[a-z0-9]+)*)*(:[A-Za-z0-9_][A-Za-z0-9._-]{0,127}|@sha256:[0-9a-f]{64})?$')",message="oci uri must be oci://<registry>/<repository> with an optional :<tag> or @sha256:<digest>"
	// +required
	URI string `json:"uri"`

	// CredentialsRef names the Secret used to access the source.
	// When omitted, the source is accessed anonymously.
	// +optional
	CredentialsRef *SecretReference `json:"credentialsRef,omitempty"`
}

// SecretReference names a Secret that holds credentials for a model source.
// A Model reads the Secret from its own namespace, and a ClusterModel reads it
// from the FusionInfer system namespace.
type SecretReference struct {
	// Name is the name of the Secret.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern="^[a-z0-9]([-a-z0-9]*[a-z0-9])?([.][a-z0-9]([-a-z0-9]*[a-z0-9])?)*$"
	// +required
	Name string `json:"name"`
}

// LoRAArtifactSpec declares the base model required by a LoRA artifact.
type LoRAArtifactSpec struct {
	// BaseModelRef identifies the compatible base model.
	// +required
	BaseModelRef ModelReference `json:"baseModelRef"`
}

// ModelReference identifies a Model or ClusterModel in the fusioninfer.io API group.
type ModelReference struct {
	// Kind is the referenced resource kind.
	// +kubebuilder:validation:Enum=Model;ClusterModel
	// +required
	Kind string `json:"kind"`

	// Name is the referenced resource name.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern="^[a-z0-9]([-a-z0-9]*[a-z0-9])?([.][a-z0-9]([-a-z0-9]*[a-z0-9])?)*$"
	// +required
	Name string `json:"name"`
}

// PrefetchSpec declares the nodes to download a model to before it is needed.
// An empty PrefetchSpec selects every node that runs the model agent.
// +kubebuilder:validation:XValidation:rule="!(has(self.nodeSelector) && has(self.nodeName))",message="nodeSelector and nodeName are mutually exclusive"
type PrefetchSpec struct {
	// NodeSelector selects the nodes whose labels match all of the given labels.
	// +kubebuilder:validation:MinProperties=1
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// NodeName selects a single node by name.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern="^[a-z0-9]([-a-z0-9]*[a-z0-9])?([.][a-z0-9]([-a-z0-9]*[a-z0-9])?)*$"
	// +optional
	NodeName string `json:"nodeName,omitempty"`
}

// ModelStatus reports source accessibility and prefetch progress.
type ModelStatus struct {
	// ObservedGeneration is the most recent generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Prefetch reports download progress on the nodes selected by spec.prefetch.
	// +optional
	Prefetch *PrefetchStatus `json:"prefetch,omitempty"`

	// Conditions represent the latest available observations of the model's state.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// PrefetchStatus reports download progress on the nodes selected by prefetch.
type PrefetchStatus struct {
	// DesiredNodes is the number of nodes selected by prefetch.
	// +kubebuilder:validation:Minimum=0
	// +required
	DesiredNodes int32 `json:"desiredNodes"`

	// ReadyNodes is the number of selected nodes that have downloaded the model.
	// +kubebuilder:validation:Minimum=0
	// +required
	ReadyNodes int32 `json:"readyNodes"`

	// FailedNodes is the number of selected nodes that failed to download the model.
	// +kubebuilder:validation:Minimum=0
	// +required
	FailedNodes int32 `json:"failedNodes"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:subresource:status
// +genclient

// Model declares a namespaced model artifact.
type Model struct {
	metav1.TypeMeta `json:",inline"`

	// Metadata is the standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// Spec declares the model artifact.
	// +required
	Spec ModelSpec `json:"spec"`

	// Status reports source accessibility and prefetch progress.
	// +optional
	Status ModelStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// ModelList contains a list of Model objects.
type ModelList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Model `json:"items"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:subresource:status
// +kubebuilder:validation:XValidation:rule="!has(self.spec.lora) || self.spec.lora.baseModelRef.kind == 'ClusterModel'",message="cluster-scoped LoRA artifacts must reference a ClusterModel"
// +genclient
// +genclient:nonNamespaced

// ClusterModel declares a cluster-scoped model artifact.
type ClusterModel struct {
	metav1.TypeMeta `json:",inline"`

	// Metadata is the standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// Spec declares the model artifact.
	// +required
	Spec ModelSpec `json:"spec"`

	// Status reports source accessibility and prefetch progress.
	// +optional
	Status ModelStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// ClusterModelList contains a list of ClusterModel objects.
type ClusterModelList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterModel `json:"items"`
}

func init() {
	SchemeBuilder.Register(
		&Model{},
		&ModelList{},
		&ClusterModel{},
		&ClusterModelList{},
	)
}
