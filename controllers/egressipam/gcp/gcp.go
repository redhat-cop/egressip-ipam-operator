// +kubebuilder:skip
package gcp

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"github.com/go-logr/logr"
	"github.com/hashicorp/go-multierror"
	"github.com/jpillora/ipmath"
	ocpconfigv1 "github.com/openshift/api/config/v1"
	cloudcredentialv1 "github.com/openshift/cloud-credential-operator/pkg/apis/cloudcredential/v1"
	"github.com/redhat-cop/egressip-ipam-operator/controllers/egressipam/reconcilecontext"
	"github.com/scylladb/go-set/strset"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/option"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type GCPInfra struct {
	//direct ocp client (not cached)
	dc client.Client
	//gcp client
	computeClient *compute.Service
	// google project
	project           string
	log               logr.Logger
	selectedInstances map[string]*compute.Instance
}

var _ reconcilecontext.Infra = &GCPInfra{}

func NewGCPInfra(directClient client.Client, rc *reconcilecontext.ReconcileContext) (reconcilecontext.Infra, error) {
	gCPInfra := &GCPInfra{
		log: ctrl.Log.WithName("GCPInfra"),
		dc:  directClient,
	}
	c, err := gCPInfra.getGCPClient(rc.Infrastructure, rc.CloudCredentialsSecret)
	if err != nil {
		gCPInfra.log.Error(err, "unable to create aws client")
		return nil, err
	}
	gCPInfra.computeClient = c
	gCPInfra.project = rc.Infrastructure.Status.PlatformStatus.GCP.ProjectID
	selectedInstances, err := gCPInfra.getGCPInstances(rc.Context, rc.SelectedNodes)
	if err != nil {
		gCPInfra.log.Error(err, "unable to retrieve selected instances")
		return nil, err
	}
	gCPInfra.selectedInstances = selectedInstances
	return gCPInfra, nil
}

func (i *GCPInfra) getGCPInstances(context context.Context, nodes map[string]corev1.Node) (map[string]*compute.Instance, error) {
	//enumerate zones
	zoneSet := strset.New()
	for _, node := range nodes {
		if zone, ok := node.Labels["topology.kubernetes.io/zone"]; ok {
			zoneSet.Add(zone)
		}
	}
	//get instances
	instanceMap := map[string]*compute.Instance{}
	for _, zone := range zoneSet.List() {
		zonec := zone
		err := i.computeClient.Instances.List(i.project, zonec).Pages(context, func(il *compute.InstanceList) error {
			for _, instance := range il.Items {
				instanceMap[instance.Name] = instance
			}
			return nil
		})
		if err != nil {
			i.log.Error(err, "unable to list instance for", "zone", zonec)
			return map[string]*compute.Instance{}, err
		}
	}
	//filter instances
	filteredMap := map[string]*compute.Instance{}
	for _, node := range nodes {
		if instance, ok := instanceMap[i.getGCPIDFromNode(node)]; ok {
			filteredMap[instance.Name] = instance
		}
	}
	return filteredMap, nil
}

func (i *GCPInfra) getGCPClient(infrastructure *ocpconfigv1.Infrastructure, cloudCredentialsSecret *corev1.Secret) (*compute.Service, error) {
	jsonSecret, ok := cloudCredentialsSecret.Data["service_account.json"]
	if !ok {
		err := errors.New("unable to find key service_account.json in secret " + cloudCredentialsSecret.String())
		i.log.Error(err, "")
		return &compute.Service{}, err
	}
	creds, err := google.CredentialsFromJSON(context.TODO(), []byte(jsonSecret), secretmanager.DefaultAuthScopes()...)
	if err != nil {
		i.log.Error(err, "unable to create gcp creds from json")
		return &compute.Service{}, err
	}
	computeClient, err := compute.NewService(context.TODO(), option.WithCredentials(creds))
	if err != nil {
		i.log.Error(err, "unable to create compute client")
		return &compute.Service{}, err
	}
	return computeClient, nil
}

//GetUsedIPsByCIDR returns a map of reserved IPs by CIDR, this IPs cannot be used for assigning to namespaces
func (i *GCPInfra) GetUsedIPsByCIDR(rc *reconcilecontext.ReconcileContext) (map[string][]net.IP, error) {
	// find the subnet for each cidr
	// for each subnet, find the machines and the internal addresses
	IPsByCIDR, err := i.getGoogleUsedIPsByCIDR(rc)
	if err != nil {
		i.log.Error(err, "unable to get used IPs by CIDR")
		return map[string][]net.IP{}, nil
	}
	//adding always taken IPs per Google rules
	for cidr := range IPsByCIDR {
		base, cidrt, err := net.ParseCIDR(cidr)
		if err != nil {
			i.log.Error(err, "unable to parse", "cidr", cidr)
			return map[string][]net.IP{}, nil
		}
		broadcastip := ipmath.FromUInt32((^binary.BigEndian.Uint32([]byte(cidrt.Mask))) | binary.BigEndian.Uint32([]byte(base.To4())))
		IPsByCIDR[cidr] = append(IPsByCIDR[cidr], ipmath.DeltaIP(base, 1), ipmath.DeltaIP(broadcastip, -1))
	}
	return IPsByCIDR, nil
}

