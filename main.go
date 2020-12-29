/*
Copyright 2020 Red Hat Community of Practice.

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
	"flag"
	"os"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	ocpconfigv1 "github.com/openshift/api/config/v1"
	ocpnetv1 "github.com/openshift/api/network/v1"
	cloudcredentialv1 "github.com/openshift/cloud-credential-operator/pkg/apis/cloudcredential/v1"
	machinev1beta1 "github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	redhatcopv1alpha1 "github.com/redhat-cop/egressip-ipam-operator/api/v1alpha1"
	"github.com/redhat-cop/egressip-ipam-operator/controllers/egressipam"
	"github.com/redhat-cop/egressip-ipam-operator/controllers/namespace"
	"github.com/redhat-cop/operator-utils/pkg/util"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(redhatcopv1alpha1.AddToScheme(scheme))

	utilruntime.Must(ocpnetv1.AddToScheme(scheme))
	utilruntime.Must(ocpconfigv1.AddToScheme(scheme))
	utilruntime.Must(cloudcredentialv1.AddToScheme(scheme))
	utilruntime.Must(machinev1beta1.AddToScheme(scheme))

	// +kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                     scheme,
		MetricsBindAddress:         metricsAddr,
		Port:                       9443,
		HealthProbeBindAddress:     probeAddr,
		LeaderElection:             enableLeaderElection,
		LeaderElectionID:           "9ae943bf.redhat.io",
		LeaderElectionResourceLock: "configmaps",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err = (&egressipam.EgressIPAMReconciler{
		ReconcilerBase: util.NewReconcilerBase(mgr, mgr.GetEventRecorderFor("EgressIPAM_controller")),
		Log:            ctrl.Log.WithName("controllers").WithName("EgressIPAM"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "EgressIPAM")
		os.Exit(1)
	}
	if err = (&namespace.NamespaceReconciler{
		ReconcilerBase: util.NewReconcilerBase(mgr, mgr.GetEventRecorderFor("Namespace_controller")),
		Log:            ctrl.Log.WithName("controllers").WithName("Namespace"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Namespace")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("health", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("check", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
