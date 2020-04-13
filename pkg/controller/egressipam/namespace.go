package egressipam

import (
	"context"
	"errors"
	"net"
	"strings"

	multierror "github.com/hashicorp/go-multierror"
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
	egressIPAMNAme, ok := evt.Meta.GetAnnotations()[NamespaceAnnotation]
	if ok {
		q.Add(reconcile.Request{NamespacedName: types.NamespacedName{
			Name: egressIPAMNAme,
		}})
	}
}

// Update implements EventHandler
// trigger a router reconcile event for those routes that reference this secret
func (e *enqueForSelectedEgressIPAMNamespace) Update(evt event.UpdateEvent, q workqueue.RateLimitingInterface) {
	egressIPAMNAme, ok := evt.MetaOld.GetAnnotations()[NamespaceAnnotation]
	if ok {
		q.Add(reconcile.Request{NamespacedName: types.NamespacedName{
			Name: egressIPAMNAme,
		}})
	}

	egressIPAMNAme, ok = evt.MetaNew.GetAnnotations()[NamespaceAnnotation]
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

func (r *ReconcileEgressIPAM) getReferringNamespaces(rc *reconcileContext) (referringNmespaces map[string]corev1.Namespace, unassignedNamespaces []corev1.Namespace, assignedNamespaces []corev1.Namespace, err error) {
	namespaceList := &corev1.NamespaceList{}
	err = r.GetClient().List(context.TODO(), namespaceList, &client.ListOptions{})
	if err != nil {
		log.Error(err, "unable to retrive all namespaces")
		return map[string]corev1.Namespace{}, []corev1.Namespace{}, []corev1.Namespace{}, err
	}
	referringNamespaces := map[string]corev1.Namespace{}
	unassignedNamespaces = []corev1.Namespace{}
	assignedNamespaces = []corev1.Namespace{}
	for _, namespace := range namespaceList.Items {
		if value, ok := namespace.GetAnnotations()[NamespaceAnnotation]; ok && value == rc.egressIPAM.GetName() {
			referringNamespaces[namespace.GetName()] = namespace
			if _, ok := namespace.GetAnnotations()[NamespaceAssociationAnnotation]; ok {
				assignedNamespaces = append(assignedNamespaces, namespace)
			} else {
				unassignedNamespaces = append(unassignedNamespaces, namespace)
			}
		} else {
			continue
		}
	}
	return referringNamespaces, unassignedNamespaces, assignedNamespaces, nil
}

// returns a map if CIDRs and array of IPs CIDR are from the egressIPAM, IPs are currently assigned IPs.
// IPs in an array are supposed to belong the the CIDR, but no check is currently in place to ensure it.
// it expects that each namespace passed as parametr has exaclty the n IPs assigned where n is the number of CIDRs in egressIPAM
func sortIPsByCIDR(rc *reconcileContext) (map[string][]net.IP, error) {
	IPsByCIDR := map[string][]net.IP{}
	for _, cidr := range rc.cIDRs {
		IPsByCIDR[cidr] = []net.IP{}
	}
	for _, namespace := range rc.initiallyAssignedNamespaces {
		if value, ok := namespace.GetAnnotations()[NamespaceAssociationAnnotation]; ok {
			ipstrings := strings.Split(value, ",")
			for i, cidr := range rc.cIDRs {
				IP := net.ParseIP(ipstrings[i])
				if IP == nil {
					err := errors.New("unable to parse IP: " + ipstrings[i] + " in namespace: " + namespace.GetName())
					log.Error(err, "unable to parse ", "IP", ipstrings[i], "for namespace", namespace.GetName())
					return map[string][]net.IP{}, err
				}
				if !rc.netCIDRByCIDR[cidr].Contains(IP) {
					err := errors.New("IP: " + IP.String() + " does not belong to CIDR: " + cidr + " for namespace" + namespace.GetName())
					log.Error(err, "assigned ", "IP", IP.String(), "does not belong to CIDR", cidr, "for namespace", namespace.GetName())
					return map[string][]net.IP{}, err
				}
				IPsByCIDR[cidr] = append(IPsByCIDR[cidr], IP)
			}
		}
	}
	return IPsByCIDR, nil
}

func (r *ReconcileEgressIPAM) removeNamespaceAssignedIPs(rc *reconcileContext) error {
	results := make(chan error)
	defer close(results)
	for _, namespace := range rc.initiallyAssignedNamespaces {
		namespacec := namespace.DeepCopy()
		go func() {
			delete(namespacec.GetAnnotations(), NamespaceAssociationAnnotation)
			err := r.GetClient().Update(context.TODO(), namespacec, &client.UpdateOptions{})
			if err != nil {
				log.Error(err, "unable to update ", "namespace", namespacec.GetName())
				results <- err
				return
			}
			results <- nil
			return
		}()
	}
	var result *multierror.Error
	for range rc.initiallyAssignedNamespaces {
		multierror.Append(result, <-results)
	}
	return result.ErrorOrNil()
}

func getNamespaceMapKeys(namespaces map[string]corev1.Namespace) []string {
	namespaceNames := []string{}
	for namespace := range namespaces {
		namespaceNames = append(namespaceNames, namespace)
	}
	return namespaceNames
}

func getNamespaceNames(namespaces []corev1.Namespace) []string {
	namespaceNames := []string{}
	for _, namespace := range namespaces {
		namespaceNames = append(namespaceNames, namespace.GetName())
	}
	return namespaceNames
}
