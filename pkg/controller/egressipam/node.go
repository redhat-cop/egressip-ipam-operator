package egressipam

import (
	"context"
	"net"

	ocpnetv1 "github.com/openshift/api/network/v1"
	redhatcopv1alpha1 "github.com/redhat-cop/egressip-ipam-operator/pkg/apis/redhatcop/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type enqueForSelectingEgressIPAMNode struct {
	r *ReconcileEgressIPAM
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

// returns nodes selected by this egressIPAM sorted by the CIDR
func (r *ReconcileEgressIPAM) getSelectedNodesByCIDR(egressIPAM *redhatcopv1alpha1.EgressIPAM) (map[string][]corev1.Node, error) {
	nodes, err := r.getSelectedNodes(egressIPAM)
	if err != nil {
		log.Error(err, "unabler to get selected nodes for ", "egressIPAM", egressIPAM)
		return map[string][]corev1.Node{}, err
	}
	selectedNdesByCIDR := map[string][]corev1.Node{}
	CIDRbyLabel := map[string]string{}
	for _, cidrAssignment := range egressIPAM.Spec.CIDRAssignments {
		_, _, err := net.ParseCIDR(cidrAssignment.CIDR)
		if err != nil {
			log.Error(err, "unable to parse", "cidr", cidrAssignment.CIDR)
			return map[string][]corev1.Node{}, err
		}

		CIDRbyLabel[cidrAssignment.LabelValue] = cidrAssignment.CIDR
		selectedNdesByCIDR[cidrAssignment.CIDR] = []corev1.Node{}
	}
	for _, node := range nodes {
		if value, ok := node.GetLabels()[egressIPAM.Spec.NodeLabel]; ok {
			if cidr, ok := CIDRbyLabel[value]; ok {
				selectedNdesByCIDR[cidr] = append(selectedNdesByCIDR[cidr], node)
			}
		}
	}
	return selectedNdesByCIDR, nil
}

func (r *ReconcileEgressIPAM) getSelectedNodes(egressIPAM *redhatcopv1alpha1.EgressIPAM) ([]corev1.Node, error) {
	nodeList := &corev1.NodeList{}
	selector, err := metav1.LabelSelectorAsSelector(&egressIPAM.Spec.NodeSelector)
	if err != nil {
		log.Error(err, "unable to create selector from label selector", "selector", &egressIPAM.Spec.NodeSelector)
		return []corev1.Node{}, err
	}
	err = r.GetClient().List(context.TODO(), nodeList, &client.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		log.Error(err, "unable to list sleected nodes", "selector", egressIPAM.Spec.NodeSelector)
		return []corev1.Node{}, err
	}
	return nodeList.Items, nil
}

func (r *ReconcileEgressIPAM) getAssignedIPsByNode(egressIPAM *redhatcopv1alpha1.EgressIPAM) (map[string][]net.IP, error) {
	assignedIPsByNode := map[string][]net.IP{}
	nodes, err := r.getSelectedNodes(egressIPAM)
	if err != nil {
		log.Error(err, "unable to get selected nodes for ", "egressIPAM", egressIPAM)
		return map[string][]net.IP{}, err
	}
	for _, node := range nodes {
		hostsubnet, err := r.getHostSubnet(node.GetName())
		if err != nil {
			log.Error(err, "unable to get hostsubnet for ", "node", node)
			return map[string][]net.IP{}, err
		}
		IPs := []net.IP{}
		for _, ipstr := range hostsubnet.EgressIPs {
			IPs = append(IPs, net.ParseIP(ipstr))
		}
		assignedIPsByNode[node.GetName()] = IPs
	}
	return assignedIPsByNode, nil
}
