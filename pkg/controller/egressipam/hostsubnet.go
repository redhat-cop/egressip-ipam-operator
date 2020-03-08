package egressipam

import (
	"context"
	"errors"
	"net"
	"reflect"

	ocpnetv1 "github.com/openshift/api/network/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
		if matches, _ := matchesNode(&egressIPAM, node); matches {
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
		if matches, _ := matchesNode(&egressIPAM, node); matches {
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

// ensures that hostsubntes have the correct egressIPs
func (r *ReconcileEgressIPAM) reconcileHSAssignedIPs(nodeAssignedIPs map[*corev1.Node][]net.IP) error {
	return errors.New("not implemented")
}

// ensures that hostsubnets have the correct CIDR
func (r *ReconcileEgressIPAM) assignCIDRsToHostSubnets(nodeByCIDR map[*net.IPNet][]corev1.Node) error {
	for cidr, nodes := range nodeByCIDR {
		cidrs := []string{cidr.String()}
		for _, node := range nodes {
			hostsubnet, err := r.getHostSubnet(&node)
			if err != nil {
				log.Error(err, "unable to lookup hostsubnet for ", "node", node)
				return err
			}
			if !reflect.DeepEqual(hostsubnet.EgressCIDRs, cidrs) {
				hostsubnet.EgressCIDRs = cidrs
				err := r.GetClient().Update(context.TODO(), hostsubnet, &client.UpdateOptions{})
				if err != nil {
					log.Error(err, "unable to update", "hostsubnet ", hostsubnet, "with cidrs", cidrs)
					return err
				}
			}
		}
	}
	return nil
}

func (r *ReconcileEgressIPAM) getHostSubnet(node *corev1.Node) (ocpnetv1.HostSubnet, error) {
	hostsubnet := &ocpnetv1.HostSubnet{}
	err := r.GetClient().Get(context.TODO(), types.NamespacedName{
		Name: node.GetName(),
	}, hostsubnet)
	if err != nil {
		log.Error(err, "unable to get hostsubnet for ", "node", node)
		return ocpnetv1.HostSubnet{}, nil
	}
	return *hostsubnet, nil
}
