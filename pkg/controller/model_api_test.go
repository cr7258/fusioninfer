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
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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

func createModel(ctx context.Context, object *unstructured.Unstructured) {
	Expect(k8sClient.Create(ctx, object)).To(Succeed())
	DeferCleanup(func() {
		_ = k8sClient.Delete(ctx, object)
	})
}

// updateModel applies mutate to the latest stored copy of object and writes it back.
func updateModel(
	ctx context.Context, object *unstructured.Unstructured, mutate func(*unstructured.Unstructured),
) error {
	latest := &unstructured.Unstructured{}
	latest.SetGroupVersionKind(object.GroupVersionKind())
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(object), latest); err != nil {
		return err
	}
	mutate(latest)
	return k8sClient.Update(ctx, latest)
}

func setField(value any, fields ...string) func(*unstructured.Unstructured) {
	return func(object *unstructured.Unstructured) {
		Expect(unstructured.SetNestedField(object.Object, value, fields...)).To(Succeed())
	}
}

func removeField(fields ...string) func(*unstructured.Unstructured) {
	return func(object *unstructured.Unstructured) {
		unstructured.RemoveNestedField(object.Object, fields...)
	}
}

func expectInvalid(err error, message string) {
	Expect(err).To(HaveOccurred())
	Expect(apierrors.IsInvalid(err)).To(BeTrue(), "expected an Invalid error, got %v", err)
	Expect(err).To(MatchError(ContainSubstring(message)))
}

