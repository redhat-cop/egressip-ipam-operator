package egressipam

import (
	"context"
	"errors"
	"reflect"
	"strings"

	multierror "github.com/hashicorp/go-multierror"
	ocpnetv1 "github.com/openshift/api/network/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type enqueForSelectedEgressIPAMNetNamespace struct {
	r *ReconcileEgressIPAM
}

// trigger a egressIPAM reconcile event for those egressIPAM objcts that reference this node
func (e *enqueForSelectedEgressIPAMNetNamespace) Create(evt event.CreateEvent, q workqueue.RateLimitingInterface) {
	netnamespace, ok := evt.Object.(*ocpnetv1.NetNamespace)
	if !ok {
		log.Info("unable to convert event object to networknamespace,", "event", evt)
		return
	}
	namespace, err := e.r.getNamespace(netnamespace)
	if err != nil {
		log.Error(err, "Unable to get Namespace from ", "NetNamespace", netnamespace)
	}
	egressIPAMNAme, ok := namespace.GetAnnotations()[namespaceAnnotation]
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
		log.Info("unable to convert event object to networknamespace,", "event", evt)
		return
	}
	namespace, err := e.r.getNamespace(netnamespace)
	if err != nil {
		log.Error(err, "Unable to get Namespace from ", "NetNamespace", netnamespace)
	}
	egressIPAMName, ok := namespace.GetAnnotations()[namespaceAnnotation]
	if ok {
		q.Add(reconcile.Request{NamespacedName: types.NamespacedName{
			Name: egressIPAMName,
		}})
	}
	netnamespace, ok = evt.ObjectNew.(*ocpnetv1.NetNamespace)
	if !ok {
		log.Info("unable to convert event object to networknamespace,", "event", evt)
		return
	}
	namespace, err = e.r.getNamespace(netnamespace)
	if err != nil {
		log.Error(err, "Unable to get Namespace from ", "NetNamespace", netnamespace)
	}
	egressIPAMName, ok = namespace.GetAnnotations()[namespaceAnnotation]
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

func (r *ReconcileEgressIPAM) getNamespace(netnamespace *ocpnetv1.NetNamespace) (corev1.Namespace, error) {
	namespace := &corev1.Namespace{}
	err := r.GetClient().Get(context.TODO(), types.NamespacedName{
		Name: netnamespace.GetName(),
	}, namespace)
	if err != nil {
		log.Error(err, "unable to get namespace from", "netnamespace", netnamespace)
		return corev1.Namespace{}, err
	}
	return *namespace, nil
}

func (r *ReconcileEgressIPAM) reconcileNetNamespaces(rc *reconcileContext) error {
	results := make(chan error)
	defer close(results)
	for _, namespace := range rc.finallyAssignedNamespaces {
		namespacec := namespace.DeepCopy()
		go func() {
			ipstring, ok := namespacec.Annotations[namespaceAssociationAnnotation]
			if !ok {
				results <- errors.New("namespace " + namespacec.GetName() + " doesn't have required annotation: " + namespaceAssociationAnnotation)
				return
			}
			IPs := strings.Split(ipstring, ",")
			netnamespace := rc.netNamespaces[namespacec.GetName()]
			if !reflect.DeepEqual(netnamespace.EgressIPs, IPs) {
				netnamespace.EgressIPs = IPs
				err := r.GetClient().Update(context.TODO(), &netnamespace, &client.UpdateOptions{})
				if err != nil {
					log.Error(err, "unable to update ", "netnamespace", netnamespace.GetName())
					results <- err
					return
				}
			}
			results <- nil
			return
		}()
	}
	result := &multierror.Error{}
	for range rc.finallyAssignedNamespaces {
		multierror.Append(result, <-results)
	}
	return result.ErrorOrNil()
}

func (r *ReconcileEgressIPAM) cleanUpNamespaceAndNetNamespace(namespaceName string) error {
	netNamespace := &ocpnetv1.NetNamespace{}
	err := r.GetClient().Get(context.TODO(), types.NamespacedName{Name: namespaceName}, netNamespace)
	if err != nil {
		log.Error(err, "unable to retrieve", "netnamespace", namespaceName)
		return err
	}
	if !reflect.DeepEqual(netNamespace.EgressIPs, []string{}) {
		netNamespace.EgressIPs = []string{}
		err := r.GetClient().Update(context.TODO(), netNamespace, &client.UpdateOptions{})
		if err != nil {
			log.Error(err, "unable to update ", "netnamespace", netNamespace.GetName())
			return err
		}
	}
	namespace := &corev1.Namespace{}
	err = r.GetClient().Get(context.TODO(), types.NamespacedName{Name: namespaceName}, namespace)
	if err != nil {
		log.Error(err, "unable to retrieve", "namespace", namespaceName)
		return err
	}
	if _, ok := namespace.GetAnnotations()[namespaceAssociationAnnotation]; ok {
		delete(namespace.Annotations, namespaceAssociationAnnotation)
		err := r.GetClient().Update(context.TODO(), namespace, &client.UpdateOptions{})
		if err != nil {
			log.Error(err, "unable to update ", "namespace", namespace.GetName())
			return err
		}
	}
	return nil
}

func (r *ReconcileEgressIPAM) removeNetnamespaceAssignedIPs(rc *reconcileContext) error {
	results := make(chan error)
	defer close(results)
	for _, namespace := range rc.referringNamespaces {
		namespacec := namespace.DeepCopy()
		go func() {
			netnamespace := rc.netNamespaces[namespacec.GetName()]
			if !reflect.DeepEqual(netnamespace.EgressIPs, []string{}) {
				netnamespace.EgressIPs = []string{}
				err := r.GetClient().Update(context.TODO(), &netnamespace, &client.UpdateOptions{})
				if err != nil {
					log.Error(err, "unable to update ", "netnamespace", netnamespace.GetName())
					results <- err
					return
				}
			}
			results <- nil
			return
		}()
	}
	result := &multierror.Error{}
	for range rc.finallyAssignedNamespaces {
		multierror.Append(result, <-results)
	}
	return result.ErrorOrNil()
}

func (r *ReconcileEgressIPAM) getAllNetNamespaces(rc *reconcileContext) (map[string]ocpnetv1.NetNamespace, error) {
	netnamespaceList := &ocpnetv1.NetNamespaceList{}
	err := r.GetClient().List(context.TODO(), netnamespaceList, &client.ListOptions{})
	if err != nil {
		log.Error(err, "unable to list all netnamespaces")
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
