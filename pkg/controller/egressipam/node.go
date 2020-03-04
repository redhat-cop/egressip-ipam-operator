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
		if matches, _ := matchesNode(&egressIPAM, node); matches {
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
		if matches, _ := matchesNode(&egressIPAM, node); matches {
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
func (r *ReconcileEgressIPAM) getSelectedNodesByCIDR(egressIPAM *redhatcopv1alpha1.EgressIPAM) (map[*net.IPNet][]corev1.Node, error) {
	nodeList := &corev1.NodeList{}
	selector, err := metav1.LabelSelectorAsSelector(&egressIPAM.Spec.NodeSelector)
	if err != nil {
		log.Error(err, "unable to create selector from label selector", "selector", &egressIPAM.Spec.NodeSelector)
		return map[*net.IPNet][]corev1.Node{}, err
	}
	err = r.GetClient().List(context.TODO(), nodeList, &client.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		log.Error(err, "unable to list sleected nodes", "selector", egressIPAM.Spec.NodeSelector)
		return map[*net.IPNet][]corev1.Node{}, err
	}
	selectedNdesByCIDR := map[*net.IPNet][]corev1.Node{}
	CIDRbyLabel := map[string]*net.IPNet{}
	for _, cidrAssignment := range egressIPAM.Spec.CIDRAssignments {
		_, cidr, err := net.ParseCIDR(cidrAssignment.CIDR)
		if err != nil {
			log.Error(err, "unable to parse", "cidr", cidrAssignment.CIDR)
			return map[*net.IPNet][]corev1.Node{}, err
		}
		CIDRbyLabel[cidrAssignment.LabelValue] = cidr
		selectedNdesByCIDR[cidr] = []corev1.Node{}
	}
	for _, node := range nodeList.Items {
		if value, ok := node.GetLabels()[egressIPAM.Spec.NodeLabel]; ok {
			if cidr, ok := CIDRbyLabel[value]; ok {
				selectedNdesByCIDR[cidr] = append(selectedNdesByCIDR[cidr], node)
			}
		}
	}
	return selectedNdesByCIDR, nil
}
