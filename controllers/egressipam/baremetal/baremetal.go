package baremetal

import (
	"net"

	"github.com/go-logr/logr"
	"github.com/redhat-cop/egressip-ipam-operator/controllers/egressipam/reconcilecontext"
	ctrl "sigs.k8s.io/controller-runtime"
)

type BareMetalInfra struct {
	log logr.Logger
}

func NewBareMetalInfra() *BareMetalInfra {
	return &BareMetalInfra{
		log: ctrl.Log.WithName("BareMetalInfra"),
	}
}

//GetSelectedInstances returns a map of nodename and corresponding instance info
func (bm *BareMetalInfra) GetSelectedInstances(rc *reconcilecontext.ReconcileContext) (map[string]interface{}, error) {
	return map[string]interface{}{}, nil
}

//GetUsedIPsByCIDR returns a map of reserved IPs by CIDR, this IPs cannot be used for assigning to namespaces
func (bm *BareMetalInfra) GetUsedIPsByCIDR(rc *reconcilecontext.ReconcileContext) (map[string][]net.IP, error) {
	usedIPsByCIDR := map[string][]net.IP{}

	for _, cidr := range rc.CIDRs {
		usedIPsByCIDR[cidr] = []net.IP{}
	}
	return usedIPsByCIDR, nil
}

//ReconcileInstanceSecondaryIPs will make sure that Assigned Egress IPs to instances are correclty reconciled
//this includes adding and possibly removing secondary IPs to selected instances.
func (bm *BareMetalInfra) ReconcileInstanceSecondaryIPs(rc *reconcilecontext.ReconcileContext) error {
	return nil
}

// RemoveAllAssignedIPs uncoditionally remoevs all the assigned IPs to VMs, used in clean-up login
func (bm *BareMetalInfra) RemoveAllAssignedIPs(rc *reconcilecontext.ReconcileContext) error {
	return nil
}
