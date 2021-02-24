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

package egressipam

import (
	"context"
	errs "errors"
	"net"
	"reflect"

	"github.com/go-logr/logr"
	multierror "github.com/hashicorp/go-multierror"
	ocpconfigv1 "github.com/openshift/api/config/v1"
	ocpnetv1 "github.com/openshift/api/network/v1"
	cloudcredentialv1 "github.com/openshift/cloud-credential-operator/pkg/apis/cloudcredential/v1"
	machinev1beta1 "github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"
	redhatcopv1alpha1 "github.com/redhat-cop/egressip-ipam-operator/api/v1alpha1"
	"github.com/redhat-cop/egressip-ipam-operator/controllers/egressipam/aws"
	"github.com/redhat-cop/egressip-ipam-operator/controllers/egressipam/azure"
	"github.com/redhat-cop/egressip-ipam-operator/controllers/egressipam/baremetal"
	"github.com/redhat-cop/egressip-ipam-operator/controllers/egressipam/reconcilecontext"
	"github.com/redhat-cop/operator-utils/pkg/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// name of the secret with the credential (cloud independent)
const credentialsSecretName = "egress-ipam-operator-cloud-credentials"

const NamespaceAnnotation = "egressip-ipam-operator.redhat-cop.io/egressipam"

// this is a comma-separated list of assigned ip address. There should be an IP from each of the CIDRs in the egressipam
const NamespaceAssociationAnnotation = "egressip-ipam-operator.redhat-cop.io/egressips"

// EgressIPAMReconciler reconciles a EgressIPAM object
type EgressIPAMReconciler struct {
	util.ReconcilerBase
	Log            logr.Logger
	controllerName string
	infrastructure *ocpconfigv1.Infrastructure
}

// +kubebuilder:rbac:groups=redhatcop.redhat.io,resources=egressipams,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=redhatcop.redhat.io,resources=egressipams/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=redhatcop.redhat.io,resources=egressipams/finalizers,verbs=update

// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="cloudcredential.openshift.io",resources=credentialsrequests,verbs=get;list;watch;create;update
// +kubebuilder:rbac:groups="machine.openshift.io",resources=machinesets,verbs=get;list;watch
// +kubebuilder:rbac:groups="machine.openshift.io",resources=machines,verbs=get;list;watch
// +kubebuilder:rbac:groups="config.openshift.io",resources=infrastructures,verbs=get;list;watch
// +kubebuilder:rbac:groups="network.openshift.io",resources=hostsubnets,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups="network.openshift.io",resources=netnamespaces,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups="",resources=events,verbs=get;list;watch;create;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the EgressIPAM object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.7.0/pkg/reconcile
func (r *EgressIPAMReconciler) Reconcile(context context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("egressipam", req.NamespacedName)

	// your logic here

	// Fetch the EgressIPAM instance
	instance := &redhatcopv1alpha1.EgressIPAM{}
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

	if ok, err := r.IsValid(instance); !ok {
		return r.ManageError(context, instance, err)
	}

	if ok := r.IsInitialized(instance); !ok {
		err := r.GetClient().Update(context, instance)
		if err != nil {
			log.Error(err, "unable to update instance", "instance", instance.GetName())
			return r.ManageError(context, instance, err)
		}
		return reconcile.Result{}, nil
	}

	if util.IsBeingDeleted(instance) {
		if !util.HasFinalizer(instance, r.controllerName) {
			return reconcile.Result{}, nil
		}
		err := r.manageCleanUpLogic(context, instance)
		if err != nil {
			log.Error(err, "unable to delete instance", "instance", instance.GetName())
			return r.ManageError(context, instance, err)
		}
		util.RemoveFinalizer(instance, r.controllerName)
		err = r.GetClient().Update(context, instance)
		if err != nil {
			log.Error(err, "unable to update instance", "instance", instance.GetName())
			return r.ManageError(context, instance, err)
		}
		return reconcile.Result{}, nil
	}
	// load the reconcile context
	rc, err := r.loadReconcileContext(context, instance)
	if err != nil {
		log.Error(err, "unable to load the reconcile context", "instance", instance.GetName())
		return r.ManageError(context, instance, err)
	}

	return r.processReconcileContext(rc)

}

func (r *EgressIPAMReconciler) processReconcileContext(rc *reconcilecontext.ReconcileContext) (ctrl.Result, error) {
	// high-level common desing for all platforms:
	// 1. load all namespaces referring this egressIPAM and sort them between those that have egress IPs assigned and those who haven't
	// 2. assign egress IPs to namespaces that don't have IPs assigned. One IP per CIDR from egressIPAM. Pick IPs that are available in that CIDR, based on the already assigned IPs
	// 3. update namespace assignment annotation (this is the source of truth for assignements)
	// 4. reconcile netnamespaces
	//assign IPs To namespaces that don't have an IP, this will update the namespace assigned IP annotation
	newlyAssignedNamespaces, err := r.assignIPsToNamespaces(rc)
	if err != nil {
		r.Log.Error(err, "unable to assign IPs to unassigned ", "namespaces", rc.UnAssignedNamespaces)
		return r.ManageError(rc.Context, rc.EgressIPAM, err)
	}

	r.Log.V(1).Info("", "newlyAssignedNamespaces", getNamespaceNames(newlyAssignedNamespaces))

	rc.FinallyAssignedNamespaces = append(rc.InitiallyAssignedNamespaces, newlyAssignedNamespaces...)

	r.Log.V(1).Info("", "finallyAssignedNamespaces", getNamespaceNames(rc.FinallyAssignedNamespaces))

	//ensure that corresponding netnamespaces have the correct IPs
	err = r.reconcileNetNamespaces(rc)
	if err != nil {
		r.Log.Error(err, "unable to reconcile netnamespace for ", "namespaces", rc.FinallyAssignedNamespaces)
		return r.ManageError(rc.Context, rc.EgressIPAM, err)
	}

	// new code

	assignedIPsByNode := r.getAssignedIPsByNode(rc)
	rc.InitiallyAssignedIPsByNode = assignedIPsByNode

	r.Log.V(1).Info("", "initiallyAssignedIPsByNode", rc.InitiallyAssignedIPsByNode)

	finallyAssignedIPsByNode, err := r.assignIPsToNodes(rc)
	if err != nil {
		r.Log.Error(err, "unable to assign egress IPs to nodes")
		return r.ManageError(rc.Context, rc.EgressIPAM, err)
	}
	r.Log.V(1).Info("", "finallyAssignedIPsByNode", finallyAssignedIPsByNode)

	rc.FinallyAssignedIPsByNode = finallyAssignedIPsByNode

	err = rc.Infra.ReconcileInstanceSecondaryIPs(rc)

	if err != nil {
		r.Log.Error(err, "unable to reconcile instances' secondary IPs")
		return r.ManageError(rc.Context, rc.EgressIPAM, err)
	}

	if isCloudInfrastructure(rc.Infrastructure) {
		err = r.reconcileHSAssignedIPs(rc)
		if err != nil {
			r.Log.Error(err, "unable to reconcile hostsubnest with ", "nodes", assignedIPsByNode)
			return r.ManageError(rc.Context, rc.EgressIPAM, err)
		}
	} else {
		err = r.assignCIDRsToHostSubnets(rc)
		if err != nil {
			r.Log.Error(err, "unable to assigne CIDR to hostsubnets from ", "nodesByCIDR", rc.SelectedNodesByCIDR)
			return r.ManageError(rc.Context, rc.EgressIPAM, err)
		}
	}

	return r.ManageSuccess(rc.Context, rc.EgressIPAM)
}

// GetInfrastructure return the openshift infrastructure object, notice that this is looked up only once in the duration of the operatgro lifecyle and the it's cached.
func (r *EgressIPAMReconciler) GetInfrastructure() *ocpconfigv1.Infrastructure {
	return r.infrastructure
}

// GetCredentialSecret returs the credentials secret to be used to instantiate cloud providers
func (r *EgressIPAMReconciler) GetCredentialSecret(context context.Context) (*corev1.Secret, error) {
	namespace, err := r.GetOperatorNamespace()
	if err != nil {
		r.Log.Error(err, "unable to get operator's namespace")
		return &corev1.Secret{}, err
	}

	credentialSecret := &corev1.Secret{}
	err = r.GetClient().Get(context, types.NamespacedName{
		Name:      credentialsSecretName,
		Namespace: namespace,
	}, credentialSecret)
	if err != nil {
		r.Log.Error(err, "unable to retrive aws credential ", "secret", types.NamespacedName{
			Name:      credentialsSecretName,
			Namespace: namespace,
		})
		return &corev1.Secret{}, err
	}

	return credentialSecret, nil
}

//IsValid check if the instance is valid. In particular it checks that the CIDRs and the reservedIPs can be parsed correctly
func (r *EgressIPAMReconciler) IsValid(obj client.Object) (bool, error) {
	ergessIPAM, ok := obj.(*redhatcopv1alpha1.EgressIPAM)
	if !ok {
		return false, errs.New("unable to convert to egressIPAM")
	}
	for _, CIDRAssignemnt := range ergessIPAM.Spec.CIDRAssignments {
		_, cidr, err := net.ParseCIDR(CIDRAssignemnt.CIDR)
		if err != nil {
			r.Log.Error(err, "unable to convert to", "cidr", CIDRAssignemnt.CIDR)
			return false, err
		}
		for _, ipstr := range CIDRAssignemnt.ReservedIPs {
			ip := net.ParseIP(ipstr)
			if ip == nil {
				err := errs.New("unable to parse IP: " + ipstr)
				r.Log.Error(err, "unable to parse", "IP", ipstr)
				return false, err
			}
			if !cidr.Contains(ip) {
				err := errs.New("IP " + ipstr + " not contained in relative CIDR: " + CIDRAssignemnt.CIDR)
				r.Log.Error(err, "not contained", "IP", ip, "cidr", cidr)
				return false, err
			}
		}
	}
	return true, nil
}

//IsInitialized initislizes the instance, currently is simply adds a finalizer.
func (r *EgressIPAMReconciler) IsInitialized(obj client.Object) bool {
	isInitialized := true
	ergessIPAM, ok := obj.(*redhatcopv1alpha1.EgressIPAM)
	if !ok {
		r.Log.Error(errs.New("unable to convert to egressIPAM"), "unable to convert to egressIPAM")
		return false
	}
	if !util.HasFinalizer(ergessIPAM, r.controllerName) {
		util.AddFinalizer(ergessIPAM, r.controllerName)
		isInitialized = false
	}
	return isInitialized
}

func (r *EgressIPAMReconciler) manageCleanUpLogic(context context.Context, instance *redhatcopv1alpha1.EgressIPAM) error {

	// load the reconcile context
	rc, err := r.loadReconcileContext(context, instance)
	if err != nil {
		r.Log.Error(err, "unable to load the reconcile context", "instance", instance)
		return err
	}

	// remove assigned IPs from namespaces
	err = r.removeNamespaceAssignedIPs(rc)
	if err != nil {
		r.Log.Error(err, "unable to remove IPs assigned to namespaces referring to ", "egressIPAM", rc.EgressIPAM.GetName())
		return err
	}

	// remove assigned IPs from netnamespaces
	err = r.removeNetnamespaceAssignedIPs(rc)
	if err != nil {
		r.Log.Error(err, "unable to remove IPs assigned to netnamespaces referring to ", "egressIPAM", rc.EgressIPAM.GetName())
		return err
	}

	// remove all assigned IPs/CIDRs from hostsubnets
	err = r.removeHostsubnetAssignedIPsAndCIDRs(rc)
	if err != nil {
		r.Log.Error(err, "unable to remove IPs/CIDRs assigned to hostsubnets selected by ", "egressIPAM", rc.EgressIPAM.GetName())
		return err
	}

	// if aws remove all secondary assigned IPs from AWS instances
	if isCloudInfrastructure(rc.Infrastructure) {
		err = rc.Infra.RemoveAllAssignedIPs(rc)
		if err != nil {
			r.Log.Error(err, "unable to remove all assigned IPs to VMs")
			return err
		}
	}
	return nil
}

func isCloudInfrastructure(infrastrucutre *ocpconfigv1.Infrastructure) bool {
	return infrastrucutre.Status.PlatformStatus.Type == ocpconfigv1.AWSPlatformType || infrastrucutre.Status.PlatformStatus.Type == ocpconfigv1.AzurePlatformType || infrastrucutre.Status.PlatformStatus.Type == ocpconfigv1.GCPPlatformType
}

func (r *EgressIPAMReconciler) loadReconcileContext(context context.Context, egressIPAM *redhatcopv1alpha1.EgressIPAM) (*reconcilecontext.ReconcileContext, error) {
	rc := &reconcilecontext.ReconcileContext{
		EgressIPAM: egressIPAM,
		Context:    context,
	}
	rc.Infrastructure = r.GetInfrastructure()

	if isCloudInfrastructure(rc.Infrastructure) {
		cloudCredentialsSecret, err := r.GetCredentialSecret(rc.Context)
		if err != nil {
			r.Log.Error(err, "unable get credentials secret")
			return &reconcilecontext.ReconcileContext{}, err
		}
		rc.CloudCredentialsSecret = cloudCredentialsSecret
	}

	CIDRs := []string{}
	CIDRsByLabel := map[string]string{}
	reservedIPsByCIDR := map[string][]net.IP{}
	netCIDRByCIDR := map[string]*net.IPNet{}
	for _, cidrAssignemnt := range egressIPAM.Spec.CIDRAssignments {
		CIDRs = append(CIDRs, cidrAssignemnt.CIDR)
		CIDRsByLabel[cidrAssignemnt.LabelValue] = cidrAssignemnt.CIDR
		IPs := []net.IP{}
		for _, ipstr := range cidrAssignemnt.ReservedIPs {
			IP := net.ParseIP(ipstr)
			if IP == nil {
				err := errs.New("unable to parse IP: " + ipstr)
				r.Log.Error(err, "unable to parse", "IP", ipstr)
				return &reconcilecontext.ReconcileContext{}, err
			}
			IPs = append(IPs, IP)
		}
		reservedIPsByCIDR[cidrAssignemnt.CIDR] = IPs
		_, CIDR, err := net.ParseCIDR(cidrAssignemnt.CIDR)
		if err != nil {
			r.Log.Error(err, "unable to parse ", "cidr", cidrAssignemnt.CIDR)
			return &reconcilecontext.ReconcileContext{}, err
		}
		netCIDRByCIDR[cidrAssignemnt.CIDR] = CIDR
	}

	rc.CIDRs = CIDRs
	rc.CIDRsByLabel = CIDRsByLabel
	rc.ReservedIPsByCIDR = reservedIPsByCIDR
	rc.NetCIDRByCIDR = netCIDRByCIDR

	r.Log.V(1).Info("", "CIDRs", rc.CIDRs)
	r.Log.V(1).Info("", "CIDRsByLabel", rc.CIDRsByLabel)
	r.Log.V(1).Info("", "reservedIPsByCIDR", rc.ReservedIPsByCIDR)
	r.Log.V(1).Info("", "netCIDRByCIDR", rc.NetCIDRByCIDR)

	results := make(chan error)
	defer close(results)
	// nodes
	go func() {
		allNodes, err := r.getAllNodes(rc)
		if err != nil {
			r.Log.Error(err, "unable to get all nodes")
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
			r.Log.Error(err, "unable to get all hostsubnets")
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
			r.Log.Error(err, "unable to determine referring namespace for", "EgressIPAM", egressIPAM.GetName())
			results <- err
			return
		}

		rc.ReferringNamespaces = referringNamespaces
		rc.InitiallyAssignedNamespaces = assignedNamespaces
		rc.UnAssignedNamespaces = unAssignedNamespaces

		r.Log.V(1).Info("", "referringNamespaces", getNamespaceMapKeys(rc.ReferringNamespaces))
		r.Log.V(1).Info("", "initiallyAssignedNamespaces", getNamespaceNames(rc.InitiallyAssignedNamespaces))
		r.Log.V(1).Info("", "unAssignedNamespaces", getNamespaceNames(rc.UnAssignedNamespaces))
		results <- nil
		return
	}()

	//netnamespace
	go func() {
		netNamespaces, err := r.getAllNetNamespaces(rc)
		if err != nil {
			r.Log.Error(err, "unable to load netnamespaces")
			results <- err
			return
		}

		rc.NetNamespaces = netNamespaces

		r.Log.V(1).Info("", "netNamespaces", getNetNamespaceMapKeys(rc.NetNamespaces))
		results <- nil
		return
	}()

	//collect results
	result := &multierror.Error{}
	for range []string{"nodes", "hostsubnets", "namespaces", "netnamespaces"} {
		err := <-results
		multierror.Append(result, err)
	}

	if result.ErrorOrNil() != nil {
		r.Log.Error(result, "unable ro run parallel initialization")
		return &reconcilecontext.ReconcileContext{}, result
	}

	selectedNodes, err := r.getSelectedNodes(rc)
	if err != nil {
		r.Log.Error(err, "unable to get selected nodes for", "EgressIPAM", egressIPAM.GetName())
		return &reconcilecontext.ReconcileContext{}, err
	}
	rc.SelectedNodes = selectedNodes
	r.Log.V(1).Info("", "selectedNodes", getNodeNames(rc.SelectedNodes))

	selectedHostSubnets := map[string]ocpnetv1.HostSubnet{}
	for hostsubnetname, hostsubnet := range rc.AllHostSubnets {
		if _, ok := rc.SelectedNodes[hostsubnetname]; ok {
			selectedHostSubnets[hostsubnetname] = hostsubnet
		}
	}

	rc.SelectedHostSubnets = selectedHostSubnets
	r.Log.V(1).Info("", "selectedHostSubnets", getHostSubnetNames(rc.SelectedHostSubnets))

	selectedNodesByCIDR := map[string][]string{}
	selectedHostSubnetsByCIDR := map[string][]string{}
	for nodename, node := range rc.SelectedNodes {
		if value, ok := node.GetLabels()[egressIPAM.Spec.TopologyLabel]; ok {
			if cidr, ok := CIDRsByLabel[value]; ok {
				selectedNodesByCIDR[cidr] = append(selectedNodesByCIDR[cidr], nodename)
				selectedHostSubnetsByCIDR[cidr] = append(selectedHostSubnetsByCIDR[cidr], nodename)
			}
		}
	}
	rc.SelectedNodesByCIDR = selectedNodesByCIDR
	rc.SelectedHostSubnetsByCIDR = selectedHostSubnetsByCIDR

	r.Log.V(1).Info("", "selectedNodesByCIDR", rc.SelectedNodesByCIDR)
	r.Log.V(1).Info("", "selectedHostSubnetByCIDR", rc.SelectedHostSubnetsByCIDR)

	switch rc.Infrastructure.Status.Platform {
	case ocpconfigv1.AWSPlatformType:
		{
			dc, err := r.GetDirectClientWithSchemeBuilders(machinev1beta1.AddToScheme)
			if err != nil {
				r.Log.Error(err, "unable to get direct client")
				return &reconcilecontext.ReconcileContext{}, err
			}
			infra, err := aws.NewAWSInfra(dc, rc)
			if err != nil {
				r.Log.Error(err, "unable to instatiate aws infra")
				return &reconcilecontext.ReconcileContext{}, err
			}
			rc.Infra = infra
		}
	case ocpconfigv1.AzurePlatformType:
		{
			dc, err := r.GetDirectClientWithSchemeBuilders(machinev1beta1.AddToScheme)
			if err != nil {
				r.Log.Error(err, "unable to get direct client")
				return &reconcilecontext.ReconcileContext{}, err
			}
			infra, err := azure.NewAzureInfra(dc, rc)
			if err != nil {
				r.Log.Error(err, "unable to instatiate azure infra")
				return &reconcilecontext.ReconcileContext{}, err
			}
			rc.Infra = infra
		}
	default:
		{
			infra := baremetal.NewBareMetalInfra()
			rc.Infra = infra
		}
	}

	results = make(chan error)
	defer close(results)
	// used IPs
	go func() {
		usedIPsByCIDR, err := rc.Infra.GetUsedIPsByCIDR(rc)
		if err != nil {
			r.Log.Error(err, "unable to get used IPs by CIDR")
			results <- err
			return
		}
		rc.UsedIPsByCIDR = usedIPsByCIDR

		r.Log.V(1).Info("", "Used IPs By CIDR", rc.UsedIPsByCIDR)
		results <- nil
		return
	}()
	// collect results
	result = &multierror.Error{}
	for range []string{"usedIPS"} {
		err := <-results
		multierror.Append(result, err)
	}
	if result.ErrorOrNil() != nil {
		r.Log.Error(result, "unable ro run parallel aws initialization")
		return &reconcilecontext.ReconcileContext{}, result
	}

	return rc, nil
}

func (r *EgressIPAMReconciler) createOrUpdateResourceWithClient(c client.Client, owner client.Object, namespace string, obj client.Object) error {
	if owner != nil {
		_ = controllerutil.SetControllerReference(owner, obj, r.GetScheme())
	}
	if namespace != "" {
		obj.SetNamespace(namespace)
	}

	obj2 := &unstructured.Unstructured{}
	obj2.SetGroupVersionKind(obj.GetObjectKind().GroupVersionKind())

	err := c.Get(context.TODO(), types.NamespacedName{
		Namespace: obj.GetNamespace(),
		Name:      obj.GetName(),
	}, obj2)

	if apierrors.IsNotFound(err) {
		err = c.Create(context.TODO(), obj)
		if err != nil {
			r.Log.Error(err, "unable to create object", "object", obj)
			return err
		}
		return nil
	}

	if err == nil {
		obj.SetResourceVersion(obj2.GetResourceVersion())
		err = c.Update(context.TODO(), obj)
		if err != nil {
			r.Log.Error(err, "unable to update object", "object", obj)
			return err
		}
		return nil

	}
	r.Log.Error(err, "unable to lookup object", "object", obj)
	return err
}

// SetupWithManager sets up the controller with the Manager.
func (r *EgressIPAMReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.controllerName = "EgressIPAM_controller"

	infrastructure := &ocpconfigv1.Infrastructure{}
	client, err := r.GetDirectClientWithSchemeBuilders(ocpconfigv1.Install)
	if err != nil {
		r.Log.Error(err, "unable to create client from ", "config", r.GetRestConfig())
		return err
	}
	err = client.Get(context.TODO(), types.NamespacedName{
		Name: "cluster",
	}, infrastructure)
	if err != nil {
		r.Log.Error(err, "unable to retrieve cluster's infrastrcuture resource ")
		return err
	}

	r.infrastructure = infrastructure

	err = r.createCredentialRequest()
	if err != nil {
		r.Log.Error(err, "unable to create credential request")
		return err
	}

	IsCreatedOrDeletedOrReadinessChanged := predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			old := e.ObjectOld.GetAnnotations()
			new := e.ObjectNew.GetAnnotations()
			oldNode, ok := e.ObjectOld.(*corev1.Node)
			if !ok {
				return false
			}
			newNode, ok := e.ObjectNew.(*corev1.Node)
			if !ok {
				return false
			}
			oldReadiness := isCondition(oldNode.Status.Conditions, corev1.NodeReady, corev1.ConditionTrue)
			newReadiness := isCondition(newNode.Status.Conditions, corev1.NodeReady, corev1.ConditionTrue)
			return !reflect.DeepEqual(old, new) || oldReadiness != newReadiness
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

	log1 := r.Log.WithName("IsCreatedORDeletedOrIsEgressCIDRsChanged")

	IsCreatedORDeletedOrIsEgressCIDRsChanged := predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldHostSubnet, ok := e.ObjectOld.(*ocpnetv1.HostSubnet)
			if !ok {
				log1.Info("unable to convert event object to hostsubnet,", "event", e)
				return false
			}
			newHostSubnet, ok := e.ObjectNew.(*ocpnetv1.HostSubnet)
			if !ok {
				log1.Info("unable to convert event object to hostsubnet,", "event", e)
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

	IsAnnotated := predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			_, okold := e.ObjectOld.GetAnnotations()[NamespaceAnnotation]
			_, oknew := e.ObjectNew.GetAnnotations()[NamespaceAnnotation]
			_, ipsold := e.ObjectOld.GetAnnotations()[NamespaceAssociationAnnotation]
			_, ipsnew := e.ObjectNew.GetAnnotations()[NamespaceAssociationAnnotation]
			return (!okold && oknew) || (ipsnew != ipsold)
		},
		CreateFunc: func(e event.CreateEvent) bool {
			_, ok := e.Object.GetAnnotations()[NamespaceAnnotation]
			return ok
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			_, ok := e.Object.GetAnnotations()[NamespaceAnnotation]
			return ok
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return false
		},
	}

	log2 := r.Log.WithName("IsCreatedOrIsEgressIPsChanged")

	IsCreatedOrIsEgressIPsChanged := predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldNetNamespace, ok := e.ObjectOld.(*ocpnetv1.NetNamespace)
			if !ok {
				log2.Info("unable to convert event object to NetNamespace,", "event", e)
				return false
			}
			newNetNamespace, ok := e.ObjectNew.(*ocpnetv1.NetNamespace)
			if !ok {
				log2.Info("unable to convert event object to NetNamespace,", "event", e)
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

	return ctrl.NewControllerManagedBy(mgr).
		For(&redhatcopv1alpha1.EgressIPAM{}, builder.WithPredicates(util.ResourceGenerationOrFinalizerChangedPredicate{})).
		Watches(&source.Kind{Type: &corev1.Node{
			TypeMeta: metav1.TypeMeta{
				Kind: "Node",
			},
		}}, &enqueForSelectingEgressIPAMNode{
			r:   r,
			log: r.Log.WithName("enqueForSelectingEgressIPAMNode"),
		}, builder.WithPredicates(&IsCreatedOrDeletedOrReadinessChanged)).
		Watches(&source.Kind{Type: &ocpnetv1.HostSubnet{
			TypeMeta: metav1.TypeMeta{
				Kind: "HostSubnet",
			},
		}}, &enqueForSelectingEgressIPAMHostSubnet{
			r:   r,
			log: r.Log.WithName("enqueForSelectingEgressIPAMHostSubnet"),
		}, builder.WithPredicates(&IsCreatedORDeletedOrIsEgressCIDRsChanged)).
		Watches(&source.Kind{Type: &corev1.Namespace{
			TypeMeta: metav1.TypeMeta{
				Kind: "Namespace",
			},
		}}, &enqueForSelectedEgressIPAMNamespace{
			r: r,
		}, builder.WithPredicates(&IsAnnotated)).
		Watches(&source.Kind{Type: &ocpnetv1.NetNamespace{
			TypeMeta: metav1.TypeMeta{
				Kind: "NetNamespace",
			},
		}}, &enqueForSelectedEgressIPAMNetNamespace{
			r:   r,
			log: r.Log.WithName("enqueForSelectedEgressIPAMNetNamespace"),
		}, builder.WithPredicates(&IsCreatedOrIsEgressIPsChanged)).
		Complete(r)
}

func (r *EgressIPAMReconciler) createCredentialRequest() error {

	infrastructure := r.GetInfrastructure()

	var providerSpec runtime.Object

	switch infrastructure.Status.Platform {
	case ocpconfigv1.AWSPlatformType:
		{
			providerSpec = aws.GetAWSCredentialsRequestProviderSpec()
		}
	case ocpconfigv1.AzurePlatformType:
		{
			providerSpec = azure.GetAzureCredentialsRequestProviderSpec()
		}
	default:
		return nil
	}

	namespace, err := r.GetOperatorNamespace()
	if err != nil {
		r.Log.Error(err, "unable to get operator's namespace")
		return err
	}
	request := cloudcredentialv1.CredentialsRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "egress-ipam-operator",
			Namespace: "openshift-cloud-credential-operator",
		},
		TypeMeta: metav1.TypeMeta{
			APIVersion: "cloudcredential.openshift.io/v1",
			Kind:       "CredentialsRequest",
		},
		Spec: cloudcredentialv1.CredentialsRequestSpec{
			SecretRef: corev1.ObjectReference{
				Name:      credentialsSecretName,
				Namespace: namespace,
			},
			ProviderSpec: &runtime.RawExtension{
				Object: providerSpec,
			},
		},
	}
	c, err := r.GetDirectClientWithSchemeBuilders(cloudcredentialv1.AddToScheme)
	if err != nil {
		r.Log.Error(err, "unable to create direct client")
		return err
	}
	err = r.createOrUpdateResourceWithClient(c, nil, "", &request)
	if err != nil {
		r.Log.Error(err, "unable to create or update ", "credential request", request)
		return err
	}

	return nil
}
