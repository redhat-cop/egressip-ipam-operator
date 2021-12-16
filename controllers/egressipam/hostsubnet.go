package egressipam

import (
	"net"
	"reflect"

	"github.com/go-logr/logr"
	multierror "github.com/hashicorp/go-multierror"
	ocpnetv1 "github.com/openshift/api/network/v1"
	redhatcopv1alpha1 "github.com/redhat-cop/egressip-ipam-operator/api/v1alpha1"
	"github.com/redhat-cop/egressip-ipam-operator/controllers/egressipam/reconcilecontext"
	"github.com/scylladb/go-set/strset"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type enqueForSelectingEgressIPAMHostSubnet struct {
	r   *EgressIPAMReconciler
	log logr.Logger
}

// return whether this EgressIPAM macthes this hostSubnet and with which CIDR
func (e *enqueForSelectingEgressIPAMHostSubnet) matchesHostSubnet(egressIPAM *redhatcopv1alpha1.EgressIPAM, hostsubnet *ocpnetv1.HostSubnet) (bool, string) {
	for _, cIDRAssignment := range egressIPAM.Spec.CIDRAssignments {
		_, cidr, err := net.ParseCIDR(cIDRAssignment.CIDR)
		if err != nil {
			e.log.Error(err, "unable to parse ", "cidr", cidr)
			return false, ""
		}
		if cidr.Contains(net.ParseIP(hostsubnet.HostIP)) {
			return true, cIDRAssignment.CIDR
		}
	}
	return false, ""
}

// trigger a egressIPAM reconcile event for those egressIPAM objects that reference this hostsubnet indireclty via the corresponding node.
func (e *enqueForSelectingEgressIPAMHostSubnet) Create(evt event.CreateEvent, q workqueue.RateLimitingInterface) {
	hostsubnet, ok := evt.Object.(*ocpnetv1.HostSubnet)
	if !ok {
		e.log.Info("unable convert event object to hostsubnet,", "event", evt)
		return
	}
	egressIPAMs, err := e.r.getAllEgressIPAM()
	if err != nil {
		e.log.Error(err, "unable to get all EgressIPAM resources")
		return
	}
	for _, egressIPAM := range egressIPAMs {
		if matches, _ := e.matchesHostSubnet(&egressIPAM, hostsubnet); matches {
			q.Add(reconcile.Request{NamespacedName: types.NamespacedName{
				Name: egressIPAM.GetName(),
			}})
		}
	}
}

// Update implements EventHandler
// trigger a router reconcile event for those routes that reference this secret
func (e *enqueForSelectingEgressIPAMHostSubnet) Update(evt event.UpdateEvent, q workqueue.RateLimitingInterface) {
	hostsubnet, ok := evt.ObjectNew.(*ocpnetv1.HostSubnet)
	if !ok {
		e.log.Info("unable convert event object to hostsubnet,", "event", evt)
		return
	}
	egressIPAMs, err := e.r.getAllEgressIPAM()
	if err != nil {
		e.log.Error(err, "unable to get all EgressIPAM resources")
		return
	}
	for _, egressIPAM := range egressIPAMs {
		if matches, _ := e.matchesHostSubnet(&egressIPAM, hostsubnet); matches {
			q.Add(reconcile.Request{NamespacedName: types.NamespacedName{
				Name: egressIPAM.GetName(),
			}})
		}
	}
}

// Delete implements EventHandler
func (e *enqueForSelectingEgressIPAMHostSubnet) Delete(evt event.DeleteEvent, q workqueue.RateLimitingInterface) {
	hostsubnet, ok := evt.Object.(*ocpnetv1.HostSubnet)
	if !ok {
		e.log.Info("unable convert event object to hostsubnet,", "event", evt)
		return
	}
	egressIPAMs, err := e.r.getAllEgressIPAM()
	if err != nil {
		e.log.Error(err, "unable to get all EgressIPAM resources")
		return
	}
	for _, egressIPAM := range egressIPAMs {
		if matches, _ := e.matchesHostSubnet(&egressIPAM, hostsubnet); matches {
			q.Add(reconcile.Request{NamespacedName: types.NamespacedName{
				Name: egressIPAM.GetName(),
			}})
		}
	}
}

// Generic implements EventHandler
func (e *enqueForSelectingEgressIPAMHostSubnet) Generic(evt event.GenericEvent, q workqueue.RateLimitingInterface) {
}

// ensures that hostsubntes have the correct egressIPs
func (r *EgressIPAMReconciler) reconcileHSAssignedIPs(rc *reconcilecontext.ReconcileContext) error {
	results := make(chan error)
	defer close(results)
	for hostsubnetname, hostsubnet := range rc.SelectedHostSubnets {
		hostsubnetnamec := hostsubnetname
		hostsubnetc := hostsubnet.DeepCopy()
		go func() {
			if !strset.New(rc.FinallyAssignedIPsByNode[hostsubnetnamec]...).IsEqual(strset.New(GetHostHostSubnetEgressIPsAsStrings(hostsubnetc.EgressIPs)...)) {
				hostsubnetc.EgressCIDRs = []ocpnetv1.HostSubnetEgressCIDR{}
				hostsubnetc.EgressIPs = GetHostHostSubnetEgressIPs(rc.FinallyAssignedIPsByNode[hostsubnetnamec])
				err := r.GetClient().Update(rc.Context, hostsubnetc, &client.UpdateOptions{})
				if err != nil {
					r.Log.Error(err, "unable to update", "hostsubnet ", hostsubnetc, "with ips", rc.FinallyAssignedIPsByNode[hostsubnetnamec])
					results <- err
					return
				}
			}
			results <- nil
		}()
	}
	result := &multierror.Error{}
	for range rc.SelectedHostSubnets {
		result = multierror.Append(result, <-results)
	}
	return result.ErrorOrNil()
}

// ensures that hostsubnets have the correct CIDR
func (r *EgressIPAMReconciler) assignCIDRsToHostSubnets(rc *reconcilecontext.ReconcileContext) error {
	for cidr, nodes := range rc.SelectedNodesByCIDR {
		cidrs := []string{cidr}
		for _, node := range nodes {
			hostsubnet := rc.AllHostSubnets[node]
			if !strset.New(GetHostSubnetCIDRsAsStrings(hostsubnet.EgressCIDRs)...).IsEqual(strset.New(cidrs...)) {
				hostsubnet.EgressCIDRs = GetHostSubnetCIDRs(cidrs)
				hostsubnet.EgressIPs = []ocpnetv1.HostSubnetEgressIP{}
				err := r.GetClient().Update(rc.Context, &hostsubnet, &client.UpdateOptions{})
				if err != nil {
					r.Log.Error(err, "unable to update", "hostsubnet ", hostsubnet, "with cidrs", cidrs)
					return err
				}
			}
		}
	}
	return nil
}

func GetHostSubnetCIDRsAsStrings(CIDRs []ocpnetv1.HostSubnetEgressCIDR) []string {
	sCIDRs := []string{}
	for _, cidr := range CIDRs {
		sCIDRs = append(sCIDRs, string(cidr))
	}
	return sCIDRs
}

func GetHostSubnetCIDRs(CIDRs []string) []ocpnetv1.HostSubnetEgressCIDR {
	hCIDRs := []ocpnetv1.HostSubnetEgressCIDR{}
	for _, cidr := range CIDRs {
		hCIDRs = append(hCIDRs, ocpnetv1.HostSubnetEgressCIDR(cidr))
	}
	return hCIDRs
}

func GetHostHostSubnetEgressIPsAsStrings(IPs []ocpnetv1.HostSubnetEgressIP) []string {
	sIPs := []string{}
	for _, ip := range IPs {
		sIPs = append(sIPs, string(ip))
	}
	return sIPs
}

func GetHostHostSubnetEgressIPs(IPs []string) []ocpnetv1.HostSubnetEgressIP {
	hIPs := []ocpnetv1.HostSubnetEgressIP{}
	for _, ip := range IPs {
		hIPs = append(hIPs, ocpnetv1.HostSubnetEgressIP(ip))
	}
	return hIPs
}

func (r *EgressIPAMReconciler) getAllHostSubnets(rc *reconcilecontext.ReconcileContext) (map[string]ocpnetv1.HostSubnet, error) {
	hostSubnetList := &ocpnetv1.HostSubnetList{}
	err := r.GetClient().List(rc.Context, hostSubnetList, &client.ListOptions{})
	if err != nil {
		r.Log.Error(err, "unable to list all hostsubnets")
		return map[string]ocpnetv1.HostSubnet{}, err
	}
	selectedHostSubnets := map[string]ocpnetv1.HostSubnet{}
	for _, hostsubnet := range hostSubnetList.Items {
		selectedHostSubnets[hostsubnet.GetName()] = hostsubnet
	}
	return selectedHostSubnets, nil
}

func (r *EgressIPAMReconciler) removeHostsubnetAssignedIPsAndCIDRs(rc *reconcilecontext.ReconcileContext) error {
	results := make(chan error)
	defer close(results)
	for _, hostsubnet := range rc.SelectedHostSubnets {
		hostsubnetc := hostsubnet.DeepCopy()
		go func() {
			if !reflect.DeepEqual(hostsubnetc.EgressCIDRs, []string{}) || !reflect.DeepEqual(hostsubnetc.EgressIPs, []string{}) {
				hostsubnetc.EgressCIDRs = []ocpnetv1.HostSubnetEgressCIDR{}
				hostsubnetc.EgressIPs = []ocpnetv1.HostSubnetEgressIP{}
				err := r.GetClient().Update(rc.Context, hostsubnetc, &client.UpdateOptions{})
				if err != nil {
					r.Log.Error(err, "unable to upadate ", "hostsubnet", hostsubnetc.GetName())
					results <- err
					return
				}
			}
			results <- nil
		}()
	}
	result := &multierror.Error{}
	for range rc.SelectedHostSubnets {
		result = multierror.Append(result, <-results)
	}
	return result.ErrorOrNil()
}

func getHostSubnetNames(hostSubnets map[string]ocpnetv1.HostSubnet) []string {
	hostSubnetNames := []string{}
	for _, hostSubnet := range hostSubnets {
		hostSubnetNames = append(hostSubnetNames, hostSubnet.GetName())
	}
	return hostSubnetNames
}
