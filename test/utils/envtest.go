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
	"runtime"
	"strings"

	"k8s.io/apimachinery/pkg/util/version"
)

// LatestEnvTestBinaryDir returns the directory under basePath with the newest envtest binaries
// for this platform, or "" if there is none. Tests that run without make, for example from an
// IDE, use it to find the binaries that 'make setup-envtest' installs under bin/k8s. Older
// versions stay there after a Kubernetes upgrade, so the newest one is the current version.
func LatestEnvTestBinaryDir(basePath string) string {
	entries, err := os.ReadDir(basePath)
	if err != nil {
		return ""
	}
	platform := "-" + runtime.GOOS + "-" + runtime.GOARCH
	var latest *version.Version
	var latestDir string
	for _, entry := range entries {
		name, ok := strings.CutSuffix(entry.Name(), platform)
		if !entry.IsDir() || !ok {
			continue
		}
		v, err := version.ParseSemantic(name)
		if err != nil {
			continue
		}
		if latest == nil || v.GreaterThan(latest) {
			latest, latestDir = v, filepath.Join(basePath, entry.Name())
		}
	}
	return latestDir
}
