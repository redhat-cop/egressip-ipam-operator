package egressipam

import (
	"context"
	"net"
	"strings"

	redhatcopv1alpha1 "github.com/redhat-cop/egressip-ipam-operator/pkg/apis/redhatcop/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

func (r *ReconcileEgressIPAM) getReferringNamespaces(egressIPAM *redhatcopv1alpha1.EgressIPAM) (unassignedNamespaces []corev1.Namespace, assignedNamespaces []corev1.Namespace, err error) {
	namespaceList := &corev1.NamespaceList{}
	err = r.GetClient().List(context.TODO(), namespaceList, &client.ListOptions{})
	if err != nil {
		log.Error(err, "unable to retrive all namespaces")
		return []corev1.Namespace{}, []corev1.Namespace{}, err
	}
	unassignedNamespaces = []corev1.Namespace{}
	assignedNamespaces = []corev1.Namespace{}
	for _, namespace := range namespaceList.Items {
		if value, ok := namespace.GetAnnotations()[namespaceAnnotation]; ok && value == egressIPAM.GetName() {
			if _, ok := namespace.GetAnnotations()[namespaceAssociationAnnotation]; ok {
				assignedNamespaces = append(assignedNamespaces, namespace)
			} else {
				unassignedNamespaces = append(unassignedNamespaces, namespace)
			}
		} else {
			continue
		}
	}
	return unassignedNamespaces, assignedNamespaces, nil
}

// returns a map if CIDRs and array of IPs CIDR are from the egressIPAM, IPs are currently assigned IPs.
// IPs in an array are supposed to belong the the CIDR, but no check is currently in place to ensure it.
// it expects that each namespace passed as parametr has exaclty the n IPs assigned where n is the number of CIDRs in egressIPAM
func sortIPsByCIDR(assignedNamespaces []corev1.Namespace, egressIPAM *redhatcopv1alpha1.EgressIPAM) (map[string][]net.IP, error) {
	IPsMatrix := [][]net.IP{}
	IPsByCIDR := map[string][]net.IP{}
	for range egressIPAM.Spec.CIDRAssignments {
		IPsMatrix = append(IPsMatrix, []net.IP{})
	}
	for _, namespace := range assignedNamespaces {
		if value, ok := namespace.GetAnnotations()[namespaceAssociationAnnotation]; ok {
			ipstrings := strings.Split(value, ",")
			for i := range IPsMatrix {
				IP := net.ParseIP(ipstrings[i])
				IPsMatrix[i] = append(IPsMatrix[i], IP)
			}
		}
	}
	for i, cidrAssignment := range egressIPAM.Spec.CIDRAssignments {
		_, network, err := net.ParseCIDR(cidrAssignment.CIDR)
		if err != nil {
			log.Error(err, "unable to parse ", "cidr", cidrAssignment.CIDR)
			return map[string][]net.IP{}, err
		}
		IPsByCIDR[network.String()] = IPsMatrix[i]
	}
	return IPsByCIDR, nil
}

func (r *ReconcileEgressIPAM) removeNamespaceAssignedIPs(egressIPAM *redhatcopv1alpha1.EgressIPAM) error {
	_, assignedNamespaces, err := r.getReferringNamespaces(egressIPAM)
	if err != nil {
		log.Error(err, "unable to retrieve refrerring namespaces for ", "egressIPAM", egressIPAM)
		return err
	}
	for _, namespace := range assignedNamespaces {
		delete(namespace.GetAnnotations(), namespaceAssociationAnnotation)
		err := r.GetClient().Update(context.TODO(), &namespace, &client.UpdateOptions{})
		if err != nil {
			log.Error(err, "unable to update ", "namespace", namespace)
			return err
		}
	}
	return nil
}
