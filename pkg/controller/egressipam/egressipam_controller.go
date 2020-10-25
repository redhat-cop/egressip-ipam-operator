package egressipam

import (
	"context"
	errs "errors"
	"net"
	"reflect"

	"github.com/hashicorp/go-multierror"
	ocpconfigv1 "github.com/openshift/api/config/v1"
	ocpnetv1 "github.com/openshift/api/network/v1"
	redhatcopv1alpha1 "github.com/redhat-cop/egressip-ipam-operator/pkg/apis/redhatcop/v1alpha1"
	"github.com/redhat-cop/operator-utils/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	controllerName = "egressipam-controller"

	// NamespaceAnnotation is the defining annotation on a namespace that will switch on this operator to handle
	// EgressIPs for the annotated namespace
	NamespaceAnnotation = "egressip-ipam-operator.redhat-cop.io/egressipam"

	// NamespaceAssociationAnnotation is a comma-separated list of assigned ip address. There should be an IP from each of the CIDRs in the egressipam
	NamespaceAssociationAnnotation = "egressip-ipam-operator.redhat-cop.io/egressips"
)

var log = logf.Log.WithName(controllerName)

/**
* USER ACTION REQUIRED: This is a scaffold file intended for the user to modify with their own Controller
* business logic.  Delete these comments after modifying this file.*
 */

// Add creates a new EgressIPAM Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	r, err := newReconciler(mgr)
	if err != nil {
		return err
	}

	return add(mgr, r)
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) (reconcile.Reconciler, error) {
	var ocpClient OcpClient = &OcpClientImplementation{
		ClientConfig: mgr.GetConfig(),
		Scheme:       mgr.GetScheme(),
	}
	if err := ocpClient.Initialize(); err != nil {
		return nil, err
	}

	result := &ReconcileEgressIPAM{
		ReconcilerBase: util.NewReconcilerBase(mgr.GetClient(), mgr.GetScheme(), mgr.GetConfig(), mgr.GetEventRecorderFor(controllerName)),

		ocpClient: &ocpClient,
	}

	cloudProvider, err := result.createCloudProvider()
	if err != nil {
		return nil, err
	}

	result.cloudProvider = &cloudProvider

	return result, nil
}

func (r *ReconcileEgressIPAM) createCloudProvider() (Cloudprovider, error) {
	var result Cloudprovider

	infrastructure, err := r.getInfrastructure()
	if err != nil {
		log.Error(err, "Unable to retrieve current cluster infrastructure")
		return nil, err
	}

	log.V(1).Info("initializing cloud provider", "provider", infrastructure.Status.PlatformStatus.Type)
	result = &BareMetalCloudProvider{}

	switch infrastructure.Status.PlatformStatus.Type {
	case ocpconfigv1.AWSPlatformType:
		result = &AwsCloudprovider{
			OcpClient: r.ocpClient,
			AwsRegion: infrastructure.Status.PlatformStatus.AWS.Region,
		}
		break
	case ocpconfigv1.AzurePlatformType:
		result = &AzureCloudProvider{}
		break
	case ocpconfigv1.OpenStackPlatformType:
		result = &OpenStackCloudProvider{}
		break
	case ocpconfigv1.VSpherePlatformType:
		result = &VsphereCloudProvider{}
		break
	// Ovirt, Libvirt and bare metal are handled all the same.
	case ocpconfigv1.OvirtPlatformType:
	case ocpconfigv1.LibvirtPlatformType:
	case ocpconfigv1.BareMetalPlatformType:
	default:
		result = &BareMetalCloudProvider{}
	}

	if err := result.Initialize(r); err != nil {
		log.Error(err, "unable to initialize the cloud provider")
		return nil, err
	}

	return result, nil
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
	}}, &handler.EnqueueRequestForObject{}, util.ResourceGenerationOrFinalizerChangedPredicate{})
	if err != nil {
		return err
	}

	IsCreatedOrDeleted := predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			return false
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
	}}, &enqueueForSelectingEgressIPAMNode{
		r: reconcileEgressIPAM,
	}, &IsCreatedOrDeleted)
	if err != nil {
		return err
	}

	IsCreatedORDeletedOrIsEgressCIDRsChanged := predicate.Funcs{
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
			return !reflect.DeepEqual(oldHostSubnet.EgressCIDRs, newHostSubnet.EgressCIDRs) || !reflect.DeepEqual(oldHostSubnet.EgressIPs, newHostSubnet.EgressIPs)
		},
		CreateFunc: func(e event.CreateEvent) bool {
			return true
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return true
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
	}, &IsCreatedORDeletedOrIsEgressCIDRsChanged)
	if err != nil {
		return err
	}

	IsAnnotated := predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			_, okold := e.MetaOld.GetAnnotations()[NamespaceAnnotation]
			_, oknew := e.MetaNew.GetAnnotations()[NamespaceAnnotation]
			_, ipsold := e.MetaOld.GetAnnotations()[NamespaceAssociationAnnotation]
			_, ipsnew := e.MetaNew.GetAnnotations()[NamespaceAssociationAnnotation]
			return (!okold && oknew) || (ipsnew != ipsold)
		},
		CreateFunc: func(e event.CreateEvent) bool {
			_, ok := e.Meta.GetAnnotations()[NamespaceAnnotation]
			return ok
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			_, ok := e.Meta.GetAnnotations()[NamespaceAnnotation]
			return ok
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
			return !reflect.DeepEqual(oldNetNamespace.EgressIPs, newNetNamespace.EgressIPs)
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
	// This Client, initialized using mgr.Client() above, is a split Client
	// that reads objects from the cache and writes to the apiserver
	util.ReconcilerBase
	infrastructure ocpconfigv1.Infrastructure

	ocpClient     *OcpClient
	cloudProvider *Cloudprovider
}

