package egressipam

import (
	"errors"
	"net"

	redhatcopv1alpha1 "github.com/redhat-cop/egressip-ipam-operator/pkg/apis/redhatcop/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

// returns nodes selected by this egressIPAM sorted by the CIDR
func (r *ReconcileEgressIPAM) getAWSAssignedIPsByNode(nodesByCIDR map[*net.IPNet][]corev1.Node, egressIPAM *redhatcopv1alpha1.EgressIPAM) (map[*corev1.Node][]net.IP, error) {
	//the CIDR and egressIPAM can be used to lookup the AZ.
	return map[*corev1.Node][]net.IP{}, errors.New("not implemented")
}

// removes AWS secondary IPs that are currently assigned but not needed
func (r *ReconcileEgressIPAM) removeAWSUnusedIPs(awsAssignedIPsByNode map[*corev1.Node][]net.IP, assignedIPsByCIDR map[*net.IPNet][]net.IP) error {
	return errors.New("not implemented")
}

// assigns secondary IPs to AWS machines
func (r *ReconcileEgressIPAM) reconcileAWSAssignedIPs(assignedIPsByNode map[*corev1.Node][]net.IP, egressIPAM *redhatcopv1alpha1.EgressIPAM) error {
	return errors.New("not implemented")
}
