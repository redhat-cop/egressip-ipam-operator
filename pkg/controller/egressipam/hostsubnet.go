package egressipam

import (
	ocpnetv1 "github.com/openshift/api/network/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type enqueForSelectingEgressIPAMHostSubnet struct {
	r *ReconcileEgressIPAM
}

// trigger a egressIPAM reconcile event for those egressIPAM objcts that reference this hostsubnet indireclty via the corresponding node.
func (e *enqueForSelectingEgressIPAMHostSubnet) Create(evt event.CreateEvent, q workqueue.RateLimitingInterface) {
	hostsubnet, ok := evt.Object.(*ocpnetv1.HostSubnet)
	if !ok {
		log.Info("unable convert event object to hostsubnet,", "event", evt)
		return
	}
	node, err := e.r.getNode(hostsubnet)
	if err != nil {
		log.Error(err, "unable to get all retrieve node corresponding to", "hostsubnet", hostsubnet)
		return
	}
	egressIPAMs, err := e.r.getAllEgressIPAM()
	if err != nil {
		log.Error(err, "unable to get all EgressIPAM resources")
		return
	}
	for _, egressIPAM := range egressIPAMs {
		if matches, _ := matchesNode(&egressIPAM, &node); matches {
			q.Add(reconcile.Request{NamespacedName: types.NamespacedName{
				Name: egressIPAM.GetName(),
			}})
		}
	}
}

// Update implements EventHandler
// trigger a router reconcile event for those routes that reference this secret
func (e *enqueForSelectingEgressIPAMHostSubnet) Update(evt event.UpdateEvent, q workqueue.RateLimitingInterface) {
	hostsubnet, ok := evt.ObjectNew.(*ocpnetv1.HostSubnet)
	if !ok {
		log.Info("unable convert event object to hostsubnet,", "event", evt)
		return
	}
	node, err := e.r.getNode(hostsubnet)
	if err != nil {
		log.Error(err, "unable to get all retrieve node corresponding to", "hostsubnet", hostsubnet)
		return
	}
	egressIPAMs, err := e.r.getAllEgressIPAM()
	if err != nil {
		log.Error(err, "unable to get all EgressIPAM resources")
		return
	}
	for _, egressIPAM := range egressIPAMs {
		if matches, _ := matchesNode(&egressIPAM, &node); matches {
			q.Add(reconcile.Request{NamespacedName: types.NamespacedName{
				Name: egressIPAM.GetName(),
			}})
		}
	}
}

// Delete implements EventHandler
func (e *enqueForSelectingEgressIPAMHostSubnet) Delete(evt event.DeleteEvent, q workqueue.RateLimitingInterface) {
	return
}

// Generic implements EventHandler
func (e *enqueForSelectingEgressIPAMHostSubnet) Generic(evt event.GenericEvent, q workqueue.RateLimitingInterface) {
	return
}
