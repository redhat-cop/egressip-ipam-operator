package egressipam

import (
	"bytes"
	"context"
	"errors"
	"net"
	"sort"
	"strings"

	"github.com/jpillora/ipmath"
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
		IPs, err := getNextAvailableIPs(IPsByCIDR)
		if err != nil {
			log.Error(err, "unable to assing new IPs for ", "namespace", unassignedNamespaces[i])
			return []corev1.Namespace{}, err
		}
		ipstrings := []string{}
		for _, IP := range IPs {
			ipstrings = append(ipstrings, IP.String())

		}
		unassignedNamespaces[i].ObjectMeta.Annotations[namespaceAssociationAnnotation] = strings.Join(ipstrings, ",")
		err = r.GetClient().Update(context.TODO(), &unassignedNamespaces[i], &client.UpdateOptions{})
		if err != nil {
			log.Error(err, "unable to update", "namespace", unassignedNamespaces[i])
			return []corev1.Namespace{}, err
		}
	}
	return unassignedNamespaces, nil
}

// returns a set of IPs. These IPs are the next available IP per CIDR.
// The map of CIDR is passed by reference and updated with the new IPs, so this function can be used in a loop.
func getNextAvailableIPs(IPsByCIDR map[*net.IPNet][]net.IP) ([]net.IP, error) {
	iPsByCIDR := IPsByCIDR
	assignedIPs := []net.IP{}
	for cidr := range iPsByCIDR {
		assignedIP, err := getNextAvailableIP(cidr, iPsByCIDR[cidr])
		if err != nil {
			log.Error(err, "unable to assign get next ip for", "cidr", cidr)
			return []net.IP{}, err
		}
		assignedIPs = append(assignedIPs, assignedIP)
		iPsByCIDR[cidr] = append(iPsByCIDR[cidr], assignedIP)
	}
	return assignedIPs, nil
}

func getNextAvailableIP(cidr *net.IPNet, assignedIPs []net.IP) (net.IP, error) {
	if uint32(len(assignedIPs)) == ipmath.NetworkSize(cidr) {
		return net.IP{}, errors.New("no more available IPs in this CIDR")
	}
	sortIPs(assignedIPs)
	for i := range assignedIPs {
		if !assignedIPs[i].Equal(ipmath.DeltaIP(cidr.IP, i+1)) {
			return ipmath.DeltaIP(cidr.IP, i+1), nil
		}
	}
	return ipmath.DeltaIP(cidr.IP, len(assignedIPs)+1), nil
}

func sortIPs(ips []net.IP) {
	sort.Slice(ips, func(i, j int) bool {
		return bytes.Compare(ips[i], ips[j]) < 0
	})
}

// returns a map with nodes and egress IPs that have been assigned to them. This should preserve IPs that are already assigned.
func assignIPsToNodes(nodesByCIDR map[*net.IPNet][]corev1.Node, assignedIPsByCIDR map[*net.IPNet][]net.IP) (map[*corev1.Node][]net.IP, error) {
	return map[*corev1.Node][]net.IP{}, errors.New("not implemented")
}

func (r *ReconcileEgressIPAM) getCurrentlyNodeAssignedIPsByCIDR(nodesByCIDR map[*net.IPNet][]corev1.Node, assignedIPsByCIDR map[*net.IPNet][]net.IP) map[*net.IPNet][]net.IP {
	currentlyAssignedIPsByNodes := r.getCurrentlyNodeAssignedIPsByCIDR(nodesByCIDR)
}

func (r *ReconcileEgressIPAM) getCurrentlyAssignedIPsByNode(nodesByCIDR map[*net.IPNet][]corev1.Node) (map[*corev1.Node][]net.IP, error) {
	currentlyAssignedIPsByNodes := map[*corev1.Node][]net.IP{}
	for _, nodes := range nodesByCIDR {
		for _, node := range nodes {
			assignedIPs := []net.IP{}
			hostsubnet, err := r.getHostSubnet(&node)
			if err != nil {
				log.Error(err, "unable to get hostsubnet for ", "node", node)
				return map[*corev1.Node][]net.IP{}, err
			}
			for _, ipstr := range hostsubnet.EgressIPs {
				assignedIPs = append(assignedIPs, net.ParseIP(ipstr))
			}
			currentlyAssignedIPsByNodes[&node] = assignedIPs
		}
	}
	return currentlyAssignedIPsByNodes, nil
}
