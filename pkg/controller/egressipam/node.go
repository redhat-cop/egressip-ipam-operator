package egressipam

import (
	"context"
	"errors"
	"net"

	ocpnetv1 "github.com/openshift/api/network/v1"
	redhatcopv1alpha1 "github.com/redhat-cop/egressip-ipam-operator/pkg/apis/redhatcop/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type enqueForSelectingEgressIPAMNode struct {
	r *ReconcileEgressIPAM
}

// return whether this EgressIPAM macthes this node and with which CIDR
func matchesNode(egressIPAM *redhatcopv1alpha1.EgressIPAM, node corev1.Node) (bool, string) {
	value, ok := node.GetLabels()[egressIPAM.Spec.TopologyLabel]
	if !ok {
		return false, ""
	}
	for _, cIDRAssignment := range egressIPAM.Spec.CIDRAssignments {
		if value == cIDRAssignment.LabelValue {
			return true, cIDRAssignment.CIDR
		}
	}
	return false, ""
}

// trigger a egressIPAM reconcile event for those egressIPAM objcts that reference this node
func (e *enqueForSelectingEgressIPAMNode) Create(evt event.CreateEvent, q workqueue.RateLimitingInterface) {
	node, ok := evt.Object.(*corev1.Node)
	if !ok {
		log.Info("unable convert event object to node,", "event", evt)
		return
	}
	egressIPAMs, err := e.r.getAllEgressIPAM()
	if err != nil {
		log.Error(err, "unable to get all EgressIPAM resources")
		return
	}
	for _, egressIPAM := range egressIPAMs {
		if matches, _ := matchesNode(&egressIPAM, *node); matches {
			q.Add(reconcile.Request{NamespacedName: types.NamespacedName{
				Name: egressIPAM.GetName(),
			}})
		}
	}
}

// Update implements EventHandler
// trigger a router reconcile event for those routes that reference this secret
func (e *enqueForSelectingEgressIPAMNode) Update(evt event.UpdateEvent, q workqueue.RateLimitingInterface) {
	node, ok := evt.ObjectNew.(*corev1.Node)
	if !ok {
		log.Info("unable convert event object to node,", "event", evt)
		return
	}
	egressIPAMs, err := e.r.getAllEgressIPAM()
	if err != nil {
		log.Error(err, "unable to get all EgressIPAM resources")
		return
	}
	for _, egressIPAM := range egressIPAMs {
		if matches, _ := matchesNode(&egressIPAM, *node); matches {
			q.Add(reconcile.Request{NamespacedName: types.NamespacedName{
				Name: egressIPAM.GetName(),
			}})
		}
	}
}

// Delete implements EventHandler
func (e *enqueForSelectingEgressIPAMNode) Delete(evt event.DeleteEvent, q workqueue.RateLimitingInterface) {
	return
}

// Generic implements EventHandler
func (e *enqueForSelectingEgressIPAMNode) Generic(evt event.GenericEvent, q workqueue.RateLimitingInterface) {
	return
}

func (r *ReconcileEgressIPAM) getNode(hostsubnet *ocpnetv1.HostSubnet) (corev1.Node, error) {
	node := &corev1.Node{}
	err := r.GetClient().Get(context.TODO(), types.NamespacedName{
		Name: hostsubnet.Host,
	}, node)
	if err != nil {
		log.Error(err, "unable to get node from", "hostsubnet", hostsubnet)
		return corev1.Node{}, err
	}
	return *node, nil
}

func (r *ReconcileEgressIPAM) getSelectedNodes(rc *reconcileContext) (map[string]corev1.Node, error) {
	selector, err := metav1.LabelSelectorAsSelector(&rc.egressIPAM.Spec.NodeSelector)
	if err != nil {
		log.Error(err, "unable to create selector from label selector", "selector", rc.egressIPAM.Spec.NodeSelector)
		return map[string]corev1.Node{}, err
	}
	selectedNodes := map[string]corev1.Node{}
	for nodename, node := range rc.allNodes {
		if selector.Matches(labels.Set(node.GetLabels())) {
			selectedNodes[nodename] = node
		}
	}
	return selectedNodes, nil
}

func (r *ReconcileEgressIPAM) getAssignedIPsByNode(rc *reconcileContext) map[string][]string {
	assignedIPsByNode := map[string][]string{}
	for hostSubnetName, hostsubnet := range rc.selectedHostSubnets {
		assignedIPsByNode[hostSubnetName] = hostsubnet.EgressIPs
	}
	return assignedIPsByNode
}

func (r *ReconcileEgressIPAM) getNodesIPsByCIDR(rc *reconcileContext) (map[string][]net.IP, error) {
	nodesIPsByCIDR := map[string][]net.IP{}
	for nodename := range rc.allNodes {
		hostsubnet, ok := rc.allHostSubnets[nodename]
		if !ok {
			return map[string][]net.IP{}, errors.New("unable to find hostsubnet for node:" + nodename)
		}
		for _, cidr := range rc.cIDRs {
			ip := net.ParseIP(hostsubnet.HostIP)
			if rc.netCIDRByCIDR[cidr].Contains(ip) {
				nodesIPsByCIDR[cidr] = append(nodesIPsByCIDR[cidr], ip)
			}
		}
	}
	return nodesIPsByCIDR, nil
}

func (r *ReconcileEgressIPAM) getAllNodes(rc *reconcileContext) (map[string]corev1.Node, error) {
	nodeList := &corev1.NodeList{}
	err := r.GetClient().List(context.TODO(), nodeList, &client.ListOptions{})
	if err != nil {
		log.Error(err, "unable to list all the nodes")
		return map[string]corev1.Node{}, err
	}
	nodes := map[string]corev1.Node{}
	for _, node := range nodeList.Items {
		nodes[node.GetName()] = node
	}
	return nodes, nil
}

func getNodeNames(nodes map[string]corev1.Node) []string {
	nodeNames := []string{}
	for _, node := range nodes {
		nodeNames = append(nodeNames, node.GetName())
	}
	return nodeNames
}
