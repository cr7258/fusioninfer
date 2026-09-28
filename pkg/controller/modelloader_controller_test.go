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
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	fusioninferiov1alpha1 "github.com/fusioninfer/fusioninfer/api/core/v1alpha1"
)

func TestModelLoaderReconcile(t *testing.T) {
	t.Run("should successfully reconcile the resource", func(t *testing.T) {
		ctx := t.Context()
		const resourceName = "test-resource"
		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}

		t.Log("creating the custom resource for the Kind ModelLoader")
		modelloader := &fusioninferiov1alpha1.ModelLoader{}
		err := k8sClient.Get(ctx, typeNamespacedName, modelloader)
		if err != nil && apierrors.IsNotFound(err) {
			resource := &fusioninferiov1alpha1.ModelLoader{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: "default",
				},
			}
			if err := k8sClient.Create(ctx, resource); err != nil {
				t.Fatalf("create ModelLoader: %v", err)
			}
		}
		t.Cleanup(func() {
			resource := &fusioninferiov1alpha1.ModelLoader{}
			if err := k8sClient.Get(context.Background(), typeNamespacedName, resource); err != nil {
				t.Errorf("get ModelLoader: %v", err)
				return
			}
			t.Log("Cleanup the specific resource instance ModelLoader")
			if err := k8sClient.Delete(context.Background(), resource); err != nil {
				t.Errorf("delete ModelLoader: %v", err)
			}
		})

		t.Log("Reconciling the created resource")
		controllerReconciler := &ModelLoaderReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
		}

		_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: typeNamespacedName,
		})
		if err != nil {
			t.Fatalf("reconcile: %v", err)
		}
	})
}
