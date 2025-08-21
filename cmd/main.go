package main

import (
	"flag"
	"os"

	"github.com/tedens/acm-manager/controllers"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"fmt"
	"strings"

	"k8s.io/client-go/discovery"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
	version  = "0.1.0"
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(networkingv1.AddToScheme(scheme))
	flag.StringVar(&version, "version", version, "Version of the binary")
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. Enabling this will ensure there is only one active controller manager.")
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	// Kubernetes version check: require >= v1.32
	config := ctrl.GetConfigOrDie()
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		setupLog.Error(err, "unable to create discovery client")
		os.Exit(1)
	}

	serverVersion, err := discoveryClient.ServerVersion()
	if err != nil {
		setupLog.Error(err, "unable to fetch server version")
		os.Exit(1)
	}

	parsedVersion := strings.TrimPrefix(serverVersion.GitVersion, "v")
	majorMinor := strings.Split(parsedVersion, ".")
	if len(majorMinor) < 2 {
		setupLog.Error(fmt.Errorf("unexpected version format: %s", parsedVersion), "unable to parse server version")
		os.Exit(1)
	}

	major := majorMinor[0]
	minor := majorMinor[1]
	if major < "1" || minor < "32" {
		setupLog.Error(fmt.Errorf("unsupported Kubernetes version: %s", serverVersion.GitVersion), "requires Kubernetes >= v1.32")
		os.Exit(1)
	}

	mgr, err := ctrl.NewManager(config, ctrl.Options{
		Scheme: scheme,
		Metrics: server.Options{
			BindAddress: "0", // disables metrics temporarily
		},
		HealthProbeBindAddress: ":8080",
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "acm-ingress-controller.tedens.dev",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err = (&controllers.IngressReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Ingress")
		os.Exit(1)
	}

	setupLog.Info("adding health and readiness checks")
	mgr.AddHealthzCheck("healthz", healthz.Ping)
	mgr.AddReadyzCheck("readyz", healthz.Ping)

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
