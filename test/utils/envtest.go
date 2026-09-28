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

package utils

import (
	"os"
	"path/filepath"
)

// FirstEnvTestBinaryDir returns the first directory under basePath, or "" if there is none.
// ENVTEST-based tests depend on binaries that the Makefile targets locate through
// KUBEBUILDER_ASSETS. When tests run directly (e.g., from an IDE), envtest needs the
// directory explicitly; run 'make setup-envtest' first to install the binaries under bin/k8s.
func FirstEnvTestBinaryDir(basePath string) string {
	entries, err := os.ReadDir(basePath)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return filepath.Join(basePath, entry.Name())
		}
	}
	return ""
}
