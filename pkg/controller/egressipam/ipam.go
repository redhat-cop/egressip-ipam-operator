package egressipam

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"sort"
	"strings"

	"github.com/jpillora/ipmath"
	ocpconfigv1 "github.com/openshift/api/config/v1"
	redhatcopv1alpha1 "github.com/redhat-cop/egressip-ipam-operator/pkg/apis/redhatcop/v1alpha1"
	"github.com/scylladb/go-set/strset"
	"github.com/scylladb/go-set/u32set"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func getReservedIPsByCIDR(egressIPAM *redhatcopv1alpha1.EgressIPAM) map[string][]net.IP {
	reservedIPsByCIDR := map[string][]net.IP{}
	for _, cidrAssignment := range egressIPAM.Spec.CIDRAssignments {
		ips := []net.IP{}
		for _, ipstr := range cidrAssignment.ReservedIPs {
			ips = append(ips, net.ParseIP(ipstr))
		}
		reservedIPsByCIDR[cidrAssignment.CIDR] = ips
	}
	return reservedIPsByCIDR
}

// Assigns ips to unassigned namespaces and updates them
func (r *ReconcileEgressIPAM) assignIPsToNamespaces(unassignedNamespaces []corev1.Namespace, assignedNamespaces []corev1.Namespace, egressIPAM *redhatcopv1alpha1.EgressIPAM) ([]corev1.Namespace, error) {
	IPsByCIDR, err := sortIPsByCIDR(assignedNamespaces, egressIPAM)
	if err != nil {
		log.Error(err, "unable to sort assignedIPs by CIDR")
		return []corev1.Namespace{}, err
	}
	log.V(1).Info("currently assigned ", "IPs by CIDR", IPsByCIDR)
	//in all cases we need to add the base network and the broadcast address
	for cidr := range IPsByCIDR {
		base, cidrt, err := net.ParseCIDR(cidr)
		if err != nil {
			log.Error(err, "unable to parse", "cidr", cidr)
			return []corev1.Namespace{}, err
		}
		broadcastip := ipmath.FromUInt32((^binary.BigEndian.Uint32([]byte(cidrt.Mask))) | binary.BigEndian.Uint32([]byte(base.To4())))
		IPsByCIDR[cidr] = append(IPsByCIDR[cidr], base, broadcastip)
	}
	log.V(1).Info("adding always excluded network IPs ", "IPs by CIDR", IPsByCIDR)

	// add reserved ips
	reservedIPsByCIDR := getReservedIPsByCIDR(egressIPAM)
	for cidr := range IPsByCIDR {
		IPsByCIDR[cidr] = append(IPsByCIDR[cidr], reservedIPsByCIDR[cidr]...)
	}
	log.V(1).Info("adding reserved IPs ", "IPs by CIDR", IPsByCIDR)

	// if nodes' IPs are in the CIDR, they should count as assigned.
	nodesIPsByCIDR, err := r.getNodesIPsByCIDR(egressIPAM)
	if err != nil {
		log.Error(err, "unable to get nodesIPs by CIDR")
		return []corev1.Namespace{}, err
	}
	for cidr := range IPsByCIDR {
		IPsByCIDR[cidr] = append(IPsByCIDR[cidr], nodesIPsByCIDR[cidr]...)
	}
	log.V(1).Info("adding nodes IPs (if in the same CIDR) ", "IPs by CIDR", IPsByCIDR)

	// if in AWS we have some reserved addresses:
	infrastructure, err := r.getInfrastructure()
	if err != nil {
		log.Error(err, "unable to get infrastructure")
		return []corev1.Namespace{}, err
	}
	if infrastructure.Status.Platform == ocpconfigv1.AWSPlatformType {
		//add some reserved IPs
		for cidr := range IPsByCIDR {
			base, _, err := net.ParseCIDR(cidr)
			if err != nil {
				log.Error(err, "unable to parse", "cidr", cidr)
				return []corev1.Namespace{}, err
			}
			IPsByCIDR[cidr] = append(IPsByCIDR[cidr], ipmath.DeltaIP(base, 1), ipmath.DeltaIP(base, 2), ipmath.DeltaIP(base, 3))
		}
	}
	log.V(1).Info("adding cloud infrastrcture reserved IPs ", "IPs by CIDR", IPsByCIDR)

	for cidr := range IPsByCIDR {
		IPsByCIDR[cidr] = sortIPs(IPsByCIDR[cidr])
	}
	log.V(1).Info("sorted reserved IPs ", "IPs by CIDR", IPsByCIDR)
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
		log.Info("ips assigned to", "namespace", unassignedNamespaces[i].GetName(), "ips", ipstrings)
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
func getNextAvailableIPs(IPsByCIDR map[string][]net.IP) ([]net.IP, error) {
	log.V(1).Info("Assigning new IPs from", "IPs by CIDR", IPsByCIDR)
	iPsByCIDR := IPsByCIDR
	assignedIPs := []net.IP{}
	for cidr := range iPsByCIDR {
		assignedIP, newIPs, err := getNextAvailableIP(cidr, iPsByCIDR[cidr])
		if err != nil {
			log.Error(err, "unable to assign get next ip for", "cidr", cidr)
			return []net.IP{}, err
		}
		assignedIPs = append(assignedIPs, assignedIP)
		iPsByCIDR[cidr] = newIPs
	}
	log.V(1).Info("Assigned", "new IPs from", assignedIPs, " new IPs by CIDR", IPsByCIDR)
	return assignedIPs, nil
}

func getNextAvailableIP(cidrs string, assignedIPs []net.IP) (net.IP, []net.IP, error) {
	log.V(1).Info("Assigning new IP from", "CIDR", cidrs, "with already assigned IPs", assignedIPs)
	_, cidr, err := net.ParseCIDR(cidrs)
	if err != nil {
		log.Error(err, "unable to parse", "cidr", cidrs)
		return net.IP{}, []net.IP{}, err
	}
	if uint32(len(assignedIPs)) == ipmath.NetworkSize(cidr) {
		return net.IP{}, []net.IP{}, errors.New("no more available IPs in this CIDR")
	}
	for i := range assignedIPs {
		if !assignedIPs[i].Equal(ipmath.DeltaIP(cidr.IP, i)) {
			assignedIP := ipmath.DeltaIP(cidr.IP, i)
			assignedIPs = append(assignedIPs[:i], append([]net.IP{assignedIP}, assignedIPs[i:]...)...)
			log.V(1).Info("Assigned ", "IP", assignedIP, "new assigned IPs", assignedIPs)
			return assignedIP, assignedIPs, nil
		}
	}
	return net.IP{}, []net.IP{}, errors.New("we should never get here")
}

func sortIPs(ips []net.IP) []net.IP {
	ipstrs := []uint32{}
	for _, ip := range ips {
		ipstrs = append(ipstrs, ipmath.ToUInt32(ip))
	}
	//shake off eventual duplicates
	ipstrs = u32set.New(ipstrs...).List()
	sort.Slice(ipstrs, func(i, j int) bool { return ipstrs[i] < ipstrs[j] })
	ips = []net.IP{}
	for _, ipstr := range ipstrs {
		ips = append(ips, ipmath.FromUInt32(ipstr))
	}
	// ips2 := make([]net.IP, len(ips))
	// copy(ips2, ips)
	// sort.Slice(ips2, func(i, j int) bool {
	// 	return bytes.Compare(ips2[i], ips2[j]) < 0
	// })
	return ips
}

// returns a map with nodes and egress IPs that have been assigned to them. This should preserve IPs that are already assigned.
func (r *ReconcileEgressIPAM) assignIPsToNodes(assignedIPsByNode map[string][]string, assignedNamespaces []corev1.Namespace, egressIPAM *redhatcopv1alpha1.EgressIPAM) (map[string][]string, error) {
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
		for cidrstr := range assignedIPsToNodesByCIDR {
			_, cidr, err := net.ParseCIDR(cidrstr)
			if err != nil {
				log.Error(err, "unable to conver to cidr ", "string", cidrstr)
				return map[string][]string{}, err
			}
			for _, ip := range ipsbn {
				if cidr.Contains(net.ParseIP(ip)) {
					assignedIPsToNodesByCIDR[cidrstr] = append(assignedIPsToNodesByCIDR[cidrstr], ip)
				}
			}
		}
	}

	// 2. get assignedIPsToNamespacesByCIDR
	for _, namespace := range assignedNamespaces {
		ipsstr, ok := namespace.GetAnnotations()[namespaceAssociationAnnotation]
		if !ok {
			return map[string][]string{}, errors.New("unable to find ips in namespace" + namespace.GetName())
		}
		ipsbn := strings.Split(ipsstr, ",")
		for cidrstr := range assignedIPsToNamespaceByCIDR {
			_, cidr, err := net.ParseCIDR(cidrstr)
			if err != nil {
				log.Error(err, "unable to conver to cidr ", "string", cidrstr)
				return map[string][]string{}, err
			}
			for _, ipstr := range ipsbn {
				ip := net.ParseIP(ipstr)
				if cidr.Contains(ip) {
					assignedIPsToNamespaceByCIDR[cidrstr] = append(assignedIPsToNamespaceByCIDR[cidrstr], ip.String())
				}
			}
		}
	}

	// 3. calculate toBeAssignedIPsByCIDR
	for cidr := range assignedIPsToNamespaceByCIDR {
		toBeAssignedToNodesIPsByCIDR[cidr] = strset.Difference(strset.New(assignedIPsToNamespaceByCIDR[cidr]...), strset.New(assignedIPsToNodesByCIDR[cidr]...)).List()
	}

	// 4. get NodesByCIDR
	nodesByCIDR, err := r.getSelectedNodesByCIDR(egressIPAM)
	if err != nil {
		log.Error(err, "unable to get nodes by CIDR")
		return map[string][]string{}, err
	}

	// 5. calculate NodesByNumberOfAssignedIPByCIDR
	nodesByNumberOfAssignedIPsByCIDR := map[string]map[int][]corev1.Node{}
	for cidr := range toBeAssignedToNodesIPsByCIDR {
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
			assignedIPsByNode[node.GetName()] = append(assignedIPsByNode[node.GetName()], ip)
			// remove the node from the minIPsPerNode map
			nodesByNumberOfAssignedIPsByCIDR[cidr][minIPsPerNode] = nodesByNumberOfAssignedIPsByCIDR[cidr][minIPsPerNode][1:]
			// add the node to the minIPsPerNode+1 map
			nodesByNumberOfAssignedIPsByCIDR[cidr][minIPsPerNode+1] = append(nodesByNumberOfAssignedIPsByCIDR[cidr][minIPsPerNode+1], node)
		}
	}

	return assignedIPsByNode, nil
}

func getMinKey(nodemap map[int][]corev1.Node) int {
	numbers := []int{}
	for n := range nodemap {
		numbers = append(numbers, n)
	}
	sort.Ints(numbers)
	return numbers[0]
}
