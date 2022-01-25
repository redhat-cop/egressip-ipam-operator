package reconcilecontext

import (
	"context"
	"net"

	ocpconfigv1 "github.com/openshift/api/config/v1"
	ocpnetv1 "github.com/openshift/api/network/v1"
	redhatcopv1alpha1 "github.com/redhat-cop/egressip-ipam-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

//ReconcileContext is a data structure represts all the known information about the current reconcile cycle.
type ReconcileContext struct {
	//immutable fields within a reconciliation cycle
	Context                     context.Context                  `json:"context,omitempty"`
	Infrastructure              *ocpconfigv1.Infrastructure      `json:"infrastructure,omitempty"`
	CloudCredentialsSecret      *corev1.Secret                   `json:"cloudCredentialsSecret,omitempty"`
	EgressIPAM                  *redhatcopv1alpha1.EgressIPAM    `json:"egressIPAM,omitempty"`
	CIDRs                       []string                         `json:"cIDRs,omitempty"`
	NetCIDRByCIDR               map[string]*net.IPNet            `json:"netCIDRByCIDR,omitempty"`
	CIDRsByLabel                map[string]string                `json:"cIDRsByLabel,omitempty"`
	ReservedIPsByCIDR           map[string][]net.IP              `json:"reservedIPsByCIDR,omitempty"`
	AllNodes                    map[string]corev1.Node           `json:"allNodes,omitempty"`
	AllHostSubnets              map[string]ocpnetv1.HostSubnet   `json:"allHostSubnets,omitempty"`
	SelectedNodes               map[string]corev1.Node           `json:"selectedNodes,omitempty"`
	SelectedNodesByCIDR         map[string][]string              `json:"selectedNodesByCIDR,omitempty"`
	SelectedHostSubnets         map[string]ocpnetv1.HostSubnet   `json:"selectedHostSubnets,omitempty"`
	SelectedHostSubnetsByCIDR   map[string][]string              `json:"selectedHostSubnetsByCIDR,omitempty"`
	ReferringNamespaces         map[string]corev1.Namespace      `json:"referringNamespaces,omitempty"`
	InitiallyAssignedNamespaces []corev1.Namespace               `json:"initiallyAssignedNamespaces,omitempty"`
	UnAssignedNamespaces        []corev1.Namespace               `json:"unAssignedNamespaces,omitempty"`
	NetNamespaces               map[string]ocpnetv1.NetNamespace `json:"netNamespaces,omitempty"`
	InitiallyAssignedIPsByNode  map[string][]string              `json:"initiallyAssignedIPsByNode,omitempty"`

	Infra Infra `json:"infra,omitempty"`

	UsedIPsByCIDR map[string][]net.IP `json:"usedIPsByCIDR,omitempty"`

	//variable fields
	FinallyAssignedNamespaces []corev1.Namespace  `json:"finallyAssignedNamespaces,omitempty"`
	FinallyAssignedIPsByNode  map[string][]string `json:"finallyAssignedIPsByNode,omitempty"`
}

type NodeCapacity interface {
}

//Infra abstracts away infrastructure related concerns
type Infra interface {
	//GetUsedIPsByCIDR returns a map of reserved IPs by CIDR, this IPs cannot be used for assigning to namespaces
	GetUsedIPsByCIDR(rc *ReconcileContext) (map[string][]net.IP, error)
	//ReconcileInstanceSecondaryIPs will make sure that Assigned Egress IPs to instances are correclty reconciled
	//this includes adding and possibly removing secondary IPs to selected instances.
	ReconcileInstanceSecondaryIPs(rc *ReconcileContext) error

	// RemoveAllAssignedIPs uncoditionally remoevs all the assigned IPs to VMs, used in clean-up login
	RemoveAllAssignedIPs(rc *ReconcileContext) error

	// GetCapacity return the ip capacity of the node (this includes the primary IP)
	GetIPCapacity(node *corev1.Node) (uint32, error)
}