func (i *GCPInfra) getGoogleUsedIPsByCIDR(rc *reconcilecontext.ReconcileContext) (map[string][]net.IP, error) {
	usedIPsByCIDR := map[string][]net.IP{}
	addressList, err := i.computeClient.Addresses.List(i.project, rc.Infrastructure.Status.PlatformStatus.GCP.Region).Context(rc.Context).Do()
	if err != nil {
		i.log.Error(err, "unable to list addresses")
		return map[string][]net.IP{}, err
	}
	for _, cidrstr := range rc.CIDRs {
		usedIPsByCIDR[cidrstr] = []net.IP{}
		_, cidr, err := net.ParseCIDR(cidrstr)
		if err != nil {
			i.log.Error(err, "unable to convert to cidr ", "string", cidrstr)
			return map[string][]net.IP{}, err
		}
		for _, address := range addressList.Items {
			if address.Region == rc.Infrastructure.Status.PlatformStatus.GCP.Region && address.AddressType == "INTERNAL" && address.IpVersion == "IPV4" && cidr.Contains(net.ParseIP(address.Address)) {
				usedIPsByCIDR[cidrstr] = append(usedIPsByCIDR[cidrstr], net.ParseIP(address.Address))
			}
		}
	}
	return usedIPsByCIDR, nil
}

//ReconcileInstanceSecondaryIPs will make sure that Assigned Egress IPs to instances are correclty reconciled
//this includes adding and possibly removing secondary IPs to selected instances.
func (i *GCPInfra) ReconcileInstanceSecondaryIPs(rc *reconcilecontext.ReconcileContext) error {
	err := i.removeGCPUnusedIPs(rc)
	if err != nil {
		i.log.Error(err, "unable to remove assigned IPs to gcp machines")
		return err
	}

	err = i.reconcileGCPAssignedIPs(rc)
	if err != nil {
		i.log.Error(err, "unable to assign egress IPs to gcp machines")
		return err
	}
	return nil
}

func (i *GCPInfra) getGCPIDFromNode(node corev1.Node) string {
	return i.getResourceShortName(node.Spec.ProviderID)
}

func (i *GCPInfra) removeGCPUnusedIPs(rc *reconcilecontext.ReconcileContext) error {
	results := make(chan error)
	defer close(results)
	for node, ips := range rc.FinallyAssignedIPsByNode {
		nodec := node
		ipsc := ips
		go func() {
			instance := i.selectedInstances[i.getGCPIDFromNode(rc.AllNodes[nodec])]
			gcpAssignedIPs := []string{}
			for _, aliasIpRange := range instance.NetworkInterfaces[0].AliasIpRanges {
				//we assume that we are the only operator adding secondary ip and we always add individual IPs not ranges
				ip, cidr, err := net.ParseCIDR(aliasIpRange.IpCidrRange)
				if err != nil {
					i.log.Error(err, "unable to convert to cidr ", "string", aliasIpRange.IpCidrRange)
					results <- err
					return
				}
				if ipmath.NetworkSize(cidr) != uint32(0) {
					err := errors.New("unexepected network size for alias CIDR, expected 0, found: " + fmt.Sprintf("%d", ipmath.NetworkSize(cidr)))
					results <- err
					return
				}
				gcpAssignedIPs = append(gcpAssignedIPs, ip.String())
			}
			toBeMaintained := strset.Intersection(strset.New(gcpAssignedIPs...), strset.New(ipsc...)).List()
			toBeRemoved := strset.Difference(strset.New(gcpAssignedIPs...), strset.New(ipsc...)).List()
			if len(toBeMaintained) != len(gcpAssignedIPs) {
				i.log.Info("vm", "instance ", instance.Name, " will be freed from IPs ", toBeRemoved)
				//get latest instance fingerprint
				latestInstance, err := i.computeClient.Instances.Get(i.project, i.getResourceShortName(instance.Zone), instance.Name).Context(rc.Context).Do()
				if err != nil {
					i.log.Error(err, "unable to get ", "instance", instance.Name)
					results <- err
					return
				}
				ipRanges := []*compute.AliasIpRange{}
				for _, ip := range toBeMaintained {
					ipRanges = append(ipRanges, &compute.AliasIpRange{
						IpCidrRange: ip + "/32",
					})
				}
				_, err = i.computeClient.Instances.UpdateNetworkInterface(i.project, i.getResourceShortName(instance.Zone), instance.Name, instance.NetworkInterfaces[0].Name, &compute.NetworkInterface{
					AliasIpRanges: ipRanges,
					Fingerprint:   latestInstance.NetworkInterfaces[0].Fingerprint,
				}).Context(rc.Context).Do()
				if err != nil {
					i.log.Error(err, "unable to remove IPs from ", "instance", instance.Name)
					results <- err
					return
				}
			}
			results <- nil
		}()
	}
	result := &multierror.Error{}
	for range rc.FinallyAssignedIPsByNode {
		result = multierror.Append(result, <-results)
	}
	return result.ErrorOrNil()
}

