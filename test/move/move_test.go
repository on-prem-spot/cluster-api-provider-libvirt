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

// Package move checks that clusterctl move carries this provider's
// objects between management clusters. It runs clusterctl's own move code
// between two envtest API servers whose CRDs come from the kustomize build,
// so the move labels set there are what is tested.
package move

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	clusterctlv1 "sigs.k8s.io/cluster-api/cmd/clusterctl/api/v1alpha3"
	clusterctlclient "sigs.k8s.io/cluster-api/cmd/clusterctl/client"
	"sigs.k8s.io/cluster-api/cmd/clusterctl/client/cluster"
	"sigs.k8s.io/cluster-api/cmd/clusterctl/client/config"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	infrav1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
)

const (
	providerNamespace = "caplv-system"
	hostNamespace     = "team-a"
	moveLabel         = "clusterctl.cluster.x-k8s.io/move"
)

func TestClusterctlMove(t *testing.T) {
	ctx := context.Background()
	crds := providerCRDs(t)
	capiCRDs := capiCRDDir(t)
	// Keep clusterctl away from the user's own configuration. This also
	// hides the Go module cache, so it comes after the lookups above.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	scheme := k8sruntime.NewScheme()
	for _, add := range []func(*k8sruntime.Scheme) error{clientgoscheme.AddToScheme, infrav1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	src, srcKubeconfig := startManagementCluster(ctx, t, crds, capiCRDs, scheme)
	dst, dstKubeconfig := startManagementCluster(ctx, t, crds, capiCRDs, scheme)

	// Hosts, each with a different kind of credential.
	id := &infrav1.LibvirtClusterIdentity{
		ObjectMeta: metav1.ObjectMeta{Name: "shared"},
		Spec: infrav1.LibvirtClusterIdentitySpec{
			SecretName:        "shared-key",
			AllowedNamespaces: &infrav1.AllowedNamespaces{},
		},
	}
	mustCreate(ctx, t, src, id)
	identitySecret := sshSecret(providerNamespace, "shared-key", nil)
	// The controller sets this reference when it first resolves the
	// identity; see ensureIdentityOwnsSecret.
	identitySecret.OwnerReferences = []metav1.OwnerReference{{
		APIVersion: infrav1.GroupVersion.String(),
		Kind:       "LibvirtClusterIdentity",
		Name:       id.Name,
		UID:        id.UID,
	}}
	mustCreate(ctx, t, src, identitySecret)
	mustCreate(ctx, t, src, sshSecret(hostNamespace, "labelled-key", map[string]string{moveLabel: ""}))
	mustCreate(ctx, t, src, sshSecret(hostNamespace, "unlabelled-key", nil))
	mustCreate(ctx, t, src, host("by-secret", &infrav1.SecretReference{Name: "labelled-key"}, nil))
	mustCreate(ctx, t, src, host("by-identity", nil, &infrav1.LibvirtClusterIdentityReference{Name: id.Name}))

	c, err := clusterctlclient.New(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Move(ctx, clusterctlclient.MoveOptions{
		FromKubeconfig: clusterctlclient.Kubeconfig{Path: srcKubeconfig},
		ToKubeconfig:   clusterctlclient.Kubeconfig{Path: dstKubeconfig},
		Namespace:      hostNamespace,
	}); err != nil {
		t.Fatalf("clusterctl move: %v", err)
	}

	// Namespaced objects move: created on the target, removed from the source.
	for _, obj := range []client.Object{
		&infrav1.LibvirtHost{ObjectMeta: metav1.ObjectMeta{Namespace: hostNamespace, Name: "by-secret"}},
		&infrav1.LibvirtHost{ObjectMeta: metav1.ObjectMeta{Namespace: hostNamespace, Name: "by-identity"}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: hostNamespace, Name: "labelled-key"}},
	} {
		expectExists(ctx, t, dst, obj, true, "target")
		expectExists(ctx, t, src, obj, false, "source")
	}
	// A Secret nothing links to the move stays behind.
	unlabelled := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: hostNamespace, Name: "unlabelled-key"}}
	expectExists(ctx, t, dst, unlabelled, false, "target")
	expectExists(ctx, t, src, unlabelled, true, "source")

	// The identity and its Secret are global: copied, and kept on the source
	// for any clusters still managed there.
	movedID := &infrav1.LibvirtClusterIdentity{ObjectMeta: metav1.ObjectMeta{Name: id.Name}}
	movedSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: providerNamespace, Name: "shared-key"}}
	for _, obj := range []client.Object{movedID, movedSecret} {
		expectExists(ctx, t, src, obj, true, "source")
		expectExists(ctx, t, dst, obj, true, "target")
	}
	if refs := movedSecret.OwnerReferences; len(refs) != 1 || refs[0].UID != movedID.UID {
		t.Errorf("target identity Secret has owner references %+v, want one to the target identity (UID %s)",
			refs, movedID.UID)
	}
}

// providerCRDs returns the CRDs from the kustomize build, labelled as
// clusterctl init labels the components it installs.
func providerCRDs(t *testing.T) []*apiextensionsv1.CustomResourceDefinition {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	kustomize := os.Getenv("KUSTOMIZE")
	if kustomize == "" {
		kustomize = filepath.Join(root, "bin", "kustomize")
	}
	if _, err := os.Stat(kustomize); err != nil {
		t.Skipf("kustomize not found at %s (set KUSTOMIZE or run make kustomize): %v", kustomize, err)
	}
	out, err := exec.Command(kustomize, "build", filepath.Join(root, "config", "crd")).Output()
	if err != nil {
		t.Fatalf("kustomize build config/crd: %v", err)
	}

	var crds []*apiextensionsv1.CustomResourceDefinition
	decoder := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(out), 4096)
	for {
		crd := &apiextensionsv1.CustomResourceDefinition{}
		if err := decoder.Decode(crd); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatalf("decode CRD: %v", err)
		}
		if crd.Name == "" {
			continue
		}
		if crd.Labels == nil {
			crd.Labels = map[string]string{}
		}
		crd.Labels[clusterctlv1.ClusterctlLabel] = ""
		crds = append(crds, crd)
	}
	if len(crds) == 0 {
		t.Fatal("kustomize build config/crd produced no CRDs")
	}
	return crds
}

