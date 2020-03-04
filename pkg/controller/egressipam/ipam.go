package egressipam

import (
	"context"
	"errors"
	"net"
	"strings"

	redhatcopv1alpha1 "github.com/redhat-cop/egressip-ipam-operator/pkg/apis/redhatcop/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Assigns ips to unassigned namespaces and updates them
func (r *ReconcileEgressIPAM) assignIPsToNamespaces(unassignedNamespaces []corev1.Namespace, assignedNamespaces []corev1.Namespace, egressIPAM *redhatcopv1alpha1.EgressIPAM) ([]corev1.Namespace, error) {
	IPsByCIDR, err := sortIPsByCIDR(assignedNamespaces, egressIPAM)
	if err != nil {
		log.Error(err, "unable to sort assignedIPs by CIDR")
		return []corev1.Namespace{}, err
	}
	for i := range unassignedNamespaces {
		IPs, err := getNextAvailableIPs(&IPsByCIDR)
		if err != nil {
			log.Error(err, "unable to assing new IPs for ", "namespace", unassignedNamespaces[i])
			return []corev1.Namespace{}, err
		}
		ipstrings := []string{}
		for _, IP := range IPs {
			ipstrings = append(ipstrings, IP.String())

		}
		unassignedNamespaces[i].ObjectMeta.Annotations[namespaceAssociationAnnotation] = strings.Join(ipstrings, "'")
		err = r.GetClient().Update(context.TODO(), &unassignedNamespaces[i], &client.UpdateOptions{})
		if err != nil {
			log.Error(err, "unable to update", "namespace", unassignedNamespaces[i])
			return []corev1.Namespace{}, err
		}
	}
	return unassignedNamespaces, nil
}

// returns a set of IPs. These IPs are the next available IP per CIDR.
// The map of CIDR is passed by referne and updated with the new IPs, so this function can be used in a loop.
func getNextAvailableIPs(IPsByCIDR *map[*net.IPNet][]net.IP) ([]net.IP, error) {
	return []net.IP{}, errors.New("not implemented")
}

// returns a map with nodes and egress IPs that have been assigned to them. This should preserve IPs that are already assigned.
func assignIPsToNodes(nodesByCIDR map[*net.IPNet][]corev1.Node, assignedIPsByCIDR map[*net.IPNet][]net.IP) (map[*corev1.Node][]net.IP, error) {
	return map[*corev1.Node][]net.IP{}, errors.New("not implemented")
}
