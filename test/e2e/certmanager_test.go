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
	"time"

	appsv1 "k8s.io/api/apps/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/e2e-framework/klient/decoder"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
)

const (
	certManagerVersion   = "v1.18.2"
	certManagerNamespace = "cert-manager"
	certManagerURLTmpl   = "https://github.com/cert-manager/cert-manager/releases/download/%s/cert-manager.yaml"
)

// certManagerManifest is the URL of the CertManager release manifest.
var certManagerManifest = fmt.Sprintf(certManagerURLTmpl, certManagerVersion)

// installCertManager creates the objects in the CertManager manifest and waits until its webhook
// is available, which can take a while if CertManager was reinstalled after an uninstall.
func installCertManager(ctx context.Context, cfg *envconf.Config) error {
	r := cfg.Client().Resources()
	if err := decoder.DecodeURL(ctx, certManagerManifest, decoder.CreateIgnoreAlreadyExists(r)); err != nil {
		return err
	}
	webhook := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "cert-manager-webhook", Namespace: certManagerNamespace},
	}
	return wait.For(
		conditions.New(r).DeploymentConditionMatch(webhook, appsv1.DeploymentAvailable, corev1.ConditionTrue),
		wait.WithContext(ctx), wait.WithTimeout(5*time.Minute), wait.WithInterval(defaultPollInterval),
	)
}

// uninstallCertManager deletes the objects in the CertManager manifest and the leader election
// leases that CertManager leaves in kube-system.
func uninstallCertManager(ctx context.Context, cfg *envconf.Config) error {
	r := cfg.Client().Resources()
	err := decoder.DecodeURL(ctx, certManagerManifest,
		decoder.IgnoreErrorHandler(decoder.DeleteHandler(r), apierrors.IsNotFound))
	for _, name := range []string{"cert-manager-cainjector-leader-election", "cert-manager-controller"} {
		lease := &coordinationv1.Lease{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: metav1.NamespaceSystem}}
		if deleteErr := r.Delete(ctx, lease); deleteErr != nil && !apierrors.IsNotFound(deleteErr) {
			err = errors.Join(err, deleteErr)
		}
	}
	return err
}

// isCertManagerInstalled reports whether any of the CertManager CRDs exists.
func isCertManagerInstalled(ctx context.Context, cfg *envconf.Config) (bool, error) {
	r := cfg.Client().Resources()
	for _, name := range []string{
		"certificates.cert-manager.io",
		"issuers.cert-manager.io",
		"clusterissuers.cert-manager.io",
		"certificaterequests.cert-manager.io",
		"orders.acme.cert-manager.io",
		"challenges.acme.cert-manager.io",
	} {
		crd := &unstructured.Unstructured{}
		crd.SetGroupVersionKind(schema.GroupVersionKind{
			Group: "apiextensions.k8s.io", Version: "v1", Kind: "CustomResourceDefinition",
		})
		err := r.Get(ctx, name, "", crd)
		if err == nil {
			return true, nil
		}
		if !apierrors.IsNotFound(err) {
			return false, err
		}
	}
	return false, nil
}