var _ = Describe("Model API contract", func() {
	ctx := context.Background()

	DescribeTable("accepts supported source URIs",
		func(name, uri string) {
			createModel(ctx, modelObject("Model", name, "default", sourceSpec(uri)))
		},
		Entry("Hugging Face without a revision", "uri-hf", hfModelURI),
		Entry("Hugging Face branch", "uri-hf-branch", hfModelURI+"@main"),
		Entry("Hugging Face commit SHA", "uri-hf-commit", hfModelURI+"@"+commitSHA),
		Entry("Hugging Face ref with slashes", "uri-hf-ref", hfModelURI+"@refs/pr/1"),
		Entry("S3", "uri-s3", "s3://team-a-models/base/qwen3-8b"),
		Entry("S3 key with an encoded space", "uri-s3-encoded", "s3://team-a-models/base/qwen3%208b"),
		Entry("OCI without a version", "uri-oci", ociModelURI),
		Entry("OCI tag", "uri-oci-tag", ociModelURI+":v1"),
		Entry("OCI digest", "uri-oci-digest", ociModelURI+"@"+ociDigest),
		Entry("OCI registry with a port", "uri-oci-port", "oci://localhost:5000/qwen3-8b:v1"),
	)

	DescribeTable("rejects invalid source URIs",
		func(name, uri, message string) {
			object := modelObject("Model", name, "default", sourceSpec(uri))
			expectInvalid(k8sClient.Create(ctx, object), message)
		},
		Entry("empty", "bad-uri-empty", "", "spec.source.uri"),
		Entry("too long", "bad-uri-long", "s3://team-a-models/"+strings.Repeat("a", 2048), "spec.source.uri"),
		Entry("unsupported scheme", "bad-uri-https", "https://example.com/model", msgScheme),
		Entry("pvc scheme", "bad-uri-pvc", "pvc://qwen3-weights/models/qwen3-8b", msgScheme),
		Entry("uppercase scheme", "bad-uri-uppercase", "HF://Qwen/Qwen3-8B", msgScheme),
		Entry("whitespace", "bad-uri-space", "hf://Qwen Team/Qwen3-8B", msgCharacters),
		Entry("query string", "bad-uri-query", hfModelURI+"?revision=main", msgQuery),
		Entry("fragment", "bad-uri-fragment", hfModelURI+"#weights", msgQuery),
		Entry("dot path segment", "bad-uri-dots", "s3://team-a-models/base/../qwen3-8b", msgDotSegments),
		Entry("encoded dot segment", "bad-uri-encoded-dots", "s3://team-a-models/base/%2e%2e/qwen3-8b", msgPercent),
		Entry("encoded slash", "bad-uri-encoded-slash", "s3://team-a-models/base%2Fqwen3-8b", msgPercent),
		Entry("Hugging Face without owner", "bad-hf-owner", "hf:///Qwen3-8B", msgHF),
		Entry("Hugging Face with an extra path", "bad-hf-path", hfModelURI+"/extra", msgHF),
		Entry("Hugging Face with credentials", "bad-hf-credentials", "hf://user:token@Qwen/Qwen3-8B", msgHF),
		Entry("Hugging Face with an empty revision", "bad-hf-empty-revision", hfModelURI+"@", msgHF),
		Entry("Hugging Face revision with ..", "bad-hf-revision", hfModelURI+"@v1..v2", msgHF),
		Entry("S3 without bucket", "bad-s3-bucket", "s3:///base/qwen3-8b", msgS3),
		Entry("S3 without prefix", "bad-s3-prefix", "s3://team-a-models", msgS3),
		Entry("S3 with a trailing slash", "bad-s3-slash", "s3://team-a-models/base/qwen3-8b/", msgS3),
		Entry("S3 with credentials", "bad-s3-credentials", "s3://key:secret@team-a-models/base/qwen3-8b", msgS3),
		Entry("OCI without repository", "bad-oci-repository", "oci://registry.example.com", msgOCI),
		Entry("OCI with credentials", "bad-oci-credentials", "oci://user:pass@registry.example.com/qwen3-8b:v1", msgOCI),
		Entry("OCI uppercase repository", "bad-oci-uppercase", "oci://registry.example.com/Models/Qwen3-8B:v1", msgOCI),
		Entry("OCI tag and digest", "bad-oci-tag-digest", ociModelURI+":v1@"+ociDigest, msgOCI),
		Entry("OCI short digest", "bad-oci-digest", ociModelURI+"@sha256:abc", msgOCI),
	)

	DescribeTable("accepts credentials and prefetch",
		func(name string, spec map[string]any) {
			createModel(ctx, modelObject("Model", name, "default", spec))
		},
		Entry("credentials", "spec-credentials", credentialsSpec(map[string]any{"name": "huggingface-token"})),
		Entry("prefetch to every node", "spec-prefetch-all", prefetchSpec(map[string]any{})),
		Entry("prefetch by node selector", "spec-prefetch-selector", prefetchSpec(map[string]any{
			"nodeSelector": map[string]any{"node.kubernetes.io/instance-type": "gpu-h100"},
		})),
		Entry("prefetch by node name", "spec-prefetch-node", prefetchSpec(map[string]any{"nodeName": "gpu-node-1"})),
	)

	DescribeTable("rejects invalid model declarations",
		func(name string, spec map[string]any, message string) {
			expectInvalid(k8sClient.Create(ctx, modelObject("Model", name, "default", spec)), message)
		},
		Entry("missing source", "bad-spec-source", map[string]any{}, "spec.source"),
		Entry("credentials without a name", "bad-credentials-missing",
			credentialsSpec(map[string]any{}), "spec.source.credentialsRef.name"),
		Entry("empty credential name", "bad-credentials-empty",
			credentialsSpec(map[string]any{"name": ""}), "spec.source.credentialsRef.name"),
		Entry("invalid credential name", "bad-credentials-invalid",
			credentialsSpec(map[string]any{"name": "Not Valid"}), "spec.source.credentialsRef.name"),
		Entry("overlong credential name", "bad-credentials-long",
			credentialsSpec(map[string]any{"name": strings.Repeat("a", 254)}), "spec.source.credentialsRef.name"),
		Entry("prefetch with nodeSelector and nodeName", "bad-prefetch-both", prefetchSpec(map[string]any{
			"nodeSelector": map[string]any{"node.kubernetes.io/instance-type": "gpu-h100"},
			"nodeName":     "gpu-node-1",
		}), "nodeSelector and nodeName are mutually exclusive"),
		Entry("prefetch with an empty nodeSelector", "bad-prefetch-selector",
			prefetchSpec(map[string]any{"nodeSelector": map[string]any{}}), "spec.prefetch.nodeSelector"),
		Entry("prefetch with an invalid nodeName", "bad-prefetch-node",
			prefetchSpec(map[string]any{"nodeName": "Not Valid"}), "spec.prefetch.nodeName"),
		Entry("missing LoRA base model reference", "bad-lora-missing", map[string]any{
			"source": map[string]any{"uri": s3AdapterURI},
			"lora":   map[string]any{},
		}, "spec.lora.baseModelRef"),
		Entry("invalid LoRA reference kind", "bad-lora-kind",
			loraSpec(map[string]any{"kind": "InferenceService", "name": "qwen3-8b"}), "spec.lora.baseModelRef.kind"),
		Entry("empty LoRA reference name", "bad-lora-empty",
			loraSpec(map[string]any{"kind": "Model", "name": ""}), "spec.lora.baseModelRef.name"),
		Entry("invalid LoRA reference name", "bad-lora-invalid",
			loraSpec(map[string]any{"kind": "Model", "name": "not/a/name"}), "spec.lora.baseModelRef.name"),
		Entry("overlong LoRA reference name", "bad-lora-long",
			loraSpec(map[string]any{"kind": "Model", "name": strings.Repeat("a", 254)}), "spec.lora.baseModelRef.name"),
	)

	DescribeTable("accepts namespaced LoRA references",
		func(name, kind string) {
			createModel(ctx, modelObject("Model", name, "default", loraSpec(map[string]any{
				"kind": kind,
				"name": "qwen3-8b",
			})))
		},
		Entry("Model", "lora-model-ref", "Model"),
		Entry("ClusterModel", "lora-cluster-ref", "ClusterModel"),
	)

	It("requires cluster-scoped LoRA artifacts to reference a ClusterModel", func() {
		createModel(ctx, modelObject("ClusterModel", "cluster-lora-cluster-ref", "", loraSpec(map[string]any{
			"kind": "ClusterModel",
			"name": "qwen3-8b",
		})))

		object := modelObject("ClusterModel", "cluster-lora-model-ref", "", loraSpec(map[string]any{
			"kind": "Model",
			"name": "qwen3-8b",
		}))
		expectInvalid(k8sClient.Create(ctx, object), "cluster-scoped LoRA artifacts must reference a ClusterModel")
	})

	DescribeTable("allows credential and prefetch updates while keeping uri and lora immutable",
		func(kind, namespace string) {
			suffix := strings.ToLower(kind)
			baseRef := map[string]any{"kind": "ClusterModel", "name": "qwen3-8b"}
			base := modelObject(kind, "update-base-"+suffix, namespace, sourceSpec(hfModelURI))
			createModel(ctx, base)
			adapter := modelObject(kind, "update-lora-"+suffix, namespace, loraSpec(baseRef))
			createModel(ctx, adapter)

			Expect(updateModel(ctx, base, func(object *unstructured.Unstructured) {
				object.SetAnnotations(map[string]string{"fusioninfer.io/test": "metadata-update"})
			})).To(Succeed())
			Expect(updateModel(ctx, base, setField(map[string]any{"name": "huggingface-token"},
				"spec", "source", "credentialsRef"))).To(Succeed())
			Expect(updateModel(ctx, base, setField(map[string]any{"nodeName": "gpu-node-1"},
				"spec", "prefetch"))).To(Succeed())
			Expect(updateModel(ctx, base, removeField("spec", "prefetch"))).To(Succeed())

			expectInvalid(updateModel(ctx, base, setField(hfModelURI+"@main", "spec", "source", "uri")),
				"uri is immutable")
			expectInvalid(updateModel(ctx, base, setField(map[string]any{"baseModelRef": baseRef}, "spec", "lora")),
				"lora cannot be added or removed")
			expectInvalid(updateModel(ctx, adapter, setField("qwen3-14b", "spec", "lora", "baseModelRef", "name")),
				"lora is immutable")
			expectInvalid(updateModel(ctx, adapter, removeField("spec", "lora")),
				"lora cannot be added or removed")
		},
		Entry("Model", "Model", "default"),
		Entry("ClusterModel", "ClusterModel", ""),
	)

	It("updates status only through the status subresource", func() {
		model := &fusioninferiov1alpha1.Model{
			ObjectMeta: metav1.ObjectMeta{Name: "model-status", Namespace: "default"},
			Spec: fusioninferiov1alpha1.ModelSpec{
				Source:   fusioninferiov1alpha1.ModelSource{URI: hfModelURI},
				Prefetch: &fusioninferiov1alpha1.PrefetchSpec{},
			},
		}
		Expect(k8sClient.Create(ctx, model)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, model)
		})

		model.Status.ObservedGeneration = model.Generation
		model.Status.Prefetch = &fusioninferiov1alpha1.PrefetchStatus{DesiredNodes: 4, ReadyNodes: 3, FailedNodes: 1}
		meta.SetStatusCondition(&model.Status.Conditions, metav1.Condition{
			Type:    fusioninferiov1alpha1.ModelConditionAccessible,
			Status:  metav1.ConditionTrue,
			Reason:  fusioninferiov1alpha1.ModelReasonVerified,
			Message: "source is accessible",
		})
		Expect(k8sClient.Status().Update(ctx, model)).To(Succeed())

		stored := &fusioninferiov1alpha1.Model{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(model), stored)).To(Succeed())
		Expect(stored.Status.Prefetch).To(Equal(model.Status.Prefetch))
		Expect(meta.IsStatusConditionTrue(stored.Status.Conditions, fusioninferiov1alpha1.ModelConditionAccessible)).
			To(BeTrue())

		stored.Status = fusioninferiov1alpha1.ModelStatus{}
		stored.Annotations = map[string]string{"fusioninfer.io/test": "status-preserved"}
		Expect(k8sClient.Update(ctx, stored)).To(Succeed())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(model), stored)).To(Succeed())
		Expect(stored.Status.Prefetch).To(Equal(model.Status.Prefetch))

		stored.Status.Prefetch.ReadyNodes = -1
		expectInvalid(k8sClient.Status().Update(ctx, stored), "status.prefetch.readyNodes")
	})

	It("rejects an object without a spec", func() {
		object := &unstructured.Unstructured{}
		object.SetAPIVersion(modelAPIVersion)
		object.SetKind("Model")
		object.SetName("model-no-spec")
		object.SetNamespace("default")

		expectInvalid(k8sClient.Create(ctx, object), "spec")
	})
})
