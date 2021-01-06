package egressipam

import (
	"context"
	"errors"
	"reflect"
	"strings"

	"github.com/go-logr/logr"
	multierror "github.com/hashicorp/go-multierror"
	ocpnetv1 "github.com/openshift/api/network/v1"
	"github.com/redhat-cop/egressip-ipam-operator/controllers/egressipam/reconcilecontext"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type enqueForSelectedEgressIPAMNetNamespace struct {
	r   *EgressIPAMReconciler
	log logr.Logger
}

// trigger a egressIPAM reconcile event for those egressIPAM objcts that reference this node
func (e *enqueForSelectedEgressIPAMNetNamespace) Create(evt event.CreateEvent, q workqueue.RateLimitingInterface) {
	netnamespace, ok := evt.Object.(*ocpnetv1.NetNamespace)
	if !ok {
		e.log.Info("unable to convert event object to networknamespace,", "event", evt)
		return
	}
	namespace, err := e.r.getNamespace(netnamespace)
	if err != nil {
		e.log.Error(err, "Unable to get Namespace from ", "NetNamespace", netnamespace)
	}
	egressIPAMNAme, ok := namespace.GetAnnotations()[NamespaceAnnotation]
	if ok {
		q.Add(reconcile.Request{NamespacedName: types.NamespacedName{
			Name: egressIPAMNAme,
		}})
	}
}

// Update implements EventHandler
// trigger a router reconcile event for those routes that reference this secret
func (e *enqueForSelectedEgressIPAMNetNamespace) Update(evt event.UpdateEvent, q workqueue.RateLimitingInterface) {
	netnamespace, ok := evt.ObjectOld.(*ocpnetv1.NetNamespace)
	if !ok {
		e.log.Info("unable to convert event object to networknamespace,", "event", evt)
		return
	}
	namespace, err := e.r.getNamespace(netnamespace)
	if err != nil {
		e.log.Error(err, "Unable to get Namespace from ", "NetNamespace", netnamespace)
	}
	egressIPAMName, ok := namespace.GetAnnotations()[NamespaceAnnotation]
	if ok {
		q.Add(reconcile.Request{NamespacedName: types.NamespacedName{
			Name: egressIPAMName,
		}})
	}
	netnamespace, ok = evt.ObjectNew.(*ocpnetv1.NetNamespace)
	if !ok {
		e.log.Info("unable to convert event object to networknamespace,", "event", evt)
		return
	}
	namespace, err = e.r.getNamespace(netnamespace)
	if err != nil {
		e.log.Error(err, "Unable to get Namespace from ", "NetNamespace", netnamespace)
	}
	egressIPAMName, ok = namespace.GetAnnotations()[NamespaceAnnotation]
	if ok {
		q.Add(reconcile.Request{NamespacedName: types.NamespacedName{
			Name: egressIPAMName,
		}})
	}

}

// Delete implements EventHandler
func (e *enqueForSelectedEgressIPAMNetNamespace) Delete(evt event.DeleteEvent, q workqueue.RateLimitingInterface) {
	return
}

// Generic implements EventHandler
func (e *enqueForSelectedEgressIPAMNetNamespace) Generic(evt event.GenericEvent, q workqueue.RateLimitingInterface) {
	return
}

func (r *EgressIPAMReconciler) getNamespace(netnamespace *ocpnetv1.NetNamespace) (corev1.Namespace, error) {
	namespace := &corev1.Namespace{}
	err := r.GetClient().Get(context.TODO(), types.NamespacedName{
		Name: netnamespace.GetName(),
	}, namespace)
	if err != nil {
		r.Log.Error(err, "unable to get namespace from", "netnamespace", netnamespace)
		return corev1.Namespace{}, err
	}
	return *namespace, nil
}

func (r *EgressIPAMReconciler) reconcileNetNamespaces(rc *reconcilecontext.ReconcileContext) error {
	results := make(chan error)
	defer close(results)
	for _, namespace := range rc.FinallyAssignedNamespaces {
		namespacec := namespace.DeepCopy()
		go func() {
			ipstring, ok := namespacec.Annotations[NamespaceAssociationAnnotation]
			if !ok {
				results <- errors.New("namespace " + namespacec.GetName() + " doesn't have required annotation: " + NamespaceAssociationAnnotation)
				return
			}
			IPs := strings.Split(ipstring, ",")
			netnamespace := rc.NetNamespaces[namespacec.GetName()]
			if !reflect.DeepEqual(netnamespace.EgressIPs, IPs) {
				netnamespace.EgressIPs = GetNetNamespaceEgressIPs(IPs)
				err := r.GetClient().Update(rc.Context, &netnamespace, &client.UpdateOptions{})
				if err != nil {
					r.Log.Error(err, "unable to update ", "netnamespace", netnamespace.GetName())
					results <- err
					return
				}
			}
			results <- nil
			return
		}()
	}
	result := &multierror.Error{}
	for range rc.FinallyAssignedNamespaces {
		multierror.Append(result, <-results)
	}
	return result.ErrorOrNil()
}

func (r *EgressIPAMReconciler) removeNetnamespaceAssignedIPs(rc *reconcilecontext.ReconcileContext) error {
	results := make(chan error)
	defer close(results)
	for _, namespace := range rc.ReferringNamespaces {
		namespacec := namespace.DeepCopy()
		go func() {
			netnamespace := rc.NetNamespaces[namespacec.GetName()]
			if !reflect.DeepEqual(netnamespace.EgressIPs, []string{}) {
				netnamespace.EgressIPs = []ocpnetv1.NetNamespaceEgressIP{}
				err := r.GetClient().Update(rc.Context, &netnamespace, &client.UpdateOptions{})
				if err != nil {
					r.Log.Error(err, "unable to update ", "netnamespace", netnamespace.GetName())
					results <- err
					return
				}
			}
			results <- nil
			return
		}()
	}
	result := &multierror.Error{}
	for range rc.FinallyAssignedNamespaces {
		multierror.Append(result, <-results)
	}
	return result.ErrorOrNil()
}

func (r *EgressIPAMReconciler) getAllNetNamespaces(rc *reconcilecontext.ReconcileContext) (map[string]ocpnetv1.NetNamespace, error) {
	netnamespaceList := &ocpnetv1.NetNamespaceList{}
	err := r.GetClient().List(rc.Context, netnamespaceList, &client.ListOptions{})
	if err != nil {
		r.Log.Error(err, "unable to list all netnamespaces")
		return map[string]ocpnetv1.NetNamespace{}, err
	}
	netnamespaces := map[string]ocpnetv1.NetNamespace{}
	for _, netnamespace := range netnamespaceList.Items {
		netnamespaces[netnamespace.GetName()] = netnamespace
	}
	return netnamespaces, nil
}

func getNetNamespaceMapKeys(netNamespaces map[string]ocpnetv1.NetNamespace) []string {
	netNamespaceNames := []string{}
	for netNamespace := range netNamespaces {
		netNamespaceNames = append(netNamespaceNames, netNamespace)
	}
	return netNamespaceNames
}

func GetNetNamespaceEgressIPsAsStrings(IPs []ocpnetv1.NetNamespaceEgressIP) []string {
	sIPs := []string{}
	for _, ip := range IPs {
		sIPs = append(sIPs, string(ip))
	}
	return sIPs
}

func GetNetNamespaceEgressIPs(IPs []string) []ocpnetv1.NetNamespaceEgressIP {
	hIPs := []ocpnetv1.NetNamespaceEgressIP{}
	for _, ip := range IPs {
		hIPs = append(hIPs, ocpnetv1.NetNamespaceEgressIP(ip))
	}
	return hIPs
}
