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
	"context"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	fusioninferiov1alpha1 "github.com/fusioninfer/fusioninfer/api/core/v1alpha1"
)

const (
	modelAPIVersion = "fusioninfer.io/v1alpha1"
	hfModelURI      = "hf://Qwen/Qwen3-8B"
	s3AdapterURI    = "s3://team-a-models/adapters/qwen3-8b-finance"
	ociModelURI     = "oci://registry.example.com/models/qwen3-8b"
	commitSHA       = "b968826d9c46dd6066d109eabc6255188de91218"
	ociDigest       = "sha256:9d2e6b4a8f1c30573a7e9c2d5b608f14e1d4a7c3096b2f855c8e1a6d4f703b29"

	msgScheme      = "uri must use a supported lowercase scheme"
	msgCharacters  = "uri must use valid URI characters"
	msgQuery       = "uri must not contain a query string or fragment"
	msgDotSegments = "uri must not contain dot path segments"
	msgPercent     = "uri must not percent-encode dots or slashes"
	msgHF          = "hf uri must be hf://<owner>/<repo>"
	msgS3          = "s3 uri must be s3://<bucket>/<prefix>"
	msgOCI         = "oci uri must be oci://<registry>/<repository>"
)

func modelObject(kind, name, namespace string, spec map[string]any) *unstructured.Unstructured {
	object := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": modelAPIVersion,
			"kind":       kind,
			"metadata": map[string]any{
				"name": name,
			},
			"spec": spec,
		},
	}
	object.SetNamespace(namespace)
	return object
}

func sourceSpec(uri string) map[string]any {
	return map[string]any{"source": map[string]any{"uri": uri}}
}

func credentialsSpec(credentialsRef map[string]any) map[string]any {
	return map[string]any{
		"source": map[string]any{"uri": hfModelURI, "credentialsRef": credentialsRef},
	}
}

func prefetchSpec(prefetch map[string]any) map[string]any {
	spec := sourceSpec(hfModelURI)
	spec["prefetch"] = prefetch
	return spec
}

func loraSpec(baseModelRef map[string]any) map[string]any {
	spec := sourceSpec(s3AdapterURI)
	spec["lora"] = map[string]any{"baseModelRef": baseModelRef}
	return spec
}

func createModel(t *testing.T, object *unstructured.Unstructured) {
	t.Helper()
	if err := k8sClient.Create(t.Context(), object); err != nil {
		t.Fatalf("create %s %q: %v", object.GetKind(), object.GetName(), err)
	}
	t.Cleanup(func() {
		_ = k8sClient.Delete(context.Background(), object)
	})
}

// updateModel applies mutate to the latest stored copy of object and writes it back.
func updateModel(
	ctx context.Context, object *unstructured.Unstructured, mutate func(*unstructured.Unstructured) error,
) error {
	latest := &unstructured.Unstructured{}
	latest.SetGroupVersionKind(object.GroupVersionKind())
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(object), latest); err != nil {
		return err
	}
	if err := mutate(latest); err != nil {
		return err
	}
	return k8sClient.Update(ctx, latest)
}

func setField(value any, fields ...string) func(*unstructured.Unstructured) error {
	return func(object *unstructured.Unstructured) error {
		return unstructured.SetNestedField(object.Object, value, fields...)
	}
}

func removeField(fields ...string) func(*unstructured.Unstructured) error {
	return func(object *unstructured.Unstructured) error {
		unstructured.RemoveNestedField(object.Object, fields...)
		return nil
	}
}

func expectInvalid(t *testing.T, err error, message string) {
	t.Helper()
	if !apierrors.IsInvalid(err) {
		t.Fatalf("expected an Invalid error, got %v", err)
	}
	if !strings.Contains(err.Error(), message) {
		t.Fatalf("expected the error to contain %q, got %v", message, err)
	}
}

func TestModelAcceptsSupportedSourceURIs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		desc string
		name string
		uri  string
	}{
		{"Hugging Face without a revision", "uri-hf", hfModelURI},
		{"Hugging Face branch", "uri-hf-branch", hfModelURI + "@main"},
		{"Hugging Face commit SHA", "uri-hf-commit", hfModelURI + "@" + commitSHA},
		{"Hugging Face ref with slashes", "uri-hf-ref", hfModelURI + "@refs/pr/1"},
		{"S3", "uri-s3", "s3://team-a-models/base/qwen3-8b"},
		{"S3 key with an encoded space", "uri-s3-encoded", "s3://team-a-models/base/qwen3%208b"},
		{"OCI without a version", "uri-oci", ociModelURI},
		{"OCI tag", "uri-oci-tag", ociModelURI + ":v1"},
		{"OCI digest", "uri-oci-digest", ociModelURI + "@" + ociDigest},
		{"OCI registry with a port", "uri-oci-port", "oci://localhost:5000/qwen3-8b:v1"},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			t.Parallel()
			createModel(t, modelObject("Model", tt.name, "default", sourceSpec(tt.uri)))
		})
	}
}

