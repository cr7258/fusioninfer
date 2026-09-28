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
	// - CERT_MANAGER_INSTALL_SKIP=true: Skips CertManager installation during test setup.
	// These variables are useful if CertManager is already installed, avoiding
	// re-installation and conflicts.
	skipCertManagerInstall = os.Getenv("CERT_MANAGER_INSTALL_SKIP") == "true"
	// - E2E_ARTIFACTS_DIR=<dir>: Saves the controller-manager logs and manifest, the
	//   Kubernetes events, and the curl-metrics pod logs to <dir> before the teardown.
	artifactsDir = os.Getenv("E2E_ARTIFACTS_DIR")
	// - KIND_CLUSTER=<name> and KIND=<path>: The Kind cluster to run on, which is created
	//   if it does not exist, and the kind binary. make test-e2e sets both.
	kindCluster = cmp.Or(os.Getenv("KIND_CLUSTER"), "fusioninfer-test-e2e")
	kindBinary  = os.Getenv("KIND")

	// installedCertManager is set before the suite installs CertManager, so that the
	// suite also removes a partial installation.
	installedCertManager = false

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
// The default setup requires Kind, builds/loads the Manager Docker image locally, and installs
// CertManager.
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
		setUpCertManager,
	)
	testenv.Finish(tearDownCertManager)

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

// setUpCertManager installs CertManager unless it is skipped or already installed. The e2e
// tests are intended to run on a temporary cluster that is created and destroyed for testing;
// checking for CertManager first prevents errors on clusters where it is already installed.
func setUpCertManager(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
	if skipCertManagerInstall {
		return ctx, nil
	}
	fmt.Println("checking if cert manager is installed already")
	installed, err := isCertManagerInstalled(ctx, cfg)
	if err != nil {
		return ctx, fmt.Errorf("failed to check for CertManager: %w", err)
	}
	if installed {
		fmt.Println("WARNING: CertManager is already installed. Skipping installation...")
		return ctx, nil
	}
	fmt.Println("Installing CertManager...")
	installedCertManager = true
	if err := installCertManager(ctx, cfg); err != nil {
		return ctx, fmt.Errorf("failed to install CertManager: %w", err)
	}
	return ctx, nil
}

// tearDownCertManager uninstalls CertManager if the suite installed it.
func tearDownCertManager(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
	if !installedCertManager {
		return ctx, nil
	}
	fmt.Println("Uninstalling CertManager...")
	if err := uninstallCertManager(ctx, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to uninstall CertManager: %v\n", err)
	}
	return ctx, nil
}
