package egressipam

import (
	"context"

	ocpnetv1 "github.com/openshift/api/network/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
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
