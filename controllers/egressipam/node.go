package egressipam

import (
	"errors"
	"net"

	"github.com/go-logr/logr"
	redhatcopv1alpha1 "github.com/redhat-cop/egressip-ipam-operator/api/v1alpha1"
	"github.com/redhat-cop/egressip-ipam-operator/controllers/egressipam/reconcilecontext"
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
	r   *EgressIPAMReconciler
	log logr.Logger
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
		e.log.Info("unable convert event object to node,", "event", evt)
		return
	}
	egressIPAMs, err := e.r.getAllEgressIPAM()
	if err != nil {
		e.log.Error(err, "unable to get all EgressIPAM resources")
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
		e.log.Info("unable convert event object to node,", "event", evt)
		return
	}
	egressIPAMs, err := e.r.getAllEgressIPAM()
	if err != nil {
		e.log.Error(err, "unable to get all EgressIPAM resources")
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
}

// Generic implements EventHandler
func (e *enqueForSelectingEgressIPAMNode) Generic(evt event.GenericEvent, q workqueue.RateLimitingInterface) {
}

func (r *EgressIPAMReconciler) getSelectedNodes(rc *reconcilecontext.ReconcileContext) (map[string]corev1.Node, error) {
	selector, err := metav1.LabelSelectorAsSelector(&rc.EgressIPAM.Spec.NodeSelector)
	if err != nil {
		r.Log.Error(err, "unable to create selector from label selector", "selector", rc.EgressIPAM.Spec.NodeSelector)
		return map[string]corev1.Node{}, err
	}
	selectedNodes := map[string]corev1.Node{}
	for nodename, node := range rc.AllNodes {
		if selector.Matches(labels.Set(node.GetLabels())) {
			selectedNodes[nodename] = node
		}
	}
	return selectedNodes, nil
}

func (r *EgressIPAMReconciler) getAssignedIPsByNode(rc *reconcilecontext.ReconcileContext) map[string][]string {
	assignedIPsByNode := map[string][]string{}
	for hostSubnetName, hostsubnet := range rc.SelectedHostSubnets {
		if node, ok := rc.AllNodes[hostSubnetName]; ok && isCondition(node.Status.Conditions, corev1.NodeReady, corev1.ConditionTrue) {
			assignedIPsByNode[hostSubnetName] = GetHostHostSubnetEgressIPsAsStrings(hostsubnet.EgressIPs)
		}

	}
	return assignedIPsByNode
}

func isCondition(conditions []corev1.NodeCondition, conditionType corev1.NodeConditionType, conditionStatus corev1.ConditionStatus) bool {
	for _, condition := range conditions {
		if conditionType == condition.Type && conditionStatus == condition.Status {
			return true
		}
	}
	return false
}

func (r *EgressIPAMReconciler) getNodesIPsByCIDR(rc *reconcilecontext.ReconcileContext) (map[string][]net.IP, error) {
	nodesIPsByCIDR := map[string][]net.IP{}
	for nodename := range rc.AllNodes {
		hostsubnet, ok := rc.AllHostSubnets[nodename]
		if !ok {
			return map[string][]net.IP{}, errors.New("unable to find hostsubnet for node:" + nodename)
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

func (r *EgressIPAMReconciler) getAllNodes(rc *reconcilecontext.ReconcileContext) (map[string]corev1.Node, error) {
	nodeList := &corev1.NodeList{}
	err := r.GetClient().List(rc.Context, nodeList, &client.ListOptions{})
	if err != nil {
		r.Log.Error(err, "unable to list all the nodes")
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
