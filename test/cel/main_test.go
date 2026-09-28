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

// Package cel tests the OpenAPI schema and CEL validation rules of the FusionInfer CRDs
// against a real API server.
package cel

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	fusioninferiov1alpha1 "github.com/fusioninfer/fusioninfer/api/core/v1alpha1"
	"github.com/fusioninfer/fusioninfer/test/utils"
)

var k8sClient client.Client

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	scheme := runtime.NewScheme()
	if err := fusioninferiov1alpha1.AddToScheme(scheme); err != nil {
		fmt.Fprintf(os.Stderr, "register scheme: %v\n", err)
		return 1
	}

	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
		BinaryAssetsDirectory: utils.FirstEnvTestBinaryDir(filepath.Join("..", "..", "bin", "k8s")),
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

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		fmt.Fprintf(os.Stderr, "create client: %v\n", err)
		return 1
	}

	return m.Run()
}
