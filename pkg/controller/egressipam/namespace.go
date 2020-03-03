package egressipam

import (
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type enqueForSelectedEgressIPAMNamespace struct {
	r *ReconcileEgressIPAM
}

// trigger a egressIPAM reconcile event for those egressIPAM objcts that reference this node
func (e *enqueForSelectedEgressIPAMNamespace) Create(evt event.CreateEvent, q workqueue.RateLimitingInterface) {
	egressIPAMNAme, ok := evt.Meta.GetAnnotations()[namespaceAnnotation]
	if ok {
		q.Add(reconcile.Request{NamespacedName: types.NamespacedName{
			Name: egressIPAMNAme,
		}})
	}
}

// Update implements EventHandler
// trigger a router reconcile event for those routes that reference this secret
func (e *enqueForSelectedEgressIPAMNamespace) Update(evt event.UpdateEvent, q workqueue.RateLimitingInterface) {
	egressIPAMNAme, ok := evt.MetaOld.GetAnnotations()[namespaceAnnotation]
	if ok {
		q.Add(reconcile.Request{NamespacedName: types.NamespacedName{
			Name: egressIPAMNAme,
		}})
	}

	egressIPAMNAme, ok = evt.MetaNew.GetAnnotations()[namespaceAnnotation]
	if ok {
		q.Add(reconcile.Request{NamespacedName: types.NamespacedName{
			Name: egressIPAMNAme,
		}})
	}

}

// Delete implements EventHandler
func (e *enqueForSelectedEgressIPAMNamespace) Delete(evt event.DeleteEvent, q workqueue.RateLimitingInterface) {
	return
}

// Generic implements EventHandler
func (e *enqueForSelectedEgressIPAMNamespace) Generic(evt event.GenericEvent, q workqueue.RateLimitingInterface) {
	return
}