func (i *GCPInfra) getResourceShortName(longName string) string {
	return strings.Split(longName, "/")[len(strings.Split(longName, "/"))-1]
}

func (i *GCPInfra) reconcileGCPAssignedIPs(rc *reconcilecontext.ReconcileContext) error {
	results := make(chan error)
	defer close(results)
	for node, ips := range rc.FinallyAssignedIPsByNode {
		nodec := node
		ipsc := ips
		go func() {
			instance := i.selectedInstances[i.getGCPIDFromNode(rc.AllNodes[nodec])]
			gcpAssignedIPs := []string{}
			for _, aliasIpRange := range instance.NetworkInterfaces[0].AliasIpRanges {
				//we assume that we are the only operator adding secondary ip and we always add individual IPs not ranges
				ip, cidr, err := net.ParseCIDR(aliasIpRange.IpCidrRange)
				if err != nil {
					i.log.Error(err, "unable to convert to cidr ", "string", aliasIpRange.IpCidrRange)
					results <- err
					return
				}
				if ipmath.NetworkSize(cidr) != uint32(0) {
					err := errors.New("unexepected network size for alias CIDR, expected 0, found: " + fmt.Sprintf("%d", ipmath.NetworkSize(cidr)))
					results <- err
					return
				}
				gcpAssignedIPs = append(gcpAssignedIPs, ip.String())
			}
			toBeAdded := strset.Difference(strset.New(ipsc...), strset.New(gcpAssignedIPs...)).List()
			if len(toBeAdded) > 0 {
				i.log.Info("vm", "instance ", instance.Name, " will be added IPs ", toBeAdded)
				//get latest instance fingerprint
				latestInstance, err := i.computeClient.Instances.Get(i.project, i.getResourceShortName(instance.Zone), instance.Name).Context(rc.Context).Do()
				if err != nil {
					i.log.Error(err, "unable to get ", "instance", instance.Name)
					results <- err
					return
				}
				ipRanges := []*compute.AliasIpRange{}
				for _, ip := range ipsc {
					ipRanges = append(ipRanges, &compute.AliasIpRange{
						IpCidrRange: ip + "/32",
					})
				}
				_, err = i.computeClient.Instances.UpdateNetworkInterface(i.project, i.getResourceShortName(instance.Zone), instance.Name, instance.NetworkInterfaces[0].Name, &compute.NetworkInterface{
					AliasIpRanges: ipRanges,
					Fingerprint:   latestInstance.NetworkInterfaces[0].Fingerprint,
				}).Context(rc.Context).Do()
				if err != nil {
					i.log.Error(err, "unable to add IPs from ", "instance", instance.Name)
					results <- err
					return
				}
			}
			results <- nil
		}()
	}
	result := &multierror.Error{}
	for range rc.FinallyAssignedIPsByNode {
		result = multierror.Append(result, <-results)
	}
	return result.ErrorOrNil()
}

func (i *GCPInfra) removeAllGCPAssignedIPs(rc *reconcilecontext.ReconcileContext) error {
	results := make(chan error)
	defer close(results)
	for _, node := range rc.SelectedNodes {
		nodec := node.DeepCopy()
		go func() {
			instance, ok := i.selectedInstances[i.getGCPIDFromNode(*nodec)]
			if !ok {
				err := errors.New("instance not found")
				i.log.Error(err, "istance not found for", "node", nodec.Name)
				results <- err
				return
			}
			_, err := i.computeClient.Instances.UpdateNetworkInterface(i.project, instance.Zone, instance.Name, instance.NetworkInterfaces[0].Name, &compute.NetworkInterface{
				AliasIpRanges: []*compute.AliasIpRange{},
				Fingerprint:   instance.Fingerprint,
			}).Context(rc.Context).Do()
			if err != nil {
				i.log.Error(err, "unable to remove all IPs from ", "instance", instance.Name)
				results <- err
				return
			}
			results <- nil
		}()
	}
	result := &multierror.Error{}
	for range rc.SelectedNodes {
		result = multierror.Append(result, <-results)
	}
	return result.ErrorOrNil()
}

// RemoveAllAssignedIPs uncoditionally removes all the assigned IPs to VMs, used in clean-up login
func (i *GCPInfra) RemoveAllAssignedIPs(rc *reconcilecontext.ReconcileContext) error {
	return i.removeAllGCPAssignedIPs(rc)
}

func GetGCPCredentialsRequestProviderSpec() *cloudcredentialv1.GCPProviderSpec {
	return &cloudcredentialv1.GCPProviderSpec{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "cloudcredential.openshift.io/v1",
			Kind:       "GCPProviderSpec",
		},
		PredefinedRoles: []string{
			"roles/compute.instanceAdmin.v1",
		},
	}
}
