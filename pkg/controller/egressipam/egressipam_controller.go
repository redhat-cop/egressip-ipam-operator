package egressipam

import (
	"context"
	"reflect"

	ocpconfigv1 "github.com/openshift/api/config/v1"
	ocpnetv1 "github.com/openshift/api/network/v1"
	redhatcopv1alpha1 "github.com/redhat-cop/egressip-ipam-operator/pkg/apis/redhatcop/v1alpha1"
	"github.com/redhat-cop/operator-utils/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
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

// this is a comma-separated list of assigned ip address. There should be an IP from each of the CIDRs in the egressipam
const namespaceAssociationAnnotation = "egressip-ipam-operator.redhat-cop.io/egressips"

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

	// high-level description of the reconciliation cycle for bare metal
	// 1. load all the nodes that comply with the additional filters of this egressIPAM
	// 2. sort the nodes by node_label/value so to have a maps of CIDR:[]node
	// 3. verify that each corresponding hostsubnet has the correct CIDR
	// 4. load all the namespaces with the egressIP annotation that applies to this egressIPAM
	// 5. load from the status the current IP assignments so to have a map namespace:[]ip
	// 6. verify that each namespace in the list at #4 has an ip, if not assign an ip finding the next available in the corresponding range.
	// 7. ensure that all corresponding netnamespace have the egressIP assigned as per the map
	// 8. annotate the assigment on the namespace

	// high-level descritpion of the reconciliation cycle for AWS
	// 1. load all the nodes that comply with the additional filters of this egressIPAM
	// 2. from the status load the map of assigned egress ip -> egressip:namespace:node
	// 3. load all the namespaces with the egressIP annotation that applies to this egressIPAM
	// 4. create a left and right outer joins between the namespaces at #3 and at #2.
	// 5. for namespaces that don't need egress IP anymore remove the annotation on the hostsubnet and the assiciation of ip:interface in AWS
	// 5.1. remove the egressIP from the hostsubnet
	// 5.2. remove the agressIP association with the AWS interface
	// 6. for namespaces that need an egress IP, select and new IP, and a node in the correct AZ
	// 6.1. add the association in AWS
	// 6.2. add the egressIP to the hostsubnet
	// 6.3. add the egressIP to the netnamespace
	// 7. annotate the assigments on the namespace

	//get all namespaces that refer this instance and opt in into egressIPs
	unassignedNamespaces, assignedNamespaces, err := r.getReferringNamespaces(instance)
	if err != nil {
		log.Error(err, "unable to retrieve and sort referring namespaces for", "instance", instance)
		return r.ManageError(instance, err)
	}

	//assign IPs To namespaces that don't have an IP, this will update the namespace assigned IP annotation
	newlyAssignedNamespaces, err := r.assignIPsToNamespaces(unassignedNamespaces, assignedNamespaces, instance)
	if err != nil {
		log.Error(err, "unable to assign IPs to unassigned ", "namespaces", unassignedNamespaces)
		return r.ManageError(instance, err)
	}

	assignedNamespaces = append(assignedNamespaces, newlyAssignedNamespaces...)

	//ensure that corresponding netnamespaces have the correct IPs
	err = r.reconcileNetNamespaces(assignedNamespaces)
	if err != nil {
		log.Error(err, "unable to reconcile netnamespace for ", "namespaces", assignedNamespaces)
		return r.ManageError(instance, err)
	}

	infrastrcuture, err := r.getInfrastructure()
	if err != nil {
		log.Error(err, "unable to retrieve cluster infrastrcuture information")
		return r.ManageError(instance, err)
	}

	//baremetal
	if infrastrcuture.Status.Platform == ocpconfigv1.NonePlatformType {
		nodesByCIDR, err := r.getSelectedNodesByCIDR(instance)
		if err != nil {
			log.Error(err, "unable to get nodes selected by ", "instance", instance)
			return r.ManageError(instance, err)
		}
		err = r.assignCIDRsToHostSubnets(nodesByCIDR)
		if err != nil {
			log.Error(err, "unable to assigne CIDR to hostsubnets from ", "nodesByCIDR", nodesByCIDR)
			return r.ManageError(instance, err)
		}
		return r.ManageSuccess(instance)
	}

	if infrastrcuture.Status.Platform == ocpconfigv1.AWSPlatformType {
		nodesByCIDR, err := r.getSelectedNodesByCIDR(instance)
		if err != nil {
			log.Error(err, "unable to get nodes selected by ", "instance", instance)
			return r.ManageError(instance, err)
		}
		assignedIPsByCIDR, err := sortIPsByCIDR(assignedNamespaces, instance)
		if err != nil {
			log.Error(err, "unable sort assigned IPs by CIDR")
			return r.ManageError(instance, err)
		}
		awsAssignedIPsByNode, err := r.getAWSAssignedIPsByNode(nodesByCIDR, instance)
		if err != nil {
			log.Error(err, "unable to get assigned AWS secondary IPs")
			return r.ManageError(instance, err)
		}
		err = r.removeAWSUnusedIPs(awsAssignedIPsByNode, assignedIPsByCIDR)
		if err != nil {
			log.Error(err, "unable to remove AWS unused IPs")
			return r.ManageError(instance, err)
		}
		assignedIPsByNode, err := assignIPsToNodes(nodesByCIDR, assignedIPsByCIDR)
		if err != nil {
			log.Error(err, "unable to assign egress IPs to nodes")
			return r.ManageError(instance, err)
		}
		err = r.reconcileAWSAssignedIPs(assignedIPsByNode, instance)
		if err != nil {
			log.Error(err, "unable to assign egress IPs to aws machines")
			return r.ManageError(instance, err)
		}
		err = r.reconcileHSAssignedIPs(assignedIPsByNode)
		if err != nil {
			log.Error(err, "unable to reconcile hostsubnest with ", "nodes", assignedIPsByNode)
			return r.ManageError(instance, err)
		}
	}

	return r.ManageSuccess(instance)
}

func (r *ReconcileEgressIPAM) getInfrastructure() (*ocpconfigv1.Infrastructure, error) {
	infrastructure := &ocpconfigv1.Infrastructure{}
	err := r.GetClient().Get(context.TODO(), types.NamespacedName{
		Name: "cluster",
	}, infrastructure)
	if err != nil {
		log.Error(err, "unable to retrieve cluster's infrastrcuture resource ")
		return &ocpconfigv1.Infrastructure{}, err
	}
	return infrastructure, nil
}
