package egressipam

import (
	"encoding/binary"
	"errors"
	"net"
	"sort"
	"strings"

	"github.com/jpillora/ipmath"
	"github.com/redhat-cop/egressip-ipam-operator/controllers/egressipam/reconcilecontext"
	"github.com/scylladb/go-set/strset"
	"github.com/scylladb/go-set/u32set"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Assigns ips to unassigned namespaces and updates them
func (r *EgressIPAMReconciler) assignIPsToNamespaces(rc *reconcilecontext.ReconcileContext) ([]corev1.Namespace, error) {
	IPsByCIDR, err := r.sortIPsByCIDR(rc)
	if err != nil {
		r.Log.Error(err, "unable to sort assignedIPs by CIDR")
		return []corev1.Namespace{}, err
	}
	r.Log.V(1).Info("currently assigned ", "IPs by CIDR", IPsByCIDR)
	//in all cases we need to add the base network and the broadcast address
	for cidr := range IPsByCIDR {
		base, cidrt, err := net.ParseCIDR(cidr)
		if err != nil {
			r.Log.Error(err, "unable to parse", "cidr", cidr)
			return []corev1.Namespace{}, err
		}
		broadcastip := ipmath.FromUInt32((^binary.BigEndian.Uint32([]byte(cidrt.Mask))) | binary.BigEndian.Uint32([]byte(base.To4())))
		IPsByCIDR[cidr] = append(IPsByCIDR[cidr], base, broadcastip)
	}
	r.Log.V(1).Info("adding always excluded network IPs ", "IPs by CIDR", IPsByCIDR)

	// add reserved ips
	//reservedIPsByCIDR := getReservedIPsByCIDR(egressIPAM)
	for cidr := range IPsByCIDR {
		IPsByCIDR[cidr] = append(IPsByCIDR[cidr], rc.ReservedIPsByCIDR[cidr]...)
	}
	r.Log.V(1).Info("adding reserved IPs ", "IPs by CIDR", IPsByCIDR)

	// if nodes' IPs are in the CIDR, they should count as assigned.
	nodesIPsByCIDR, err := r.getNodesIPsByCIDR(rc)
	if err != nil {
		r.Log.Error(err, "unable to get nodesIPs by CIDR")
		return []corev1.Namespace{}, err
	}
	for cidr := range IPsByCIDR {
		IPsByCIDR[cidr] = append(IPsByCIDR[cidr], nodesIPsByCIDR[cidr]...)
	}
	r.Log.V(1).Info("adding nodes IPs (if in the same CIDR) ", "IPs by CIDR", IPsByCIDR)

	//adding cloud provider IPs by CIDR
	infraIPsByCIDR, err := rc.Infra.GetUsedIPsByCIDR(rc)
	if err != nil {
		r.Log.Error(err, "unable to get cloud infra used IPs by CIDR")
		return []corev1.Namespace{}, err
	}
	for cidr := range IPsByCIDR {
		IPsByCIDR[cidr] = append(IPsByCIDR[cidr], infraIPsByCIDR[cidr]...)
	}

	r.Log.V(1).Info("adding cloud infrastructure reserved IPs ", "IPs by CIDR", IPsByCIDR)

	IPsByCIDR = removeDuplicates(IPsByCIDR)

	r.Log.V(1).Info("final  ", "IPs by CIDR", IPsByCIDR)

	for cidr := range IPsByCIDR {
		IPsByCIDR[cidr] = sortIPs(IPsByCIDR[cidr])
	}
	r.Log.V(1).Info("sorted reserved IPs ", "IPs by CIDR", IPsByCIDR)
	newlyAssignedNamespaces := []corev1.Namespace{}
	for _, unamespace := range rc.UnAssignedNamespaces {
		namespace := unamespace.DeepCopy()
		newIPsByCIDRs, err := r.getNextAvailableIPs(IPsByCIDR)
		if err != nil {
			r.Log.Error(err, "unable to assing new IPs for ", "namespace", namespace.GetName())
			return []corev1.Namespace{}, err
		}
		ipstrings := []string{}
		for _, cidr := range rc.CIDRs {
			ipstrings = append(ipstrings, newIPsByCIDRs[cidr].String())
		}
		r.Log.Info("ips assigned to", "namespace", namespace.GetName(), "ips", ipstrings)
		namespace.ObjectMeta.Annotations[NamespaceAssociationAnnotation] = strings.Join(ipstrings, ",")
		err = r.GetClient().Update(rc.Context, namespace, &client.UpdateOptions{})
		if err != nil {
			r.Log.Error(err, "unable to update", "namespace", namespace.GetName())
			return []corev1.Namespace{}, err
		}
		newlyAssignedNamespaces = append(newlyAssignedNamespaces, *namespace)
	}
	return newlyAssignedNamespaces, nil
}

func removeDuplicates(IPsByCIDR map[string][]net.IP) map[string][]net.IP {
	result := map[string][]net.IP{}
	for cidr, IPs := range IPsByCIDR {
		ipSet := strset.New()
		for _, IP := range IPs {
			ipSet.Add(IP.String())
		}
		netIPs := []net.IP{}
		for _, ip := range ipSet.List() {
			netIPs = append(netIPs, net.ParseIP(ip))
		}
		result[cidr] = netIPs
	}
	return result
}

// returns a set of IPs. These IPs are the next available IP per CIDR.
// The map of CIDR is passed by reference and updated with the new IPs, so this function can be used in a loop.
func (r *EgressIPAMReconciler) getNextAvailableIPs(IPsByCIDR map[string][]net.IP) (map[string]net.IP, error) {
	r.Log.V(1).Info("Assigning new IPs from", "IPs by CIDR", IPsByCIDR)
	//iPsByCIDR := IPsByCIDR
	assignedIPs := map[string]net.IP{}
	for cidr := range IPsByCIDR {
		assignedIP, newIPs, err := r.getNextAvailableIP(cidr, IPsByCIDR[cidr])
		if err != nil {
			r.Log.Error(err, "unable to assign get next ip for", "cidr", cidr)
			return map[string]net.IP{}, err
		}
		assignedIPs[cidr] = assignedIP
		IPsByCIDR[cidr] = newIPs
	}
	r.Log.V(1).Info("Assigned", "new IPs from", assignedIPs, " new IPs by CIDR", IPsByCIDR)
	return assignedIPs, nil
}

func (r *EgressIPAMReconciler) getNextAvailableIP(cidrs string, assignedIPs []net.IP) (net.IP, []net.IP, error) {
	r.Log.V(1).Info("Assigning new IP from", "CIDR", cidrs, "with already assigned IPs", assignedIPs)
	_, cidr, err := net.ParseCIDR(cidrs)
	if err != nil {
		r.Log.Error(err, "unable to parse", "cidr", cidrs)
		return net.IP{}, []net.IP{}, err
	}
	if uint32(len(assignedIPs)) == ipmath.NetworkSize(cidr) {
		return net.IP{}, []net.IP{}, errors.New("no more available IPs in this CIDR: " + cidr.String())
	}
	for i := range assignedIPs {
		if !assignedIPs[i].Equal(ipmath.DeltaIP(cidr.IP, i)) {
			assignedIP := ipmath.DeltaIP(cidr.IP, i)
			assignedIPs = append(assignedIPs[:i], append([]net.IP{assignedIP}, assignedIPs[i:]...)...)
			r.Log.V(1).Info("Assigned ", "IP", assignedIP, "new assigned IPs", assignedIPs)
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
func (r *EgressIPAMReconciler) assignIPsToNodes(rc *reconcilecontext.ReconcileContext) (map[string][]string, error) {
	// 1. get assignedIPsToNodesByCIDR
	// 2. get assignedIPsToNamespacesByCIDR
	// 3. calculate toBeAssignedIPsByCIDR
	// 4. recalculate assignedIPsToNodesByCIDR
	// 5 recalculate assignedIPsByNode
	// 6. get NodesByCIDR
	// 7. calculate NodesBy#AssignedIPByCIDR
	// 8. assign IPs to the least assigned nodes, update map, by CIDR

	assignedIPsToNodesByCIDR := map[string][]string{}
	assignedIPsToNamespaceByCIDR := map[string][]string{}
	toBeAssignedToNodesIPsByCIDR := map[string][]string{}
	for _, cidr := range rc.CIDRs {
		assignedIPsToNodesByCIDR[cidr] = []string{}
		assignedIPsToNamespaceByCIDR[cidr] = []string{}
		toBeAssignedToNodesIPsByCIDR[cidr] = []string{}
	}
	// 1. get assignedIPsToNodesByCIDR
	for _, ipsbn := range rc.InitiallyAssignedIPsByNode {
		for cidrstr := range assignedIPsToNodesByCIDR {
			_, cidr, err := net.ParseCIDR(cidrstr)
			if err != nil {
				r.Log.Error(err, "unable to conver to cidr ", "string", cidrstr)
				return map[string][]string{}, err
			}
			for _, ip := range ipsbn {
				if cidr.Contains(net.ParseIP(ip)) {
					assignedIPsToNodesByCIDR[cidrstr] = append(assignedIPsToNodesByCIDR[cidrstr], ip)
				}
			}
		}
	}
	r.Log.V(1).Info("", "assignedIPsToNodesByCIDR: ", assignedIPsToNodesByCIDR)
	// 2. get assignedIPsToNamespacesByCIDR
	for _, namespace := range rc.FinallyAssignedNamespaces {
		ipsstr, ok := namespace.GetAnnotations()[NamespaceAssociationAnnotation]
		if !ok {
			return map[string][]string{}, errors.New("unable to find ips in namespace" + namespace.GetName())
		}
		ipsbn := strings.Split(ipsstr, ",")
		for cidrstr := range assignedIPsToNamespaceByCIDR {
			_, cidr, err := net.ParseCIDR(cidrstr)
			if err != nil {
				r.Log.Error(err, "unable to conver to cidr ", "string", cidrstr)
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
	r.Log.V(1).Info("", "assignedIPsToNamespaceByCIDR: ", assignedIPsToNamespaceByCIDR)
	// 3. calculate toBeAssignedIPsByCIDR
	for cidr := range assignedIPsToNamespaceByCIDR {
		toBeAssignedToNodesIPsByCIDR[cidr] = strset.Difference(strset.New(assignedIPsToNamespaceByCIDR[cidr]...), strset.New(assignedIPsToNodesByCIDR[cidr]...)).List()
	}

	r.Log.V(1).Info("", "toBeAssignedToNodesIPsByCIDR: ", toBeAssignedToNodesIPsByCIDR)

	// 4. recalculate assignedIPsToNodesByCIDR
	for cidr := range assignedIPsToNamespaceByCIDR {
		assignedIPsToNodesByCIDR[cidr] = strset.Intersection(strset.New(assignedIPsToNodesByCIDR[cidr]...), strset.New(assignedIPsToNamespaceByCIDR[cidr]...)).List()
	}
	r.Log.V(1).Info("new", "assignedIPsToNodesByCIDR: ", assignedIPsToNodesByCIDR)

	// 5 recalculate assignedIPsByNode
	newAssignedIPsByNode := map[string][]string{}
	for _, assignedIPs := range assignedIPsToNodesByCIDR {
		for _, assignedIP := range assignedIPs {
			for node, initiallyAssignedToNodeIPs := range rc.InitiallyAssignedIPsByNode {
				for _, initiallyAssignedToNodeIP := range initiallyAssignedToNodeIPs {
					if assignedIP == initiallyAssignedToNodeIP {
						newAssignedIPsByNode[node] = append(newAssignedIPsByNode[node], assignedIP)
					}
				}
			}
		}
	}

	r.Log.V(1).Info("new", "assignedIPsByNode: ", newAssignedIPsByNode)

	// 6. get NodesByCIDR
	nodesByCIDR := rc.SelectedNodesByCIDR

	r.Log.V(1).Info("", "nodesByCIDR: ", nodesByCIDR)

	// 7. calculate NodesByNumberOfAssignedIPByCIDR
	nodesByNumberOfAssignedIPsByCIDR := map[string]map[int][]string{}
	for cidr := range nodesByCIDR {
		nodesByNumberOfAssignedIPsByCIDR[cidr] = map[int][]string{}
		for _, node := range nodesByCIDR[cidr] {
			nodesByNumberOfAssignedIPsByCIDR[cidr][len(newAssignedIPsByNode[node])] = append(nodesByNumberOfAssignedIPsByCIDR[cidr][len(newAssignedIPsByNode[node])], node)
		}
	}

	r.Log.V(1).Info("", "nodesByNumberOfAssignedIPsByCIDR: ", nodesByNumberOfAssignedIPsByCIDR)

	// 8. assign IPs to the least assigned nodes, update map, by CIDR
	for cidr, ips := range toBeAssignedToNodesIPsByCIDR {
		for _, ip := range ips {
			//pick the first node with the least IPs in this CIDR
			r.Log.V(1).Info("", "nodesByNumberOfAssignedIPsByCIDR: ", nodesByNumberOfAssignedIPsByCIDR)
			minIPsPerNode := getMinKey(nodesByNumberOfAssignedIPsByCIDR[cidr])
			if minIPsPerNode == -1 {
				err := errors.New("Unable to find nodes for CIDR" + cidr)
				r.Log.Error(err, "", cidr, "nodes", nodesByNumberOfAssignedIPsByCIDR[cidr])
				return map[string][]string{}, err
			}
			r.Log.V(1).Info("", "minIPsPerNode: ", minIPsPerNode, "for cidr", cidr)
			node := nodesByNumberOfAssignedIPsByCIDR[cidr][minIPsPerNode][0]
			r.Log.Info("assigning", "IP", ip, "to node", node)
			// add the node to the assignedIP per node map
			newAssignedIPsByNode[node] = append(newAssignedIPsByNode[node], ip)
			// remove the node from the minIPsPerNode map
			nodesByNumberOfAssignedIPsByCIDR[cidr][minIPsPerNode] = nodesByNumberOfAssignedIPsByCIDR[cidr][minIPsPerNode][1:]
			// add the node to the minIPsPerNode+1 map
			nodesByNumberOfAssignedIPsByCIDR[cidr][minIPsPerNode+1] = append(nodesByNumberOfAssignedIPsByCIDR[cidr][minIPsPerNode+1], node)
		}
	}

	return newAssignedIPsByNode, nil
}

func getMinKey(nodemap map[int][]string) int {
	numbers := []int{}
	for n, nodes := range nodemap {
		if len(nodes) > 0 {
			numbers = append(numbers, n)
		}
	}
	if len(numbers) == 0 {
		return -1
	}
	sort.Ints(numbers)
	return numbers[0]
}