func (r *ReconcileEgressIPAM) getDirectClient() (client.Client, error) {
	result, err := client.New(r.GetRestConfig(), client.Options{})
	if err != nil {
		//goland:noinspection SpellCheckingInspection
		log.Error(err, "unable to create result", "with restconfig", r.GetRestConfig())
		return nil, err
	}

	return result, nil
}

// Reconcile reads that state of the cluster for a EgressIPAM object and makes changes based on the state read
// and what is in the EgressIPAM.Spec
//
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

	if ok, err := r.IsValid(instance); !ok {
		return r.ManageError(instance, err)
	}

	if ok := r.IsInitialized(instance); !ok {
		err := r.GetClient().Update(context.TODO(), instance)
		if err != nil {
			log.Error(err, "unable to update instance", "instance", instance.GetName())
			return r.ManageError(instance, err)
		}
		return reconcile.Result{}, nil
	}

	if util.IsBeingDeleted(instance) {
		if !util.HasFinalizer(instance, controllerName) {
			return reconcile.Result{}, nil
		}

		err := r.manageCleanUpLogic(instance)
		if err != nil {
			log.Error(err, "unable to delete instance", "instance", instance.GetName())
			return r.ManageError(instance, err)
		}
		util.RemoveFinalizer(instance, controllerName)
		err = r.GetClient().Update(context.TODO(), instance)
		if err != nil {
			log.Error(err, "unable to update instance", "instance", instance.GetName())
			return r.ManageError(instance, err)
		}

		return r.ManageSuccess(instance)
	}

	// load the reconcile context
	rc, err := r.loadReconcileContext(instance)
	if err != nil {
		log.Error(err, "unable to load the reconcile context", "instance", instance.GetName())
		return r.ManageError(instance, err)
	}

	// assign IPs To namespaces that don't have an IP, this will update the namespace assigned IP annotation and handle
	// the cloud provider
	newlyAssignedNamespaces, err := r.assignIPsToNamespaces(rc)
	if err != nil {
		log.Error(err, "unable to assign IPs to unassigned ", "namespaces", rc.UnAssignedNamespaces)
		return r.ManageError(instance, err)
	}

	log.V(1).Info("", "newlyAssignedNamespaces", getNamespaceNames(newlyAssignedNamespaces))

	rc.FinallyAssignedNamespaces = append(rc.InitiallyAssignedNamespaces, newlyAssignedNamespaces...)

	log.V(1).Info("", "FinallyAssignedNamespaces", getNamespaceNames(rc.FinallyAssignedNamespaces))

	//ensure that corresponding netnamespaces have the correct IPs
	err = r.reconcileNetNamespaces(rc)
	if err != nil {
		log.Error(err, "unable to reconcile netnamespace for ", "namespaces", rc.FinallyAssignedNamespaces)
		return r.ManageError(instance, err)
	}

	err = r.assignCIDRsToHostSubnets(rc)
	if err != nil {
		log.Error(err, "unable to assign CIDR to hostsubnets from ", "nodesByCIDR", rc.SelectedNodesByCIDR)
		return r.ManageError(instance, err)
	}

	// Cloud provider specific reconciliation
	if err := (*r.cloudProvider).Reconcile(rc); err != nil {
		return r.ManageError(instance, err)
	}

	return r.ManageSuccess(instance)
}

