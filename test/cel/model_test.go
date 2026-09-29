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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	modelAPIVersion = "fusioninfer.io/v1alpha1"

	// Valid sources that the test objects start from.
	hfModelURI   = "hf://Qwen/Qwen3-8B"
	s3AdapterURI = "s3://team-a-models/adapters/qwen3-8b-finance"
	ociModelURI  = "oci://registry.example.com/models/qwen3-8b"
	commitSHA    = "b968826d9c46dd6066d109eabc6255188de91218"
	ociDigest    = "sha256:9d2e6b4a8f1c30573a7e9c2d5b608f14e1d4a7c3096b2f855c8e1a6d4f703b29"

	// Messages of the URI validation rules; the tests match them as substrings of the API error.
	msgScheme      = "uri must use a supported lowercase scheme"
	msgCharacters  = "uri must use valid URI characters"
	msgQuery       = "uri must not contain a query string or fragment"
	msgDotSegments = "uri must not contain dot path segments"
	msgPercent     = "uri must not percent-encode dots or slashes"
	msgHF          = "hf uri must be hf://<owner>/<repo>"
	msgS3          = "s3 uri must be s3://<bucket>/<prefix>"
	msgOCI         = "oci uri must be oci://<registry>/<repository>"
)

// modelObject builds an unstructured Model or ClusterModel, so the tests can send missing or
// malformed fields that the typed structs cannot express.
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

// sourceSpec returns a spec that only sets source.uri.
func sourceSpec(uri string) map[string]any {
	return map[string]any{"source": map[string]any{"uri": uri}}
}

// credentialsSpec returns a spec with a valid URI and the given credentialsRef.
func credentialsSpec(credentialsRef map[string]any) map[string]any {
	return map[string]any{
		"source": map[string]any{"uri": hfModelURI, "credentialsRef": credentialsRef},
	}
}

// prefetchSpec returns a spec with a valid URI and the given prefetch.
func prefetchSpec(prefetch map[string]any) map[string]any {
	spec := sourceSpec(hfModelURI)
	spec["prefetch"] = prefetch
	return spec
}

// loraSpec returns a LoRA adapter spec that references baseModelRef.
func loraSpec(baseModelRef map[string]any) map[string]any {
	spec := sourceSpec(s3AdapterURI)
	spec["lora"] = map[string]any{"baseModelRef": baseModelRef}
	return spec
}

// createModel creates object, fails the test if the API server rejects it, and deletes it when
// the test ends.
func createModel(t *testing.T, object *unstructured.Unstructured) {
	t.Helper()
	if err := k8sClient.Create(t.Context(), object); err != nil {
		t.Fatalf("create %s %q: %v", object.GetKind(), object.GetName(), err)
	}
	// t.Context is canceled before cleanups run, so delete with a fresh context.
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

// setField returns a mutation that sets the field at the given path to value.
func setField(value any, fields ...string) func(*unstructured.Unstructured) error {
	return func(object *unstructured.Unstructured) error {
		return unstructured.SetNestedField(object.Object, value, fields...)
	}
}

// removeField returns a mutation that deletes the field at the given path.
func removeField(fields ...string) func(*unstructured.Unstructured) error {
	return func(object *unstructured.Unstructured) error {
		unstructured.RemoveNestedField(object.Object, fields...)
		return nil
	}
}

// expectInvalid checks that the API server rejected the request as Invalid and mentioned message.
func expectInvalid(t *testing.T, err error, message string) {
	t.Helper()
	if !apierrors.IsInvalid(err) {
		t.Errorf("expected an Invalid error, got %v", err)
		return
	}
	if !strings.Contains(err.Error(), message) {
		t.Errorf("expected the error to contain %q, got %v", message, err)
	}
}

// TestModelAcceptsSupportedSourceURIs checks that valid hf, s3 and oci URIs are accepted.
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

// TestModelRejectsInvalidSourceURIs checks that each URI rule rejects a malformed URI with its own message.
func TestModelRejectsInvalidSourceURIs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		desc    string
		name    string
		uri     string
		message string
	}{
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

// TestModelAcceptsCredentialsAndPrefetch checks the valid forms of credentialsRef and prefetch.
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

// TestModelRejectsInvalidDeclarations checks the name patterns of credentialsRef, prefetch and lora,
// and the CEL rule that makes the prefetch targets mutually exclusive.
func TestModelRejectsInvalidDeclarations(t *testing.T) {
	t.Parallel()
	tests := []struct {
		desc    string
		name    string
		spec    map[string]any
		message string
	}{
		{"invalid credential name", "bad-credentials-invalid",
			credentialsSpec(map[string]any{"name": "Not Valid"}), "spec.source.credentialsRef.name"},
		{"prefetch with nodeSelector and nodeName", "bad-prefetch-both", prefetchSpec(map[string]any{
			"nodeSelector": map[string]any{"node.kubernetes.io/instance-type": "gpu-h100"},
			"nodeName":     "gpu-node-1",
		}), "nodeSelector and nodeName are mutually exclusive"},
		{"prefetch with an invalid nodeName", "bad-prefetch-node",
			prefetchSpec(map[string]any{"nodeName": "Not Valid"}), "spec.prefetch.nodeName"},
		{"invalid LoRA reference name", "bad-lora-invalid",
			loraSpec(map[string]any{"kind": "Model", "name": "not/a/name"}), "spec.lora.baseModelRef.name"},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			t.Parallel()
			object := modelObject("Model", tt.name, "default", tt.spec)
			expectInvalid(t, k8sClient.Create(t.Context(), object), tt.message)
		})
	}
}

// TestModelAcceptsNamespacedLoRAReferences checks that a namespaced LoRA can reference a Model or a ClusterModel.
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

// TestClusterModelLoRAMustReferenceClusterModel checks that a cluster-scoped LoRA can only reference a ClusterModel.
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

// TestModelUpdatesKeepURIAndLoRAImmutable checks which fields can change after creation:
// metadata, credentialsRef and prefetch can; uri and lora cannot.
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

			// Updates that must succeed, applied to base in order.
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
					t.Errorf("update %s: %v", update.desc, err)
				}
			}

			// Updates that the immutability rules must reject.
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
