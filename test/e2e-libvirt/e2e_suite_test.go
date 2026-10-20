//go:build e2e

/*
Copyright 2026 Anthony Green.

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

// Package e2elibvirt runs Cluster API's provider test specs against real
// libvirt hosts: a kind management cluster with CAPI, the kubeadm providers
// and CAPLV installed by clusterctl, building workload clusters out of KVM
// VMs. The suite setup follows sigs.k8s.io/cluster-api/test/e2e. See
// README.md for how to prepare a host and run it.
package e2elibvirt

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"

	"sigs.k8s.io/cluster-api/test/framework"
	"sigs.k8s.io/cluster-api/test/framework/bootstrap"
	"sigs.k8s.io/cluster-api/test/framework/clusterctl"
	"sigs.k8s.io/cluster-api/test/framework/ginkgoextensions"
)

var (
	// configPath is the path to the e2e config file.
	configPath string

	// useExistingCluster uses the current kubeconfig context as the
	// management cluster instead of creating a kind cluster.
	useExistingCluster bool

	// artifactFolder is where logs and resource dumps are written.
	artifactFolder string

	// skipCleanup keeps the management cluster and workload clusters for
	// debugging.
	skipCleanup bool
)

var (
	ctx = ctrl.SetupSignalHandler()

	// watchesCtx stops log streaming when the suite ends.
	watchesCtx, cancelWatches = context.WithCancel(ctx)

	e2eConfig                *clusterctl.E2EConfig
	clusterctlConfigPath     string
	bootstrapClusterProvider bootstrap.ClusterProvider
	bootstrapClusterProxy    framework.ClusterProxy
)

// hostVariables describe the libvirt host and have no default in the e2e
// config.
var hostVariables = []string{
	"LIBVIRT_HOST_URI",
	"LIBVIRT_HOST_SSH_FINGERPRINT",
	"LIBVIRT_HOST_RESERVED_MEMORY_MB",
	"LIBVIRT_HOST_SSH_PRIVATE_KEY_PATH",
}

func init() {
	flag.StringVar(&configPath, "e2e.config", "", "path to the e2e config file")
	flag.StringVar(&artifactFolder, "e2e.artifacts-folder", "", "folder where e2e test artifacts are stored")
	flag.BoolVar(&skipCleanup, "e2e.skip-resource-cleanup", false, "if true, test resources are not cleaned up")
	flag.BoolVar(&useExistingCluster, "e2e.use-existing-cluster", false,
		"if true, the current kubeconfig context is used as the management cluster")
}

func TestE2E(t *testing.T) {
	g := NewWithT(t)
	g.Expect(os.MkdirAll(artifactFolder, 0o755)).To(Succeed(), "Can't create e2e.artifacts-folder %q", artifactFolder)

	RegisterFailHandler(Fail)
	w, err := ginkgoextensions.EnableFileLogging(filepath.Join(artifactFolder, "ginkgo-log.txt"))
	g.Expect(err).ToNot(HaveOccurred())
	defer func() { _ = w.Close() }()

	RunSpecs(t, "caplv-e2e")
}

var _ = SynchronizedBeforeSuite(func() []byte {
	Expect(configPath).To(BeAnExistingFile(), "e2e.config should be an existing file")

	By("Loading the e2e test configuration from " + configPath)
	e2eConfig = loadE2EConfig(configPath)
	for _, name := range hostVariables {
		Expect(e2eConfig.GetVariableOrEmpty(name)).NotTo(BeEmpty(),
			"%s must be set; scripts/setup-host.sh exports it", name)
	}

	By("Creating a clusterctl local repository into " + artifactFolder)
	clusterctlConfigPath = clusterctl.CreateRepository(ctx, clusterctl.CreateRepositoryInput{
		E2EConfig:        e2eConfig,
		RepositoryFolder: filepath.Join(artifactFolder, "repository"),
	})

	By("Setting up the bootstrap cluster")
	kubeconfigPath := ""
	if !useExistingCluster {
		bootstrapClusterProvider = bootstrap.CreateKindBootstrapClusterAndLoadImages(ctx,
			bootstrap.CreateKindBootstrapClusterAndLoadImagesInput{
				Name:              e2eConfig.ManagementClusterName,
				KubernetesVersion: e2eConfig.MustGetVariable("KUBERNETES_VERSION_MANAGEMENT"),
				Images:            e2eConfig.Images,
				IPFamily:          "IPv4",
				LogFolder:         filepath.Join(artifactFolder, "kind"),
			})
		Expect(bootstrapClusterProvider).ToNot(BeNil(), "Failed to create a bootstrap cluster")
		kubeconfigPath = bootstrapClusterProvider.GetKubeconfigPath()
	}
	bootstrapClusterProxy = framework.NewClusterProxy("bootstrap", kubeconfigPath, initScheme())

	By("Initializing the bootstrap cluster")
	initInput := clusterctl.InitManagementClusterAndWatchControllerLogsInput{
		ClusterProxy:            bootstrapClusterProxy,
		ClusterctlConfigPath:    clusterctlConfigPath,
		InfrastructureProviders: e2eConfig.InfrastructureProviders(),
		LogFolder:               filepath.Join(artifactFolder, "clusters", bootstrapClusterProxy.GetName()),
	}
	clusterctl.InitManagementClusterAndWatchControllerLogs(watchesCtx, initInput,
		e2eConfig.GetIntervals(bootstrapClusterProxy.GetName(), "wait-controllers")...)

	return []byte(configPath + "\n" + clusterctlConfigPath + "\n" + bootstrapClusterProxy.GetKubeconfigPath())
}, func(data []byte) {
	parts := splitLines(string(data))
	Expect(parts).To(HaveLen(3))
	configPath, clusterctlConfigPath = parts[0], parts[1]
	e2eConfig = loadE2EConfig(configPath)
	bootstrapClusterProxy = framework.NewClusterProxy("bootstrap", parts[2], initScheme())
})

var _ = SynchronizedAfterSuite(func() {}, func() {
	cancelWatches()
	if skipCleanup {
		return
	}
	By("Tearing down the management cluster")
	if bootstrapClusterProxy != nil {
		bootstrapClusterProxy.Dispose(ctx)
	}
	if bootstrapClusterProvider != nil {
		bootstrapClusterProvider.Dispose(ctx)
	}
})

func initScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	framework.TryAddDefaultSchemes(scheme)
	return scheme
}

func loadE2EConfig(path string) *clusterctl.E2EConfig {
	config := clusterctl.LoadE2EConfig(ctx, clusterctl.LoadE2EConfigInput{ConfigPath: path})
	Expect(config).ToNot(BeNil(), "Failed to load E2E config from %s", path)
	return config
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := range len(s) {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}
