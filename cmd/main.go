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

package main

import (
	"crypto/tls"
	"flag"
	"fmt"

	"os"

	"github.com/spf13/pflag"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	gossh "golang.org/x/crypto/ssh"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/discovery"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	infrastructurev1alpha1 "github.com/on-prem-spot/cluster-api-provider-libvirt/api/v1alpha1"
	"github.com/on-prem-spot/cluster-api-provider-libvirt/feature"
	"github.com/on-prem-spot/cluster-api-provider-libvirt/internal/bootartifacts"
	"github.com/on-prem-spot/cluster-api-provider-libvirt/internal/controller"
	"github.com/on-prem-spot/cluster-api-provider-libvirt/internal/imagecache"
	"github.com/on-prem-spot/cluster-api-provider-libvirt/internal/iso"
	"github.com/on-prem-spot/cluster-api-provider-libvirt/internal/libvirt"
	libvirtssh "github.com/on-prem-spot/cluster-api-provider-libvirt/internal/ssh"
	webhookv1alpha1 "github.com/on-prem-spot/cluster-api-provider-libvirt/internal/webhook/v1alpha1"
	"github.com/on-prem-spot/cluster-api-provider-libvirt/version"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(clusterv1.AddToScheme(scheme))
	utilruntime.Must(infrastructurev1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

// nolint:gocyclo
func main() {
	var metricsAddr string
	var metricsCertPath, metricsCertName, metricsCertKey string
	var webhookCertPath, webhookCertName, webhookCertKey string
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var maxConcurrentReconciles int
	var baseImageCacheDir string
	var bootArtifactsCacheDir string
	var allowInsecureHostKeys bool
	var allowInsecureImageTransport bool
	var watchFilterValue string
	var identityNamespace string
	var tlsOpts []func(*tls.Config)
	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.StringVar(&webhookCertPath, "webhook-cert-path", "", "The directory that contains the webhook certificate.")
	flag.StringVar(&webhookCertName, "webhook-cert-name", "tls.crt", "The name of the webhook certificate file.")
	flag.StringVar(&webhookCertKey, "webhook-cert-key", "tls.key", "The name of the webhook key file.")
	flag.StringVar(&metricsCertPath, "metrics-cert-path", "",
		"The directory that contains the metrics server certificate.")
	flag.StringVar(&metricsCertName, "metrics-cert-name", "tls.crt", "The name of the metrics server certificate file.")
	flag.StringVar(&metricsCertKey, "metrics-cert-key", "tls.key", "The name of the metrics server key file.")
	flag.IntVar(&maxConcurrentReconciles, "max-concurrent-reconciles", 50,
		"Maximum number of LibvirtMachine reconciles running in parallel. "+
			"Each targets a different host over its own SSH connection.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	flag.StringVar(&baseImageCacheDir, "base-image-cache-dir", "/var/cache/caplv/baseimages",
		"Directory inside the controller pod where base qcow2 images are staged "+
			"after download. Should be backed by an emptyDir (or a PVC if survival "+
			"across pod restarts is desired). Content-addressed by sha256.")
	flag.StringVar(&bootArtifactsCacheDir, "boot-artifacts-cache-dir", "/var/cache/caplv/bootartifacts",
		"Directory inside the controller pod where LibvirtCluster.spec.bootArtifacts kernels "+
			"and initramfs images are staged after download, before they are copied to hosts. "+
			"Should be backed by an emptyDir. Content-addressed by sha256.")
	flag.BoolVar(&allowInsecureHostKeys, "allow-insecure-host-keys", false,
		"Connect to a LibvirtHost that has no spec.hostKeyFingerprint, accepting any SSH host key. "+
			"Off by default: an unverified SSH channel exposes the worker ignition config, which "+
			"carries the cluster's kubelet bootstrap credentials, to whoever answers on that address.")
	flag.BoolVar(&allowInsecureImageTransport, "allow-insecure-image-transport", false,
		"Permit LibvirtCluster.spec.baseImage sources that disable transport authentication "+
			"(plain http:// URLs, oci.plainHTTP, s3.insecure, or any insecureSkipTLSVerify). "+
			"Off by default: those bytes become the root disk of every worker VM.")
	flag.StringVar(&watchFilterValue, "watch-filter", "",
		fmt.Sprintf("Label value that the controller watches to reconcile cluster-api objects. "+
			"Label key is always %s. If unspecified, the controller watches for all cluster-api objects.",
			clusterv1.WatchLabel))
	flag.StringVar(&identityNamespace, "identity-namespace", os.Getenv("POD_NAMESPACE"),
		"Namespace holding the Secrets of LibvirtClusterIdentities. Defaults to $POD_NAMESPACE, "+
			"which the shipped manager Deployment sets to the controller's own namespace.")
	opts := zap.Options{
		Development: false,
	}
	opts.BindFlags(flag.CommandLine)
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	feature.MutableGates.AddFlag(pflag.CommandLine)
	pflag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	setupLog.Info("Starting CAPLV",
		"version", version.Version,
		"gitCommit", version.GitCommit,
		"buildDate", version.BuildDate)

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("Disabling HTTP/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	// Initial webhook TLS options
	webhookTLSOpts := tlsOpts
	webhookServerOptions := webhook.Options{
		TLSOpts: webhookTLSOpts,
	}

	if len(webhookCertPath) > 0 {
		setupLog.Info("Initializing webhook certificate watcher using provided certificates",
			"webhook-cert-path", webhookCertPath, "webhook-cert-name", webhookCertName, "webhook-cert-key", webhookCertKey)

		webhookServerOptions.CertDir = webhookCertPath
		webhookServerOptions.CertName = webhookCertName
		webhookServerOptions.KeyName = webhookCertKey
	}

	webhookServer := webhook.NewServer(webhookServerOptions)

	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsOpts,
	}

	if secureMetrics {
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	if len(metricsCertPath) > 0 {
		setupLog.Info("Initializing metrics certificate watcher using provided certificates",
			"metrics-cert-path", metricsCertPath, "metrics-cert-name", metricsCertName, "metrics-cert-key", metricsCertKey)

		metricsServerOptions.CertDir = metricsCertPath
		metricsServerOptions.CertName = metricsCertName
		metricsServerOptions.KeyName = metricsCertKey
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "b252ec3e.cluster.x-k8s.io",
	})
	if err != nil {
		setupLog.Error(err, "Failed to start manager")
		os.Exit(1)
	}

	// Wire SSH client factory. Host-key verification is mandatory unless the
	// operator explicitly opted out.
	if allowInsecureHostKeys {
		setupLog.Info("WARNING: --allow-insecure-host-keys is set; LibvirtHosts without a " +
			"hostKeyFingerprint will be connected to without SSH host key verification")
	}
	sshClientFactory := controller.SSHClientFactory(libvirtssh.NewSSHClientFactory(allowInsecureHostKeys))

	// Wire libvirt client factory.
	libvirtClientFactory := func(sshClient *gossh.Client, connURI string) libvirt.Client {
		return libvirt.NewVirshClient(sshClient, connURI)
	}

	if err := (&controller.LibvirtHostReconciler{
		Client:               mgr.GetClient(),
		Scheme:               mgr.GetScheme(),
		SSHClientFactory:     sshClientFactory,
		LibvirtClientFactory: libvirtClientFactory,
		Recorder:             mgr.GetEventRecorder("libvirthost-controller"),
		APIReader:            mgr.GetAPIReader(),
		WatchFilterValue:     watchFilterValue,
		IdentityNamespace:    identityNamespace,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "LibvirtHost")
		os.Exit(1)
	}
	if err := (&controller.LibvirtClusterReconciler{
		Client:           mgr.GetClient(),
		Scheme:           mgr.GetScheme(),
		Recorder:         mgr.GetEventRecorder("libvirtcluster-controller"),
		WatchFilterValue: watchFilterValue,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "LibvirtCluster")
		os.Exit(1)
	}
	if allowInsecureImageTransport {
		setupLog.Info("WARNING: --allow-insecure-image-transport is set; base images may be " +
			"fetched over unauthenticated transports")
	}
	bootArtifactsResolver := bootartifacts.NewMultiResolver(bootArtifactsCacheDir)
	bootArtifactsResolver.AllowInsecureTransport = allowInsecureImageTransport
	baseImageResolver := imagecache.NewMultiResolver()
	baseImageResolver.AllowInsecureTransport = allowInsecureImageTransport
	baseImageCache, err := imagecache.New(baseImageCacheDir, baseImageResolver)
	if err != nil {
		setupLog.Error(err, "Failed to initialise base image cache",
			"dir", baseImageCacheDir)
		os.Exit(1)
	}
	if err := (&controller.LibvirtMachineReconciler{
		Client:                  mgr.GetClient(),
		APIReader:               mgr.GetAPIReader(),
		Scheme:                  mgr.GetScheme(),
		SSHClientFactory:        sshClientFactory,
		LibvirtClientFactory:    libvirtClientFactory,
		ISOBuilder:              iso.NewDiskfsBuilder(),
		BootArtifactsResolver:   bootArtifactsResolver,
		BaseImageCache:          baseImageCache,
		MaxConcurrentReconciles: maxConcurrentReconciles,
		Recorder:                mgr.GetEventRecorder("libvirtmachine-controller"),
		WatchFilterValue:        watchFilterValue,
		IdentityNamespace:       identityNamespace,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "LibvirtMachine")
		os.Exit(1)
	}
	if err := (&controller.CSRApproverReconciler{
		Client:           mgr.GetClient(),
		Scheme:           mgr.GetScheme(),
		WatchFilterValue: watchFilterValue,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "CSRApprover")
		os.Exit(1)
	}
	// nolint:goconst
	if os.Getenv("ENABLE_WEBHOOKS") != "false" {
		if err := webhookv1alpha1.SetupLibvirtMachineWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "Failed to create webhook", "webhook", "LibvirtMachine")
			os.Exit(1)
		}
		if err := webhookv1alpha1.SetupLibvirtMachineTemplateWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "Failed to create webhook", "webhook", "LibvirtMachineTemplate")
			os.Exit(1)
		}
		if err := webhookv1alpha1.SetupLibvirtClusterTemplateWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "Failed to create webhook", "webhook", "LibvirtClusterTemplate")
			os.Exit(1)
		}
		if err := webhookv1alpha1.SetupLibvirtClusterWebhookWithManager(mgr, allowInsecureImageTransport); err != nil {
			setupLog.Error(err, "Failed to create webhook", "webhook", "LibvirtCluster")
			os.Exit(1)
		}
		if err := webhookv1alpha1.SetupLibvirtHostWebhookWithManager(mgr, allowInsecureHostKeys); err != nil {
			setupLog.Error(err, "Failed to create webhook", "webhook", "LibvirtHost")
			os.Exit(1)
		}
		if err := webhookv1alpha1.SetupLibvirtClusterIdentityWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "Failed to create webhook", "webhook", "LibvirtClusterIdentity")
			os.Exit(1)
		}
	}
	// The external control plane writes the API server the manager reaches,
	// and its CA, into <cluster>-kubeconfig: Cluster API runs in the same
	// cluster and reaches it the same way.
	restConfig := mgr.GetConfig()
	caData := restConfig.CAData
	if len(caData) == 0 && restConfig.CAFile != "" {
		if caData, err = os.ReadFile(restConfig.CAFile); err != nil {
			setupLog.Error(err, "Failed to read the API server CA", "file", restConfig.CAFile)
			os.Exit(1)
		}
	}
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(restConfig)
	if err != nil {
		setupLog.Error(err, "Failed to create discovery client")
		os.Exit(1)
	}
	if err := (&controller.LibvirtExternalControlPlaneReconciler{
		Client:           mgr.GetClient(),
		Scheme:           mgr.GetScheme(),
		Recorder:         mgr.GetEventRecorder("libvirtexternalcontrolplane-controller"),
		WatchFilterValue: watchFilterValue,
		APIServer:        restConfig.Host,
		CAData:           caData,
		ServerVersion: func() (string, error) {
			info, err := discoveryClient.ServerVersion()
			if err != nil {
				return "", err
			}
			return info.GitVersion, nil
		},
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "LibvirtExternalControlPlane")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("Starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "Failed to run manager")
		os.Exit(1)
	}
}