func (r *ReconcileEgressIPAM) getInfrastructure() (*ocpconfigv1.Infrastructure, error) {
	if !reflect.DeepEqual(r.infrastructure, ocpconfigv1.Infrastructure{}) {
		return &r.infrastructure, nil
	}
	infrastructure := &ocpconfigv1.Infrastructure{}
	c, err := client.New(r.GetRestConfig(), client.Options{})
	if err != nil {
		log.Error(err, "unable to create Client from ", "config", r.GetRestConfig())
		return &ocpconfigv1.Infrastructure{}, err
	}
	err = c.Get(context.TODO(), types.NamespacedName{
		Name: "cluster",
	}, infrastructure)
	if err != nil {
		log.Error(err, "unable to retrieve cluster's infrastructure resource ")
		return &ocpconfigv1.Infrastructure{}, err
	}
	r.infrastructure = *infrastructure
	return &r.infrastructure, nil
}

//IsValid check if the instance is valid. In particular it checks that the CIDRs and the reservedIPs can be parsed correctly
func (r *ReconcileEgressIPAM) IsValid(obj metav1.Object) (bool, error) {
	egressIPAM, ok := obj.(*redhatcopv1alpha1.EgressIPAM)
	if !ok {
		return false, errs.New("unable to convert to EgressIPAM")
	}
	for _, CidrAssignment := range egressIPAM.Spec.CIDRAssignments {
		_, cidr, err := net.ParseCIDR(CidrAssignment.CIDR)
		if err != nil {
			log.Error(err, "unable to convert to", "cidr", CidrAssignment.CIDR)
			return false, err
		}
		for _, ipstr := range CidrAssignment.ReservedIPs {
			ip := net.ParseIP(ipstr)
			if ip == nil {
				err := errs.New("unable to parse IP: " + ipstr)
				log.Error(err, "unable to parse", "IP", ipstr)
				return false, err
			}
			if !cidr.Contains(ip) {
				err := errs.New("IP " + ipstr + " not contained in relative CIDR: " + CidrAssignment.CIDR)
				log.Error(err, "not contained", "IP", ip, "cidr", cidr)
				return false, err
			}
		}
	}
	return true, nil
}

//IsInitialized initializes the instance, currently is simply adds a finalizer.
func (r *ReconcileEgressIPAM) IsInitialized(obj metav1.Object) bool {
	isInitialized := true
	egressIPAM, ok := obj.(*redhatcopv1alpha1.EgressIPAM)
	if !ok {
		log.Error(errs.New("unable to convert to EgressIPAM"), "unable to convert to EgressIPAM")
		return false
	}
	if !util.HasFinalizer(egressIPAM, controllerName) {
		util.AddFinalizer(egressIPAM, controllerName)
		isInitialized = false
	}
	return isInitialized
}

func (r *ReconcileEgressIPAM) manageCleanUpLogic(instance *redhatcopv1alpha1.EgressIPAM) error {
	// load the reconcile context
	rc, err := r.loadReconcileContext(instance)
	if err != nil {
		log.Error(err, "unable to load the reconcile context", "instance", instance)
		return err
	}

	// remove assigned IPs from namespaces
	err = r.removeNamespaceAssignedIPs(rc)
	if err != nil {
		log.Error(err, "unable to remove IPs assigned to namespaces referring to ", "EgressIPAM", rc.EgressIPAM.GetName())
		return err
	}

	// remove assigned IPs from netnamespaces
	err = r.removeNetnamespaceAssignedIPs(rc)
	if err != nil {
		log.Error(err, "unable to remove IPs assigned to netnamespaces referring to ", "EgressIPAM", rc.EgressIPAM.GetName())
		return err
	}

	// remove all assigned IPs/CIDRs from hostsubnets
	err = r.removeHostsubnetAssignedIPsAndCIDRs(rc)
	if err != nil {
		log.Error(err, "unable to remove IPs/CIDRs assigned to hostsubnets selected by ", "EgressIPAM", rc.EgressIPAM.GetName())
		return err
	}

	// Handle the cloud provider specific removal
	return (*r.cloudProvider).CleanUpCloudProvider(rc)
}

