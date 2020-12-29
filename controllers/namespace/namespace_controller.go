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

package namespace

import (
	"context"
	"reflect"

	"github.com/go-logr/logr"
	ocpnetv1 "github.com/openshift/api/network/v1"
	"github.com/redhat-cop/egressip-ipam-operator/controllers/egressipam"
	"github.com/redhat-cop/operator-utils/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// NamespaceReconciler reconciles a Namespace object
type NamespaceReconciler struct {
	util.ReconcilerBase
	Log logr.Logger
}

// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups="network.openshift.io",resources=netnamespaces,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups="",resources=events,verbs=get;list;watch;create;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Namespace object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.7.0/pkg/reconcile
func (r *NamespaceReconciler) Reconcile(context context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("namespace", req.NamespacedName)

	// your logic here

	// Fetch the Namespace instance
	instance := &corev1.Namespace{}
	err := r.GetClient().Get(context, req.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	err = r.cleanUpNamespaceAndNetNamespace(instance)
	if err != nil {
		log.Error(err, "unable to clean up", "netnamespace", instance.GetName())
		return r.ManageError(context, instance, err)
	}

	return r.ManageSuccess(context, instance)
}

func (r *NamespaceReconciler) cleanUpNamespaceAndNetNamespace(namespace *corev1.Namespace) error {
	netNamespace := &ocpnetv1.NetNamespace{}
	err := r.GetClient().Get(context.TODO(), types.NamespacedName{Name: namespace.GetName()}, netNamespace)
	if err != nil {
		r.Log.Error(err, "unable to retrieve", "netnamespace", namespace.GetName())
		return err
	}
	if !reflect.DeepEqual(netNamespace.EgressIPs, []string{}) {
		netNamespace.EgressIPs = []ocpnetv1.NetNamespaceEgressIP{}
		err := r.GetClient().Update(context.TODO(), netNamespace, &client.UpdateOptions{})
		if err != nil {
			r.Log.Error(err, "unable to update ", "netnamespace", netNamespace.GetName())
			return err
		}
	}
	if _, ok := namespace.GetAnnotations()[egressipam.NamespaceAssociationAnnotation]; ok {
		delete(namespace.Annotations, egressipam.NamespaceAssociationAnnotation)
		err := r.GetClient().Update(context.TODO(), namespace, &client.UpdateOptions{})
		if err != nil {
			r.Log.Error(err, "unable to update ", "namespace", namespace.GetName())
			return err
		}
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *NamespaceReconciler) SetupWithManager(mgr ctrl.Manager) error {

	IsAnnotated := predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			_, okold := e.ObjectOld.GetAnnotations()[egressipam.NamespaceAnnotation]
			_, oknew := e.ObjectNew.GetAnnotations()[egressipam.NamespaceAnnotation]
			return (okold && !oknew)
		},
		CreateFunc: func(e event.CreateEvent) bool {
			return false
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return false
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return false
		},
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Namespace{}, builder.WithPredicates(IsAnnotated)).
		Complete(r)
}