func TestModelRejectsInvalidSourceURIs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		desc    string
		name    string
		uri     string
		message string
	}{
		{"empty", "bad-uri-empty", "", "spec.source.uri"},
		{"too long", "bad-uri-long", "s3://team-a-models/" + strings.Repeat("a", 2048), "spec.source.uri"},
		{"unsupported scheme", "bad-uri-https", "https://example.com/model", msgScheme},
		{"pvc scheme", "bad-uri-pvc", "pvc://qwen3-weights/models/qwen3-8b", msgScheme},
		{"uppercase scheme", "bad-uri-uppercase", "HF://Qwen/Qwen3-8B", msgScheme},
		{"whitespace", "bad-uri-space", "hf://Qwen Team/Qwen3-8B", msgCharacters},
		{"query string", "bad-uri-query", hfModelURI + "?revision=main", msgQuery},
		{"fragment", "bad-uri-fragment", hfModelURI + "#weights", msgQuery},
		{"dot path segment", "bad-uri-dots", "s3://team-a-models/base/../qwen3-8b", msgDotSegments},
		{"encoded dot segment", "bad-uri-encoded-dots", "s3://team-a-models/base/%2e%2e/qwen3-8b", msgPercent},
		{"encoded slash", "bad-uri-encoded-slash", "s3://team-a-models/base%2Fqwen3-8b", msgPercent},
		{"Hugging Face without owner", "bad-hf-owner", "hf:///Qwen3-8B", msgHF},
		{"Hugging Face with an extra path", "bad-hf-path", hfModelURI + "/extra", msgHF},
		{"Hugging Face with credentials", "bad-hf-credentials", "hf://user:token@Qwen/Qwen3-8B", msgHF},
		{"Hugging Face with an empty revision", "bad-hf-empty-revision", hfModelURI + "@", msgHF},
		{"Hugging Face revision with ..", "bad-hf-revision", hfModelURI + "@v1..v2", msgHF},
		{"S3 without bucket", "bad-s3-bucket", "s3:///base/qwen3-8b", msgS3},
		{"S3 without prefix", "bad-s3-prefix", "s3://team-a-models", msgS3},
		{"S3 with a trailing slash", "bad-s3-slash", "s3://team-a-models/base/qwen3-8b/", msgS3},
		{"S3 with credentials", "bad-s3-credentials", "s3://key:secret@team-a-models/base/qwen3-8b", msgS3},
		{"OCI without repository", "bad-oci-repository", "oci://registry.example.com", msgOCI},
		{"OCI with credentials", "bad-oci-credentials", "oci://user:pass@registry.example.com/qwen3-8b:v1", msgOCI},
		{"OCI uppercase repository", "bad-oci-uppercase", "oci://registry.example.com/Models/Qwen3-8B:v1", msgOCI},
		{"OCI tag and digest", "bad-oci-tag-digest", ociModelURI + ":v1@" + ociDigest, msgOCI},
		{"OCI short digest", "bad-oci-digest", ociModelURI + "@sha256:abc", msgOCI},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			t.Parallel()
			object := modelObject("Model", tt.name, "default", sourceSpec(tt.uri))
			expectInvalid(t, k8sClient.Create(t.Context(), object), tt.message)
		})
	}
}

func TestModelAcceptsCredentialsAndPrefetch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		desc string
		name string
		spec map[string]any
	}{
		{"credentials", "spec-credentials", credentialsSpec(map[string]any{"name": "huggingface-token"})},
		{"prefetch to every node", "spec-prefetch-all", prefetchSpec(map[string]any{})},
		{"prefetch by node selector", "spec-prefetch-selector", prefetchSpec(map[string]any{
			"nodeSelector": map[string]any{"node.kubernetes.io/instance-type": "gpu-h100"},
		})},
		{"prefetch by node name", "spec-prefetch-node", prefetchSpec(map[string]any{"nodeName": "gpu-node-1"})},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			t.Parallel()
			createModel(t, modelObject("Model", tt.name, "default", tt.spec))
		})
	}
}

