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

package e2elibvirt

import (
	"context"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	capie2e "sigs.k8s.io/cluster-api/test/e2e"
	"sigs.k8s.io/cluster-api/test/framework"
	"sigs.k8s.io/cluster-api/test/framework/clusterctl"
)

// The quick start creates a cluster from the shipped "topology" flavor (the
// libvirt-kubeadm ClusterClass): one control plane VM and two worker VMs.
// The machines claim the LibvirtHosts in data/hosts.yaml by label and take
// their addresses from them, so the two workers come from one
// MachineDeployment. It then checks the Cluster and Machines are
// Available/Ready, and deletes the cluster, which removes the VMs.
var _ = Describe("When following the Cluster API quick-start", func() {
	capie2e.QuickStartSpec(ctx, func() capie2e.QuickStartSpecInput {
		return capie2e.QuickStartSpecInput{
			E2EConfig:             e2eConfig,
			ClusterctlConfigPath:  clusterctlConfigPath,
			BootstrapClusterProxy: bootstrapClusterProxy,
			ArtifactFolder:        artifactFolder,
			SkipCleanup:           skipCleanup,
			Flavor:                ptr.To("topology"),
			WorkerMachineCount:    ptr.To[int64](2),
			PostNamespaceCreated:  createHosts,
			ControlPlaneWaiters: clusterctl.ControlPlaneWaiters{
				WaitForControlPlaneInitialized: waitForControlPlaneAndInstallCNI,
			},
		}
	})
})

// createHosts registers the LibvirtHosts in data/hosts.yaml, and the Secret
// with their SSH key, in the spec's namespace. Hosts are inventory shared by
// clusters, so cluster templates do not create them.
func createHosts(proxy framework.ClusterProxy, namespace string) {
	keyPath := e2eConfig.MustGetVariable("LIBVIRT_HOST_SSH_PRIVATE_KEY_PATH")
	key, err := os.ReadFile(keyPath) //nolint:gosec // path comes from the e2e config
	Expect(err).ToNot(HaveOccurred(), "Failed to read the libvirt host SSH key %s", keyPath)

	Expect(proxy.GetClient().Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      e2eConfig.MustGetVariable("LIBVIRT_HOST_SSH_SECRET"),
		},
		Data: map[string][]byte{"ssh-privatekey": key},
	})).To(Succeed())

	hosts, err := os.ReadFile("data/hosts.yaml")
	Expect(err).ToNot(HaveOccurred())
	rendered := os.Expand(string(hosts), func(name string) string {
		if name == "NAMESPACE" {
			return namespace
		}
		return e2eConfig.GetVariableOrEmpty(name)
	})
	// CAPLV's deployment reports available a moment before its webhook
	// accepts connections; retry as the framework does for its own creates.
	Eventually(func() error {
		return proxy.CreateOrUpdate(ctx, []byte(rendered))
	}, "2m", "5s").Should(Succeed(), "Failed to create the LibvirtHosts")
}

// waitForControlPlaneAndInstallCNI waits for the first control plane
// machine as the default waiter does, then installs the CNI so the nodes can
// become Ready. CAPI's docker provider uses a ClusterResourceSet in its own
// test templates instead; this suite installs it directly so that it can
// use the shipped templates unmodified.
func waitForControlPlaneAndInstallCNI(
	ctx context.Context,
	input clusterctl.ApplyCustomClusterTemplateAndWaitInput,
	result *clusterctl.ApplyCustomClusterTemplateAndWaitResult,
) {
	result.ControlPlane = framework.DiscoveryAndWaitForControlPlaneInitialized(ctx,
		framework.DiscoveryAndWaitForControlPlaneInitializedInput{
			Lister:  input.ClusterProxy.GetClient(),
			Cluster: result.Cluster,
		}, input.WaitForControlPlaneIntervals...)

	cniPath := e2eConfig.MustGetVariable("CNI")
	cni, err := os.ReadFile(cniPath) //nolint:gosec // path comes from the e2e config
	Expect(err).ToNot(HaveOccurred(), "Failed to read the CNI manifest %s", cniPath)

	By("Installing the CNI in the workload cluster")
	workload := input.ClusterProxy.GetWorkloadCluster(ctx, result.Cluster.Namespace, result.Cluster.Name)
	Expect(workload.CreateOrUpdate(ctx, cni)).To(Succeed())
}
