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

type enqueueForSelectingEgressIPAMNode struct {
	r *ReconcileEgressIPAM
}

// return whether this EgressIPAM matches this node and with which CIDR
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

// trigger a egressIPAM reconcile event for those egressIPAM objects that reference this node
func (e *enqueueForSelectingEgressIPAMNode) Create(evt event.CreateEvent, q workqueue.RateLimitingInterface) {
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
func (e *enqueueForSelectingEgressIPAMNode) Update(evt event.UpdateEvent, q workqueue.RateLimitingInterface) {
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
func (e *enqueueForSelectingEgressIPAMNode) Delete(_ event.DeleteEvent, _ workqueue.RateLimitingInterface) {
	return
}

// Generic implements EventHandler
func (e *enqueueForSelectingEgressIPAMNode) Generic(_ event.GenericEvent, _ workqueue.RateLimitingInterface) {
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

func (r *ReconcileEgressIPAM) getSelectedNodes(rc *ReconcileContext) (map[string]corev1.Node, error) {
	selector, err := metav1.LabelSelectorAsSelector(&rc.EgressIPAM.Spec.NodeSelector)
	if err != nil {
		log.Error(err, "unable to create selector from label selector", "selector", rc.EgressIPAM.Spec.NodeSelector)
		return map[string]corev1.Node{}, err
	}
	selectedNodes := map[string]corev1.Node{}
	for nodeName, node := range rc.AllNodes {
		if selector.Matches(labels.Set(node.GetLabels())) {
			selectedNodes[nodeName] = node
		}
	}
	return selectedNodes, nil
}

func (r *ReconcileEgressIPAM) GetAssignedIPsByNode(rc *ReconcileContext) map[string][]string {
	assignedIPsByNode := map[string][]string{}
	for hostSubnetName, hostsubnet := range rc.SelectedHostSubnets {
		assignedIPsByNode[hostSubnetName] = GetHostHostSubnetEgressIPsAsStrings(hostsubnet.EgressIPs)
	}
	return assignedIPsByNode
}

func (r *ReconcileEgressIPAM) getNodesIPsByCIDR(rc *ReconcileContext) (map[string][]net.IP, error) {
	nodesIPsByCIDR := map[string][]net.IP{}
	for nodeName := range rc.AllNodes {
		hostsubnet, ok := rc.AllHostSubnets[nodeName]
		if !ok {
			return map[string][]net.IP{}, errors.New("unable to find hostsubnet for node:" + nodeName)
		}
		for _, cidr := range rc.CIDRs {
			ip := net.ParseIP(hostsubnet.HostIP)
			if rc.NetCIDRByCIDR[cidr].Contains(ip) {
				nodesIPsByCIDR[cidr] = append(nodesIPsByCIDR[cidr], ip)
			}
		}
	}
	return nodesIPsByCIDR, nil
}

func (r *ReconcileEgressIPAM) getAllNodes(_ *ReconcileContext) (map[string]corev1.Node, error) {
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
	//goland:noinspection GoPreferNilSlice
	nodeNames := []string{}
	for _, node := range nodes {
		nodeNames = append(nodeNames, node.GetName())
	}
	return nodeNames
}
