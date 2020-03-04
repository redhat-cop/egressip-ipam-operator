package egressipam

import (
	"context"
	"errors"
	"reflect"
	"strings"

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

func (r *ReconcileEgressIPAM) reconcileNetNamespaces(assignedNamespaces []corev1.Namespace) error {
	for _, namespace := range assignedNamespaces {
		ipstring, ok := namespace.GetAnnotations()[namespaceAssociationAnnotation]
		if !ok {
			return errors.New("namespace " + namespace.GetName() + " doesn't have required annotation: " + namespaceAssociationAnnotation)
		}
		IPs := strings.Split(ipstring, ",")
		netnamespace := ocpnetv1.NetNamespace{}
		err := r.GetClient().Get(context.TODO(), types.NamespacedName{Name: namespace.GetName()}, &netnamespace)
		if err != nil {
			log.Error(err, "unable to retrieve the netnamespace for", "namespace", namespace)
			return err
		}
		if !reflect.DeepEqual(netnamespace.EgressIPs, IPs) {
			netnamespace.EgressIPs = IPs
			err := r.GetClient().Update(context.TODO(), &netnamespace, &client.UpdateOptions{})
			if err != nil {
				log.Error(err, "unable update ", "netnamespace", netnamespace)
				return err
			}
		}
	}
	return nil
}
