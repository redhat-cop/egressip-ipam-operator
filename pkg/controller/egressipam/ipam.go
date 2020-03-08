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
	"github.com/scylladb/go-set/strset"
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
func (r *ReconcileEgressIPAM) assignIPsToNodes(assignedIPsByNode map[string][]net.IP, assignedNamespaces []corev1.Namespace, egressIPAM *redhatcopv1alpha1.EgressIPAM) (map[string][]net.IP, error) {
	// 1. get assignedIPsToNodesByCIDR
	// 2. get assignedIPsToNamespacesByCIDR
	// 3. calculate toBeAssignedIPsByCIDR
	// 4. get NodesByCIDR
	// 5. calculate NodesBy#AssignedIPByCIDR
	// 6. assign IPs to the least assigned nodes, update map, by CIDR

	assignedIPsToNodesByCIDR := map[string][]string{}
	assignedIPsToNamespaceByCIDR := map[string][]string{}
	toBeAssignedToNodesIPsByCIDR := map[string][]string{}
	for _, cidrAssignment := range egressIPAM.Spec.CIDRAssignments {
		assignedIPsToNodesByCIDR[cidrAssignment.CIDR] = []string{}
		assignedIPsToNamespaceByCIDR[cidrAssignment.CIDR] = []string{}
		toBeAssignedToNodesIPsByCIDR[cidrAssignment.CIDR] = []string{}
	}
	// 1. get assignedIPsToNodesByCIDR
	for _, ipsbn := range assignedIPsByNode {
		for cidrstr, _ := range assignedIPsToNodesByCIDR {
			_, cidr, err := net.ParseCIDR(cidrstr)
			if err != nil {
				log.Error(err, "unable to conver to cidr ", "string", cidrstr)
				return map[string][]net.IP{}, err
			}
			for _, ip := range ipsbn {
				if cidr.Contains(ip) {
					assignedIPsToNodesByCIDR[cidrstr] = append(assignedIPsToNodesByCIDR[cidrstr], ip.String())
				}
			}
		}
	}

	// 2. get assignedIPsToNamespacesByCIDR
	for _, namespace := range assignedNamespaces {
		ipsstr, ok := namespace.GetAnnotations()[namespaceAssociationAnnotation]
		if !ok {
			return map[string][]net.IP{}, errors.New("unable to find ips in namespace" + namespace.GetName())
		}
		ipsbn := strings.Split(ipsstr, ",")
		for cidrstr, _ := range assignedIPsToNamespaceByCIDR {
			_, cidr, err := net.ParseCIDR(cidrstr)
			if err != nil {
				log.Error(err, "unable to conver to cidr ", "string", cidrstr)
				return map[string][]net.IP{}, err
			}
			for _, ipstr := range ipsbn {
				ip := net.ParseIP(ipstr)
				if cidr.Contains(ip) {
					assignedIPsToNodesByCIDR[cidrstr] = append(assignedIPsToNodesByCIDR[cidrstr], ip.String())
				}
			}
		}
	}

	// 3. calculate toBeAssignedIPsByCIDR
	for cidr, _ := range assignedIPsToNamespaceByCIDR {
		toBeAssignedToNodesIPsByCIDR[cidr] = strset.Difference(strset.New(assignedIPsToNamespaceByCIDR[cidr]...), strset.New(assignedIPsToNodesByCIDR[cidr]...)).List()
	}

	// 4. get NodesByCIDR
	nodesByCIDR, err := r.getSelectedNodesByCIDR(egressIPAM)
	if err != nil {
		log.Error(err, "unable to get nodes by CIDR")
		return map[string][]net.IP{}, err
	}

	// 5. calculate NodesByNumberOfAssignedIPByCIDR
	nodesByNumberOfAssignedIPsByCIDR := map[string]map[int][]corev1.Node{}
	for cidr, _ := range toBeAssignedToNodesIPsByCIDR {
		nodesByNumberOfAssignedIPsByCIDR[cidr] = map[int][]corev1.Node{}
		for _, node := range nodesByCIDR[cidr] {
			nodesByNumberOfAssignedIPsByCIDR[cidr][len(assignedIPsToNodesByCIDR[cidr])] = append(nodesByNumberOfAssignedIPsByCIDR[cidr][len(assignedIPsToNodesByCIDR[cidr])], node)
		}
	}

	// 6. assign IPs to the least assigned nodes, update map, by CIDR
	for cidr, ips := range toBeAssignedToNodesIPsByCIDR {
		for _, ip := range ips {
			//pick the first node with the least IPs in this CIDR
			minIPsPerNode := getMinKey(nodesByNumberOfAssignedIPsByCIDR[cidr])
			node := nodesByNumberOfAssignedIPsByCIDR[cidr][minIPsPerNode][0]
			// add the node to the assignedIP per node map
			assignedIPsByNode[node.GetName()] = append(assignedIPsByNode[node.GetName()], net.ParseIP(ip))
			// remove the node from the minIPsPerNode map
			nodesByNumberOfAssignedIPsByCIDR[cidr][minIPsPerNode] = nodesByNumberOfAssignedIPsByCIDR[cidr][minIPsPerNode][1:]
			// add the node to the minIPsPerNode+1 map
			nodesByNumberOfAssignedIPsByCIDR[cidr][minIPsPerNode+1] = append(nodesByNumberOfAssignedIPsByCIDR[cidr][minIPsPerNode+1], node)
		}
	}

	return assignedIPsByNode, nil
}

func getMinKey(nodemap map[int][]corev1.Node) int {
	return 0
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
