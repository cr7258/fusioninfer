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
	"cmp"
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"

	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/e2e-framework/pkg/env"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/envfuncs"
	"sigs.k8s.io/e2e-framework/support"
	"sigs.k8s.io/e2e-framework/support/kind"

	"github.com/fusioninfer/fusioninfer/test/utils"
)

var (
	// Optional Environment Variables:
	// - E2E_ARTIFACTS_DIR=<dir>: Saves the controller-manager logs and manifest, the
	//   Kubernetes events, and the curl-metrics pod logs to <dir> before the teardown.
	artifactsDir = os.Getenv("E2E_ARTIFACTS_DIR")
	// - KIND_CLUSTER=<name> and KIND=<path>: The Kind cluster to run on, which is created
	//   if it does not exist, and the kind binary. make test-e2e sets both.
	kindCluster = cmp.Or(os.Getenv("KIND_CLUSTER"), "fusioninfer-test-e2e")
	kindBinary  = os.Getenv("KIND")

	// projectImage is the name of the image which will be build and loaded
	// with the code source changes to be tested.
	projectImage = "example.com/fusioninfer:v0.0.1"

	testenv env.Environment
	// clientset reads pod logs and requests service account tokens, which the
	// e2e-framework client does not support.
	clientset kubernetes.Interface
)

// TestMain runs the end-to-end (e2e) test suite for the project. These tests execute in an isolated,
// temporary environment to validate project changes with the purpose of being used in CI jobs.
// The default setup requires Kind and builds/loads the Manager Docker image locally.
func TestMain(m *testing.M) {
	var err error
	testenv, err = env.NewFromFlags()
	if err != nil {
		fmt.Fprintf(os.Stderr, "create test environment: %v\n", err)
		os.Exit(1)
	}
	testenv.Setup(
		useKindCluster,
		buildManagerImage,
		envfuncs.LoadImageToCluster(kindCluster, projectImage),
	)

	fmt.Println("Starting fusioninfer integration test suite")
	os.Exit(testenv.Run(m))
}

// useKindCluster connects the tests to the Kind cluster, creating it if it does not exist, and
// points the make targets at it through KUBECONFIG.
func useKindCluster(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
	var opts []support.ClusterOpts
	if kindBinary != "" {
		opts = append(opts, kind.WithPath(kindBinary))
	}
	ctx, err := envfuncs.CreateClusterWithOpts(kind.NewProvider(), kindCluster, opts...)(ctx, cfg)
	if err != nil {
		return ctx, fmt.Errorf("failed to use Kind cluster %q: %w", kindCluster, err)
	}
	if err := os.Setenv("KUBECONFIG", cfg.KubeconfigFile()); err != nil {
		return ctx, err
	}
	clientset, err = kubernetes.NewForConfig(cfg.Client().RESTConfig())
	return ctx, err
}

// buildManagerImage builds the manager image from the working tree.
func buildManagerImage(ctx context.Context, _ *envconf.Config) (context.Context, error) {
	fmt.Println("building the manager(Operator) image")
	cmd := exec.Command("make", "docker-build", fmt.Sprintf("IMG=%s", projectImage))
	if _, err := utils.Run(cmd); err != nil {
		return ctx, fmt.Errorf("failed to build the manager(Operator) image: %w", err)
	}
	return ctx, nil
}
