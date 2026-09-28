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
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	inferenceapi "sigs.k8s.io/gateway-api-inference-extension/api/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	lwsv1 "sigs.k8s.io/lws/api/leaderworkerset/v1"
	schedulingv1beta1 "volcano.sh/apis/pkg/apis/scheduling/v1beta1"

	fusioninferiov1alpha1 "github.com/fusioninfer/fusioninfer/api/core/v1alpha1"
	"github.com/fusioninfer/fusioninfer/test/utils"
)

var k8sClient client.Client

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	logf.SetLogger(zap.New(zap.WriteTo(os.Stderr), zap.UseDevMode(true)))

	// Register all schemes
	for _, addToScheme := range []func(*runtime.Scheme) error{
		fusioninferiov1alpha1.AddToScheme,
		lwsv1.AddToScheme,
		schedulingv1beta1.AddToScheme,
		inferenceapi.Install,
		gatewayv1.Install,
	} {
		if err := addToScheme(scheme.Scheme); err != nil {
			fmt.Fprintf(os.Stderr, "register scheme: %v\n", err)
			return 1
		}
	}

	testEnv := &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "config", "crd", "bases"),
			filepath.Join("..", "..", "config", "crd", "external"), // External CRDs (LWS, InferencePool, etc.)
		},
		ErrorIfCRDPathMissing: false, // Allow missing external CRDs for now
		BinaryAssetsDirectory: utils.LatestEnvTestBinaryDir(filepath.Join("..", "..", "bin", "k8s")),
	}
	cfg, err := testEnv.Start()
	if err != nil {
		fmt.Fprintf(os.Stderr, "start envtest: %v\n", err)
		return 1
	}
	defer func() {
		if err := testEnv.Stop(); err != nil {
			fmt.Fprintf(os.Stderr, "stop envtest: %v\n", err)
		}
	}()

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	if err != nil {
		fmt.Fprintf(os.Stderr, "create client: %v\n", err)
		return 1
	}

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "create manager: %v\n", err)
		return 1
	}
	if err := (&InferenceServiceReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		fmt.Fprintf(os.Stderr, "set up InferenceService controller: %v\n", err)
		return 1
	}

	ctx, cancel := context.WithCancel(context.Background())
	mgrErr := make(chan error, 1)
	go func() {
		mgrErr <- mgr.Start(ctx)
	}()

	code := m.Run()
	cancel()
	if err := <-mgrErr; err != nil {
		fmt.Fprintf(os.Stderr, "manager: %v\n", err)
		return 1
	}
	return code
}
