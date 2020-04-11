package egressipam

import (
	"context"
	"net"
	"reflect"

	ocpnetv1 "github.com/openshift/api/network/v1"
	redhatcopv1alpha1 "github.com/redhat-cop/egressip-ipam-operator/pkg/apis/redhatcop/v1alpha1"
	"github.com/scylladb/go-set/strset"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type enqueForSelectingEgressIPAMHostSubnet struct {
	r *ReconcileEgressIPAM
}

// return whether this EgressIPAM macthes this hostSubnet and with which CIDR
func matchesHostSubnet(egressIPAM *redhatcopv1alpha1.EgressIPAM, hostsubnet *ocpnetv1.HostSubnet) (bool, string) {
	for _, cIDRAssignment := range egressIPAM.Spec.CIDRAssignments {
		_, cidr, err := net.ParseCIDR(cIDRAssignment.CIDR)
		if err != nil {
			log.Error(err, "unable to parse ", "cidr", cidr)
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
		log.Info("unable convert event object to hostsubnet,", "event", evt)
		return
	}
	egressIPAMs, err := e.r.getAllEgressIPAM()
	if err != nil {
		log.Error(err, "unable to get all EgressIPAM resources")
		return
	}
	for _, egressIPAM := range egressIPAMs {
		if matches, _ := matchesHostSubnet(&egressIPAM, hostsubnet); matches {
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
		log.Info("unable convert event object to hostsubnet,", "event", evt)
		return
	}
	egressIPAMs, err := e.r.getAllEgressIPAM()
	if err != nil {
		log.Error(err, "unable to get all EgressIPAM resources")
		return
	}
	for _, egressIPAM := range egressIPAMs {
		if matches, _ := matchesHostSubnet(&egressIPAM, hostsubnet); matches {
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
		log.Info("unable convert event object to hostsubnet,", "event", evt)
		return
	}
	egressIPAMs, err := e.r.getAllEgressIPAM()
	if err != nil {
		log.Error(err, "unable to get all EgressIPAM resources")
		return
	}
	for _, egressIPAM := range egressIPAMs {
		if matches, _ := matchesHostSubnet(&egressIPAM, hostsubnet); matches {
			q.Add(reconcile.Request{NamespacedName: types.NamespacedName{
				Name: egressIPAM.GetName(),
			}})
		}
	}
}

// Generic implements EventHandler
func (e *enqueForSelectingEgressIPAMHostSubnet) Generic(evt event.GenericEvent, q workqueue.RateLimitingInterface) {
	return
}

// ensures that hostsubntes have the correct egressIPs
func (r *ReconcileEgressIPAM) reconcileHSAssignedIPs(rc *reconcileContext) error {
	for hostsubnetname, hostsubnet := range rc.selectedHostSubnets {
		if !strset.New(rc.finallyAssignedIPsByNode[hostsubnetname]...).IsEqual(strset.New(hostsubnet.EgressIPs...)) {
			hostsubnet.EgressIPs = rc.finallyAssignedIPsByNode[hostsubnetname]
			err := r.GetClient().Update(context.TODO(), &hostsubnet, &client.UpdateOptions{})
			if err != nil {
				log.Error(err, "unable to update", "hostsubnet ", hostsubnet, "with ips", rc.finallyAssignedIPsByNode[hostsubnetname])
				return err
			}
		}
	}
	return nil
}

// ensures that hostsubnets have the correct CIDR
func (r *ReconcileEgressIPAM) assignCIDRsToHostSubnets(rc *reconcileContext) error {
	for cidr, nodes := range rc.selectedNodesByCIDR {
		cidrs := []string{cidr}
		for _, node := range nodes {
			hostsubnet := rc.allHostSubnets[node]
			if !strset.New(hostsubnet.EgressCIDRs...).IsEqual(strset.New(cidrs...)) {
				hostsubnet.EgressCIDRs = cidrs
				err := r.GetClient().Update(context.TODO(), &hostsubnet, &client.UpdateOptions{})
				if err != nil {
					log.Error(err, "unable to update", "hostsubnet ", hostsubnet, "with cidrs", cidrs)
					return err
				}
			}
		}
	}
	return nil
}

func (r *ReconcileEgressIPAM) getAllHostSubnets(thiscontext *reconcileContext) (map[string]ocpnetv1.HostSubnet, error) {
	hostSubnetList := &ocpnetv1.HostSubnetList{}
	err := r.GetClient().List(context.TODO(), hostSubnetList, &client.ListOptions{})
	if err != nil {
		log.Error(err, "unable to list all hostsubnets")
		return map[string]ocpnetv1.HostSubnet{}, err
	}
	selectedHostSubnets := map[string]ocpnetv1.HostSubnet{}
	for _, hostsubnet := range hostSubnetList.Items {
		selectedHostSubnets[hostsubnet.GetName()] = hostsubnet
	}
	return selectedHostSubnets, nil
}

func (r *ReconcileEgressIPAM) removeHostsubnetAssignedIPsAndCIDRs(rc *reconcileContext) error {
	for _, hostsubnet := range rc.selectedHostSubnets {
		if !reflect.DeepEqual(hostsubnet.EgressCIDRs, []string{}) || !reflect.DeepEqual(hostsubnet.EgressIPs, []string{}) {
			hostsubnet.EgressCIDRs = []string{}
			hostsubnet.EgressIPs = []string{}
			err := r.GetClient().Update(context.TODO(), &hostsubnet, &client.UpdateOptions{})
			if err != nil {
				log.Error(err, "unable to upadate ", "hostsubnet", hostsubnet)
				return err
			}
		}
	}
	return nil
}

func getHostSubnetNames(hostSubnets map[string]ocpnetv1.HostSubnet) []string {
	hostSubnetNames := []string{}
	for _, hostSubnet := range hostSubnets {
		hostSubnetNames = append(hostSubnetNames, hostSubnet.GetName())
	}
	return hostSubnetNames
}