// capiCRDDir returns the directory holding the core Cluster API CRDs of the
// version in go.mod; clusterctl move checks the Cluster CRD's version.
func capiCRDDir(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "sigs.k8s.io/cluster-api").Output()
	if err != nil {
		t.Fatalf("locate the sigs.k8s.io/cluster-api module: %v", err)
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		t.Fatal("the sigs.k8s.io/cluster-api module is not in the module cache; run go mod download")
	}
	return filepath.Join(dir, "core", "config", "crd", "bases")
}

// startManagementCluster starts an API server with the Cluster API and
// provider CRDs and a clusterctl inventory recording the provider in
// providerNamespace, and returns a client and a kubeconfig path for it.
func startManagementCluster(
	ctx context.Context,
	t *testing.T,
	crds []*apiextensionsv1.CustomResourceDefinition,
	capiCRDDir string,
	scheme *k8sruntime.Scheme,
) (client.Client, string) {
	t.Helper()
	env := &envtest.Environment{
		CRDInstallOptions: envtest.CRDInstallOptions{
			CRDs:               crds,
			Paths:              []string{capiCRDDir},
			ErrorIfPathMissing: true,
		},
		// make test starts several API servers at once alongside the other
		// envtest suites; the 20s default is tight under that load.
		ControlPlaneStartTimeout: time.Minute,
	}
	if dir := envtestBinaryDir(); dir != "" {
		env.BinaryAssetsDirectory = dir
	}
	cfg, err := env.Start()
	if err != nil {
		t.Fatalf("start envtest: %v", err)
	}
	t.Cleanup(func() { _ = env.Stop() })

	user, err := env.AddUser(envtest.User{Name: "admin", Groups: []string{"system:masters"}}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	kubeconfig, err := user.KubeConfig()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(path, kubeconfig, 0o600); err != nil {
		t.Fatal(err)
	}

	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	for _, ns := range []string{providerNamespace, hostNamespace} {
		mustCreate(ctx, t, c, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
	}

	configClient, err := config.New(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	inventory := cluster.New(cluster.Kubeconfig{Path: path}, configClient).ProviderInventory()
	if err := inventory.EnsureCustomResourceDefinitions(ctx); err != nil {
		t.Fatalf("install clusterctl inventory CRD: %v", err)
	}
	if err := inventory.Create(ctx, clusterctlv1.Provider{
		ObjectMeta:   metav1.ObjectMeta{Namespace: providerNamespace, Name: "infrastructure-libvirt"},
		ProviderName: "libvirt",
		Type:         string(clusterctlv1.InfrastructureProviderType),
		Version:      "v0.1.0",
	}); err != nil {
		t.Fatalf("create clusterctl inventory: %v", err)
	}
	return c, path
}

// envtestBinaryDir finds the envtest binaries make setup-envtest installs,
// so the test also runs outside make test.
func envtestBinaryDir() string {
	if os.Getenv("KUBEBUILDER_ASSETS") != "" {
		return ""
	}
	_, thisFile, _, _ := runtime.Caller(0)
	entries, err := os.ReadDir(filepath.Join(filepath.Dir(thisFile), "..", "..", "bin", "k8s"))
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return filepath.Join(filepath.Dir(thisFile), "..", "..", "bin", "k8s", entry.Name())
		}
	}
	return ""
}

func sshSecret(namespace, name string, labels map[string]string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name, Labels: labels},
		Data:       map[string][]byte{"ssh-privatekey": []byte("key")},
	}
}

func host(
	name string,
	secretRef *infrav1.SecretReference,
	identityRef *infrav1.LibvirtClusterIdentityReference,
) *infrav1.LibvirtHost {
	return &infrav1.LibvirtHost{
		ObjectMeta: metav1.ObjectMeta{Namespace: hostNamespace, Name: name},
		Spec: infrav1.LibvirtHostSpec{
			URI:         "qemu+ssh://caplv@hv.example.com/system",
			SecretRef:   secretRef,
			IdentityRef: identityRef,
		},
	}
}

func mustCreate(ctx context.Context, t *testing.T, c client.Client, obj client.Object) {
	t.Helper()
	if err := c.Create(ctx, obj); err != nil {
		t.Fatalf("create %T %s: %v", obj, client.ObjectKeyFromObject(obj), err)
	}
}

// expectExists checks whether obj exists, reading it into obj when it does.
func expectExists(ctx context.Context, t *testing.T, c client.Client, obj client.Object, want bool, where string) {
	t.Helper()
	err := c.Get(ctx, client.ObjectKeyFromObject(obj), obj)
	switch {
	case err == nil && !want:
		t.Errorf("%T %s still exists on the %s cluster", obj, client.ObjectKeyFromObject(obj), where)
	case apierrors.IsNotFound(err) && want:
		t.Errorf("%T %s is missing from the %s cluster", obj, client.ObjectKeyFromObject(obj), where)
	case err != nil && !apierrors.IsNotFound(err):
		t.Errorf("get %T %s from the %s cluster: %v", obj, client.ObjectKeyFromObject(obj), where, err)
	}
}