// ReconcileContext is the data model of the operator
type ReconcileContext struct {
	//immutable fields within a reconciliation cycle
	EgressIPAM                  *redhatcopv1alpha1.EgressIPAM
	CIDRs                       []string
	NetCIDRByCIDR               map[string]*net.IPNet
	CIDRsByLabel                map[string]string
	ReservedIPsByCIDR           map[string][]net.IP
	AllNodes                    map[string]corev1.Node
	AllHostSubnets              map[string]ocpnetv1.HostSubnet
	SelectedNodes               map[string]corev1.Node
	SelectedNodesByCIDR         map[string][]string
	SelectedHostSubnets         map[string]ocpnetv1.HostSubnet
	SelectedHostSubnetsByCIDR   map[string][]string
	ReferringNamespaces         map[string]corev1.Namespace
	InitiallyAssignedNamespaces []corev1.Namespace
	UnAssignedNamespaces        []corev1.Namespace
	NetNamespaces               map[string]ocpnetv1.NetNamespace
	InitiallyAssignedIPsByNode  map[string][]string

	// Cloud Region
	CloudRegion string

	//variable fields
	FinallyAssignedNamespaces []corev1.Namespace
	FinallyAssignedIPsByNode  map[string][]string
}

func (r *ReconcileEgressIPAM) loadReconcileContext(egressIPAM *redhatcopv1alpha1.EgressIPAM) (*ReconcileContext, error) {
	rc := &ReconcileContext{
		EgressIPAM: egressIPAM,
	}
	var CIDRs []string
	CIDRsByLabel := map[string]string{}
	reservedIPsByCIDR := map[string][]net.IP{}
	netCIDRByCIDR := map[string]*net.IPNet{}
	for _, cidrAssignment := range egressIPAM.Spec.CIDRAssignments {
		CIDRs = append(CIDRs, cidrAssignment.CIDR)
		CIDRsByLabel[cidrAssignment.LabelValue] = cidrAssignment.CIDR
		var IPs []net.IP
		for _, ipstr := range cidrAssignment.ReservedIPs {
			IP := net.ParseIP(ipstr)
			if IP == nil {
				err := errs.New("unable to parse IP: " + ipstr)
				log.Error(err, "unable to parse", "IP", ipstr)
				return &ReconcileContext{}, err
			}
			IPs = append(IPs, IP)
		}
		reservedIPsByCIDR[cidrAssignment.CIDR] = IPs
		_, CIDR, err := net.ParseCIDR(cidrAssignment.CIDR)
		if err != nil {
			log.Error(err, "unable to parse ", "cidr", cidrAssignment.CIDR)
			return &ReconcileContext{}, err
		}
		netCIDRByCIDR[cidrAssignment.CIDR] = CIDR
	}

	rc.CIDRs = CIDRs
	rc.CIDRsByLabel = CIDRsByLabel
	rc.ReservedIPsByCIDR = reservedIPsByCIDR
	rc.NetCIDRByCIDR = netCIDRByCIDR

	log.V(1).Info("", "CIDRs", rc.CIDRs)
	log.V(1).Info("", "CIDRsByLabel", rc.CIDRsByLabel)
	log.V(1).Info("", "ReservedIPsByCIDR", rc.ReservedIPsByCIDR)
	log.V(1).Info("", "NetCIDRByCIDR", rc.NetCIDRByCIDR)

	results := make(chan error)
	defer close(results)
	// nodes
	go func() {
		allNodes, err := r.getAllNodes(rc)
		if err != nil {
			log.Error(err, "unable to get all nodes")
			results <- err
			return
		}
		rc.AllNodes = allNodes
		results <- nil
		return
	}()

	//hostsubnets
	go func() {
		allHostSubnets, err := r.getAllHostSubnets(rc)
		if err != nil {
			log.Error(err, "unable to get all hostsubnets")
			results <- err
			return
		}
		rc.AllHostSubnets = allHostSubnets
		results <- nil
		return
	}()

	//namespaces
	go func() {
		referringNamespaces, unAssignedNamespaces, assignedNamespaces, err := r.getReferringNamespaces(rc)
		if err != nil {
			log.Error(err, "unable to determine referring namespace for", "EgressIPAM", egressIPAM.GetName())
			results <- err
			return
		}

		rc.ReferringNamespaces = referringNamespaces
		rc.InitiallyAssignedNamespaces = assignedNamespaces
		rc.UnAssignedNamespaces = unAssignedNamespaces

		log.V(1).Info("", "ReferringNamespaces", getNamespaceMapKeys(rc.ReferringNamespaces))
		log.V(1).Info("", "InitiallyAssignedNamespaces", getNamespaceNames(rc.InitiallyAssignedNamespaces))
		log.V(1).Info("", "UnAssignedNamespaces", getNamespaceNames(rc.UnAssignedNamespaces))
		results <- nil
		return
	}()

	//netnamespace
	go func() {
		netNamespaces, err := r.getAllNetNamespaces(rc)
		if err != nil {
			log.Error(err, "unable to load netnamespaces")
			results <- err
			return
		}

		rc.NetNamespaces = netNamespaces

		log.V(1).Info("", "NetNamespaces", getNetNamespaceMapKeys(rc.NetNamespaces))
		results <- nil
		return
	}()

	//collect results
	result := &multierror.Error{}
	for range []string{"nodes", "hostsubnets", "namespaces", "netnamespaces"} {
		err := <-results
		log.V(1).Info("receiving", "error", err)
		_ = multierror.Append(result, err)
	}

	if result.ErrorOrNil() != nil {
		log.Error(result, "unable ro run parallel initialization")
		return &ReconcileContext{}, result
	}

	selectedNodes, err := r.getSelectedNodes(rc)
	if err != nil {
		log.Error(err, "unable to get selected nodes for", "EgressIPAM", egressIPAM.GetName())
		return &ReconcileContext{}, err
	}
	rc.SelectedNodes = selectedNodes
	log.V(1).Info("", "SelectedNodes", getNodeNames(rc.SelectedNodes))

	selectedHostSubnets := map[string]ocpnetv1.HostSubnet{}
	for hostsubnetName, hostsubnet := range rc.AllHostSubnets {
		if _, ok := rc.SelectedNodes[hostsubnetName]; ok {
			selectedHostSubnets[hostsubnetName] = hostsubnet
		}
	}

	rc.SelectedHostSubnets = selectedHostSubnets
	log.V(1).Info("", "SelectedHostSubnets", getHostSubnetNames(rc.SelectedHostSubnets))

	selectedNodesByCIDR := map[string][]string{}
	selectedHostSubnetsByCIDR := map[string][]string{}
	for nodeName, node := range rc.SelectedNodes {
		if value, ok := node.GetLabels()[egressIPAM.Spec.TopologyLabel]; ok {
			if cidr, ok := CIDRsByLabel[value]; ok {
				selectedNodesByCIDR[cidr] = append(selectedNodesByCIDR[cidr], nodeName)
				selectedHostSubnetsByCIDR[cidr] = append(selectedHostSubnetsByCIDR[cidr], nodeName)
			}
		}
	}
	rc.SelectedNodesByCIDR = selectedNodesByCIDR
	rc.SelectedHostSubnetsByCIDR = selectedHostSubnetsByCIDR

	log.V(1).Info("", "SelectedNodesByCIDR", rc.SelectedNodesByCIDR)
	log.V(1).Info("", "selectedHostSubnetByCIDR", rc.SelectedHostSubnetsByCIDR)

	if err := (*r.cloudProvider).CollectCloudData(rc); err != nil {
		log.Error(err, "unable to retrieve the cloud information")
		return &ReconcileContext{}, err
	}

	return rc, nil
}