func TestModelRejectsInvalidDeclarations(t *testing.T) {
	t.Parallel()
	tests := []struct {
		desc    string
		name    string
		spec    map[string]any
		message string
	}{
		{"missing source", "bad-spec-source", map[string]any{}, "spec.source"},
		{"credentials without a name", "bad-credentials-missing",
			credentialsSpec(map[string]any{}), "spec.source.credentialsRef.name"},
		{"empty credential name", "bad-credentials-empty",
			credentialsSpec(map[string]any{"name": ""}), "spec.source.credentialsRef.name"},
		{"invalid credential name", "bad-credentials-invalid",
			credentialsSpec(map[string]any{"name": "Not Valid"}), "spec.source.credentialsRef.name"},
		{"overlong credential name", "bad-credentials-long",
			credentialsSpec(map[string]any{"name": strings.Repeat("a", 254)}), "spec.source.credentialsRef.name"},
		{"prefetch with nodeSelector and nodeName", "bad-prefetch-both", prefetchSpec(map[string]any{
			"nodeSelector": map[string]any{"node.kubernetes.io/instance-type": "gpu-h100"},
			"nodeName":     "gpu-node-1",
		}), "nodeSelector and nodeName are mutually exclusive"},
		{"prefetch with an empty nodeSelector", "bad-prefetch-selector",
			prefetchSpec(map[string]any{"nodeSelector": map[string]any{}}), "spec.prefetch.nodeSelector"},
		{"prefetch with an invalid nodeName", "bad-prefetch-node",
			prefetchSpec(map[string]any{"nodeName": "Not Valid"}), "spec.prefetch.nodeName"},
		{"missing LoRA base model reference", "bad-lora-missing", map[string]any{
			"source": map[string]any{"uri": s3AdapterURI},
			"lora":   map[string]any{},
		}, "spec.lora.baseModelRef"},
		{"invalid LoRA reference kind", "bad-lora-kind",
			loraSpec(map[string]any{"kind": "InferenceService", "name": "qwen3-8b"}), "spec.lora.baseModelRef.kind"},
		{"empty LoRA reference name", "bad-lora-empty",
			loraSpec(map[string]any{"kind": "Model", "name": ""}), "spec.lora.baseModelRef.name"},
		{"invalid LoRA reference name", "bad-lora-invalid",
			loraSpec(map[string]any{"kind": "Model", "name": "not/a/name"}), "spec.lora.baseModelRef.name"},
		{"overlong LoRA reference name", "bad-lora-long",
			loraSpec(map[string]any{"kind": "Model", "name": strings.Repeat("a", 254)}), "spec.lora.baseModelRef.name"},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			t.Parallel()
			object := modelObject("Model", tt.name, "default", tt.spec)
			expectInvalid(t, k8sClient.Create(t.Context(), object), tt.message)
		})
	}
}

func TestModelAcceptsNamespacedLoRAReferences(t *testing.T) {
	t.Parallel()
	tests := []struct {
		desc string
		name string
		kind string
	}{
		{"Model", "lora-model-ref", "Model"},
		{"ClusterModel", "lora-cluster-ref", "ClusterModel"},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			t.Parallel()
			createModel(t, modelObject("Model", tt.name, "default", loraSpec(map[string]any{
				"kind": tt.kind,
				"name": "qwen3-8b",
			})))
		})
	}
}

func TestClusterModelLoRAMustReferenceClusterModel(t *testing.T) {
	t.Parallel()
	createModel(t, modelObject("ClusterModel", "cluster-lora-cluster-ref", "", loraSpec(map[string]any{
		"kind": "ClusterModel",
		"name": "qwen3-8b",
	})))

	object := modelObject("ClusterModel", "cluster-lora-model-ref", "", loraSpec(map[string]any{
		"kind": "Model",
		"name": "qwen3-8b",
	}))
	expectInvalid(t, k8sClient.Create(t.Context(), object), "cluster-scoped LoRA artifacts must reference a ClusterModel")
}

