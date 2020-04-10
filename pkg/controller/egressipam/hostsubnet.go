package egressipam

import (
	"context"
	"reflect"

	ocpnetv1 "github.com/openshift/api/network/v1"
	redhatcopv1alpha1 "github.com/redhat-cop/egressip-ipam-operator/pkg/apis/redhatcop/v1alpha1"
	"github.com/scylladb/go-set/strset"
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
func (r *ReconcileEgressIPAM) reconcileHSAssignedIPs(nodeAssignedIPs map[string][]string, egressIPAM *redhatcopv1alpha1.EgressIPAM) error {
	nodes, err := r.getSelectedNodes(egressIPAM)
	if err != nil {
		log.Error(err, "unable to get all selected nodes for ", "egressIPAM", egressIPAM)
		return err
	}
	for _, node := range nodes {
		hostsubnet, err := r.getHostSubnet(node.GetName())
		if err != nil {
			log.Error(err, "unable to lookup hostsubnet for ", "node", node.GetName)
			return err
		}
		if !strset.New(nodeAssignedIPs[node.GetName()]...).IsEqual(strset.New(hostsubnet.EgressIPs...)) {
			hostsubnet.EgressIPs = nodeAssignedIPs[node.GetName()]
			err := r.GetClient().Update(context.TODO(), &hostsubnet, &client.UpdateOptions{})
			if err != nil {
				log.Error(err, "unable to update", "hostsubnet ", hostsubnet, "with ips", nodeAssignedIPs[node.GetName()])
				return err
			}
		}
	}
	return nil
}

// ensures that hostsubnets have the correct CIDR
func (r *ReconcileEgressIPAM) assignCIDRsToHostSubnets(nodesByCIDR map[string][]corev1.Node, egressIPAM *redhatcopv1alpha1.EgressIPAM) error {
	selectedNodesByCIDR, err := r.getSelectedNodesByCIDR(egressIPAM)
	if err != nil {
		log.Error(err, "unable to get all selected nodes for ", "egressIPAM", egressIPAM)
		return err
	}
	cidrByNode := map[string]string{}
	for cidr, nodes := range nodesByCIDR {
		for _, node := range nodes {
			cidrByNode[node.GetName()] = cidr
		}
	}
	for cidr, nodes := range selectedNodesByCIDR {
		cidrs := []string{cidr}
		for _, node := range nodes {
			hostsubnet, err := r.getHostSubnet(node.GetName())
			if err != nil {
				log.Error(err, "unable to lookup hostsubnet for ", "node", node)
				return err
			}
			if !strset.New(hostsubnet.EgressCIDRs...).IsEqual(strset.New([]string{cidrByNode[node.GetName()]}...)) {
				hostsubnet.EgressCIDRs = []string{cidrByNode[node.GetName()]}
				err := r.GetClient().Update(context.TODO(), &hostsubnet, &client.UpdateOptions{})
				if err != nil {
					log.Error(err, "unable to update", "hostsubnet ", hostsubnet, "with cidrs", cidrs)
					return err
				}
			}
		}
	}
	return nil
}

func (r *ReconcileEgressIPAM) getHostSubnet(node string) (ocpnetv1.HostSubnet, error) {
	hostsubnet := &ocpnetv1.HostSubnet{}
	err := r.GetClient().Get(context.TODO(), types.NamespacedName{
		Name: node,
	}, hostsubnet)
	if err != nil {
		log.Error(err, "unable to get hostsubnet for ", "node", node)
		return ocpnetv1.HostSubnet{}, nil
	}
	return *hostsubnet, nil
}

func (r *ReconcileEgressIPAM) removeHostsubnetAssignedIPsCIDRsAssigned(egressIPAM *redhatcopv1alpha1.EgressIPAM) error {
	nodes, err := r.getSelectedNodes(egressIPAM)
	if err != nil {
		log.Error(err, "unable to get selected nodes for ", "egressIPAM", egressIPAM)
		return err
	}
	for _, node := range nodes {
		hostsubnet, err := r.getHostSubnet(node.GetName())
		if err != nil {
			log.Error(err, "unable to get hostsubnet for ", "node", node.GetName())
			return err
		}
		if !reflect.DeepEqual(hostsubnet.EgressCIDRs, []string{}) || !reflect.DeepEqual(hostsubnet.EgressIPs, []string{}) {
			hostsubnet.EgressCIDRs = []string{}
			hostsubnet.EgressIPs = []string{}
			err := r.GetClient().Update(context.TODO(), &hostsubnet, &client.UpdateOptions{})
			if err != nil {
				log.Error(err, "unable to upadate ", "hostsubnet", hostsubnet)
				return err
			}
		}
	}
	return nil
}
