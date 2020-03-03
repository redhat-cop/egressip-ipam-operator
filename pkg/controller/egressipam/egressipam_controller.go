package egressipam

import (
	"context"
	"reflect"

	ocpnetv1 "github.com/openshift/api/network/v1"
	redhatcopv1alpha1 "github.com/redhat-cop/egressip-ipam-operator/pkg/apis/redhatcop/v1alpha1"
	"github.com/redhat-cop/operator-utils/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const controllerName = "egressipam-controller"
const namespaceAnnotation = "egressip-ipam-operator.redhat-cop.io/egressipam"

var log = logf.Log.WithName(controllerName)

/**
* USER ACTION REQUIRED: This is a scaffold file intended for the user to modify with their own Controller
* business logic.  Delete these comments after modifying this file.*
 */

// Add creates a new EgressIPAM Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileEgressIPAM{
		ReconcilerBase: util.NewReconcilerBase(mgr.GetClient(), mgr.GetScheme(), mgr.GetConfig(), mgr.GetEventRecorderFor(controllerName)),
	}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	reconcileEgressIPAM, ok := r.(*ReconcileEgressIPAM)
	if !ok {
		log.Info("unable to convert to ReconcileEgressIPAM from ", "reconciler", r)
	}
	// Create a new controller
	c, err := controller.New(controllerName, mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource EgressIPAM
	err = c.Watch(&source.Kind{Type: &redhatcopv1alpha1.EgressIPAM{
		TypeMeta: metav1.TypeMeta{
			Kind: "EgressIPAM",
		},
	}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	IsCreatedOrIsAnnotationChanged := predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			return reflect.DeepEqual(e.MetaOld.GetAnnotations(), e.MetaNew.GetAnnotations())
		},
		CreateFunc: func(e event.CreateEvent) bool {
			return true
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return false
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return false
		},
	}

	// Watch for changes on nodes
	err = c.Watch(&source.Kind{Type: &corev1.Node{
		TypeMeta: metav1.TypeMeta{
			Kind: "Node",
		},
	}}, &enqueForSelectingEgressIPAMNode{
		r: reconcileEgressIPAM,
	}, &IsCreatedOrIsAnnotationChanged)
	if err != nil {
		return err
	}

	IsCreatedOrIsEgressCIDRsChanged := predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldHostSubnet, ok := e.ObjectOld.(*ocpnetv1.HostSubnet)
			if !ok {
				log.Info("unable to convert event object to hostsubnet,", "event", e)
				return false
			}
			newHostSubnet, ok := e.ObjectNew.(*ocpnetv1.HostSubnet)
			if !ok {
				log.Info("unable to convert event object to hostsubnet,", "event", e)
				return false
			}
			return reflect.DeepEqual(oldHostSubnet.EgressCIDRs, newHostSubnet.EgressCIDRs)
		},
		CreateFunc: func(e event.CreateEvent) bool {
			return true
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return false
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return false
		},
	}

	//Watch for changes on hostsubnet
	err = c.Watch(&source.Kind{Type: &ocpnetv1.HostSubnet{
		TypeMeta: metav1.TypeMeta{
			Kind: "HostSubnet",
		},
	}}, &enqueForSelectingEgressIPAMHostSubnet{
		r: reconcileEgressIPAM,
	}, &IsCreatedOrIsEgressCIDRsChanged)
	if err != nil {
		return err
	}

	IsAnnotated := predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			_, okold := e.MetaOld.GetAnnotations()[namespaceAnnotation]
			_, oknew := e.MetaNew.GetAnnotations()[namespaceAnnotation]
			return okold || oknew
		},
		CreateFunc: func(e event.CreateEvent) bool {
			_, ok := e.Meta.GetAnnotations()[namespaceAnnotation]
			return ok
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return false
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return false
		},
	}

	//Watch for namespaces
	err = c.Watch(&source.Kind{Type: &corev1.Namespace{
		TypeMeta: metav1.TypeMeta{
			Kind: "Namespace",
		},
	}}, &enqueForSelectedEgressIPAMNamespace{
		r: reconcileEgressIPAM,
	}, &IsAnnotated)
	if err != nil {
		return err
	}

	IsCreatedOrIsEgressIPsChanged := predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldNetNamespace, ok := e.ObjectOld.(*ocpnetv1.NetNamespace)
			if !ok {
				log.Info("unable to convert event object to NetNamespace,", "event", e)
				return false
			}
			newNetNamespace, ok := e.ObjectNew.(*ocpnetv1.NetNamespace)
			if !ok {
				log.Info("unable to convert event object to NetNamespace,", "event", e)
				return false
			}
			return reflect.DeepEqual(oldNetNamespace.EgressIPs, newNetNamespace.EgressIPs)
		},
		CreateFunc: func(e event.CreateEvent) bool {
			return true
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return false
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return false
		},
	}

	// Watch for netnamespace
	err = c.Watch(&source.Kind{Type: &ocpnetv1.NetNamespace{
		TypeMeta: metav1.TypeMeta{
			Kind: "NetNamespace",
		},
	}}, &enqueForSelectedEgressIPAMNetNamespace{
		r: reconcileEgressIPAM,
	}, &IsCreatedOrIsEgressIPsChanged)
	if err != nil {
		return err
	}

	return nil
}

// blank assignment to verify that ReconcileEgressIPAM implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileEgressIPAM{}

// ReconcileEgressIPAM reconciles a EgressIPAM object
type ReconcileEgressIPAM struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	util.ReconcilerBase
}

// Reconcile reads that state of the cluster for a EgressIPAM object and makes changes based on the state read
// and what is in the EgressIPAM.Spec
// TODO(user): Modify this Reconcile function to implement your Controller logic.  This example creates
// a Pod as an example
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileEgressIPAM) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling EgressIPAM")

	// Fetch the EgressIPAM instance
	instance := &redhatcopv1alpha1.EgressIPAM{}
	err := r.GetClient().Get(context.TODO(), request.NamespacedName, instance)
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

	// high-level description of the reconciliation cycle
	// 1. load all the nodes that comply with the additional filters of this egressIPAM
	// 2. sort the nodes by node_label/value so to have a maps of CIDR:[]node
	// 3. verify that each corresponding hostsubnet has the correct CIDR
	// 4. load all the namespaces with the egressIP annotation that applies to this egressIPAM
	// 5. load from the status the current IP assignments so to have a map namespace:[]ip
	// 6. verify that each namespace in the list at #4 has an ip, if not assign an ip finding the next available in the corresponding range.
	// 7. ensure that all corresponsing netnamespace have the egressIP assigned as per the map
	// 8. save the new assigment in the status
	// 9. TBD annotate the namespace with the new IP.

	return reconcile.Result{}, nil
}