func TestModelUpdatesKeepURIAndLoRAImmutable(t *testing.T) {
	t.Parallel()
	tests := []struct {
		kind      string
		namespace string
	}{
		{"Model", "default"},
		{"ClusterModel", ""},
	}
	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			suffix := strings.ToLower(tt.kind)
			baseRef := map[string]any{"kind": "ClusterModel", "name": "qwen3-8b"}
			base := modelObject(tt.kind, "update-base-"+suffix, tt.namespace, sourceSpec(hfModelURI))
			createModel(t, base)
			adapter := modelObject(tt.kind, "update-lora-"+suffix, tt.namespace, loraSpec(baseRef))
			createModel(t, adapter)

			allowed := []struct {
				desc   string
				mutate func(*unstructured.Unstructured) error
			}{
				{"metadata", func(object *unstructured.Unstructured) error {
					object.SetAnnotations(map[string]string{"fusioninfer.io/test": "metadata-update"})
					return nil
				}},
				{"credentialsRef", setField(map[string]any{"name": "huggingface-token"}, "spec", "source", "credentialsRef")},
				{"prefetch", setField(map[string]any{"nodeName": "gpu-node-1"}, "spec", "prefetch")},
				{"prefetch removal", removeField("spec", "prefetch")},
			}
			for _, update := range allowed {
				if err := updateModel(ctx, base, update.mutate); err != nil {
					t.Fatalf("update %s: %v", update.desc, err)
				}
			}

			rejected := []struct {
				object  *unstructured.Unstructured
				mutate  func(*unstructured.Unstructured) error
				message string
			}{
				{base, setField(hfModelURI+"@main", "spec", "source", "uri"), "uri is immutable"},
				{base, setField(map[string]any{"baseModelRef": baseRef}, "spec", "lora"), "lora cannot be added or removed"},
				{adapter, setField("qwen3-14b", "spec", "lora", "baseModelRef", "name"), "lora is immutable"},
				{adapter, removeField("spec", "lora"), "lora cannot be added or removed"},
			}
			for _, update := range rejected {
				expectInvalid(t, updateModel(ctx, update.object, update.mutate), update.message)
			}
		})
	}
}

func TestModelStatusUpdatesOnlyThroughSubresource(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	model := &fusioninferiov1alpha1.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "model-status", Namespace: "default"},
		Spec: fusioninferiov1alpha1.ModelSpec{
			Source:   fusioninferiov1alpha1.ModelSource{URI: hfModelURI},
			Prefetch: &fusioninferiov1alpha1.PrefetchSpec{},
		},
	}
	if err := k8sClient.Create(ctx, model); err != nil {
		t.Fatalf("create model: %v", err)
	}
	t.Cleanup(func() {
		_ = k8sClient.Delete(context.Background(), model)
	})

	model.Status.ObservedGeneration = model.Generation
	model.Status.Prefetch = &fusioninferiov1alpha1.PrefetchStatus{DesiredNodes: 4, ReadyNodes: 3, FailedNodes: 1}
	meta.SetStatusCondition(&model.Status.Conditions, metav1.Condition{
		Type:    fusioninferiov1alpha1.ModelConditionAccessible,
		Status:  metav1.ConditionTrue,
		Reason:  fusioninferiov1alpha1.ModelReasonVerified,
		Message: "source is accessible",
	})
	if err := k8sClient.Status().Update(ctx, model); err != nil {
		t.Fatalf("update status: %v", err)
	}

	stored := &fusioninferiov1alpha1.Model{}
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(model), stored); err != nil {
		t.Fatalf("get model: %v", err)
	}
	if diff := cmp.Diff(model.Status.Prefetch, stored.Status.Prefetch); diff != "" {
		t.Fatalf("status.prefetch mismatch (-want +got):\n%s", diff)
	}
	if !meta.IsStatusConditionTrue(stored.Status.Conditions, fusioninferiov1alpha1.ModelConditionAccessible) {
		t.Fatalf("condition %s is not True", fusioninferiov1alpha1.ModelConditionAccessible)
	}

	stored.Status = fusioninferiov1alpha1.ModelStatus{}
	stored.Annotations = map[string]string{"fusioninfer.io/test": "status-preserved"}
	if err := k8sClient.Update(ctx, stored); err != nil {
		t.Fatalf("update model: %v", err)
	}
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(model), stored); err != nil {
		t.Fatalf("get model: %v", err)
	}
	if diff := cmp.Diff(model.Status.Prefetch, stored.Status.Prefetch); diff != "" {
		t.Fatalf("status.prefetch changed by a main resource update (-want +got):\n%s", diff)
	}

	stored.Status.Prefetch.ReadyNodes = -1
	expectInvalid(t, k8sClient.Status().Update(ctx, stored), "status.prefetch.readyNodes")
}

func TestModelRejectsObjectWithoutSpec(t *testing.T) {
	t.Parallel()
	object := &unstructured.Unstructured{}
	object.SetAPIVersion(modelAPIVersion)
	object.SetKind("Model")
	object.SetName("model-no-spec")
	object.SetNamespace("default")

	expectInvalid(t, k8sClient.Create(t.Context(), object), "spec")
}
