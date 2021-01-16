package azure

import (
	"context"
	"errors"
	"net"
	"strings"

	"github.com/Azure/azure-sdk-for-go/profiles/latest/network/mgmt/network"
	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2020-06-30/compute"
	"github.com/Azure/go-autorest/autorest"
	"github.com/Azure/go-autorest/autorest/adal"
	"github.com/Azure/go-autorest/autorest/azure"
	"github.com/go-logr/logr"
	"github.com/hashicorp/go-multierror"
	"github.com/jpillora/ipmath"
	ocpconfigv1 "github.com/openshift/api/config/v1"
	cloudcredentialv1 "github.com/openshift/cloud-credential-operator/pkg/apis/cloudcredential/v1"
	"github.com/redhat-cop/egressip-ipam-operator/controllers/egressipam/reconcilecontext"
	"github.com/scylladb/go-set/strset"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// az network nic ip-config create \
//   --resource-group lab2-xj26h-rg \
//   --nic-name lab2-xj26h-worker-westeurope3-rwxsz-nic \
//   --name IPConfig-Egress \
//   --private-ip-address 10.44.9.70

const userAgent = "egressip-ipam-operator-services"

type AzureInfra struct {
	//direct ocp client (not chached)
	dc client.Client
	//aws client
	compute          *compute.VirtualMachinesClient
	vnet             *network.VirtualNetworksClient
	networkInterface *network.InterfacesClient
	log              logr.Logger
}

func NewAzureInfra(directClient client.Client, rc *reconcilecontext.ReconcileContext) (reconcilecontext.Infra, error) {
	azureInfra := &AzureInfra{
		log: ctrl.Log.WithName("AzureInfra"),
		dc:  directClient,
	}
	compute, vnet, networkInterface, err := azureInfra.getAzureClients(rc.Infrastructure, rc.CloudCredentialsSecret)
	if err != nil {
		azureInfra.log.Error(err, "unable to create aws client")
		return nil, err
	}
	azureInfra.compute = compute
	azureInfra.vnet = vnet
	azureInfra.networkInterface = networkInterface

	return azureInfra, nil
}

func (i *AzureInfra) getAzureClients(infrastructure *ocpconfigv1.Infrastructure, cloudCredentialsSecret *corev1.Secret) (*compute.VirtualMachinesClient, *network.VirtualNetworksClient, *network.InterfacesClient, error) {
	clientID, clientSecret, tenantID, subscription, err := i.getAzureCredentials(cloudCredentialsSecret)
	if err != nil {
		i.log.Error(err, "unable to get azure credentials")
		return nil, nil, nil, err
	}
	authorizer, err := getAuthorizer(clientID, clientSecret, tenantID)
	if err != nil {
		i.log.Error(err, "unable to get azure authorizer")
		return nil, nil, nil, err
	}
	vmClient := compute.NewVirtualMachinesClient(subscription)
	vmClient.Authorizer = authorizer
	vmClient.AddToUserAgent(userAgent)
	vnetClient := network.NewVirtualNetworksClient(subscription)
	vnetClient.Authorizer = authorizer
	vnetClient.AddToUserAgent(userAgent)
	networkClient := network.NewInterfacesClient(subscription)
	networkClient.Authorizer = authorizer
	networkClient.AddToUserAgent(userAgent)
	return &vmClient, &vnetClient, &networkClient, nil
}

func (i *AzureInfra) getAzureCredentials(cloudCredentialsSecret *corev1.Secret) (clientID string, clientSecret string, tenantID string, subscription string, err error) {

	clientIDb, ok := cloudCredentialsSecret.Data["azure_client_id"]
	if !ok {
		err := errors.New("unable to find key azure_client_id in secret " + cloudCredentialsSecret.String())
		i.log.Error(err, "")
		return "", "", "", "", err
	}

	clientSecretb, ok := cloudCredentialsSecret.Data["azure_client_secret"]
	if !ok {
		err := errors.New("unable to find key azure_client_secret in secret " + cloudCredentialsSecret.String())
		i.log.Error(err, "")
		return "", "", "", "", err
	}

	tenantIDb, ok := cloudCredentialsSecret.Data["azure_tenant_id"]
	if !ok {
		err := errors.New("unable to find key azure_tenant_id in secret " + cloudCredentialsSecret.String())
		i.log.Error(err, "")
		return "", "", "", "", err
	}

	subscriptionb, ok := cloudCredentialsSecret.Data["azure_subscription_id"]
	if !ok {
		err := errors.New("unable to find key azure_subscription_id in secret " + cloudCredentialsSecret.String())
		i.log.Error(err, "")
		return "", "", "", "", err
	}

	return string(clientIDb), string(clientSecretb), string(tenantIDb), string(subscriptionb), nil
}

func getAuthorizer(clientID string, clientSecret string, tenantID string) (autorest.Authorizer, error) {
	oauthConfig, err := adal.NewOAuthConfig(azure.PublicCloud.ActiveDirectoryEndpoint, tenantID)
	if err != nil {
		return nil, err
	}
	spToken, err := adal.NewServicePrincipalToken(*oauthConfig, clientID, clientSecret, azure.PublicCloud.ResourceManagerEndpoint)
	if err != nil {
		return nil, err
	}
	return autorest.NewBearerAuthorizer(spToken), nil
}

//GetSelectedInstances returns a map of nodename and corresponding instance info
func (i *AzureInfra) GetSelectedInstances(rc *reconcilecontext.ReconcileContext) (map[string]interface{}, error) {
	instanceMap, err := i.getAzureInstances(rc.Context, rc.Infrastructure.Status.PlatformStatus.Azure.ResourceGroupName, rc.SelectedNodes)
	if err != nil {
		i.log.Error(err, "unable to retrieve azure istances for selected nodes")
		return map[string]interface{}{}, err
	}
	selectedInstances := map[string]interface{}{}
	for name, AzureInstance := range instanceMap {
		selectedInstances[name] = AzureInstance
	}
	return selectedInstances, nil
}

func (i *AzureInfra) getAzureInstances(context context.Context, resourceGroupName string, nodes map[string]corev1.Node) (map[string]*compute.VirtualMachine, error) {
	result, err := i.compute.ListComplete(context, resourceGroupName)

	if err != nil {
		i.log.Error(err, "unable to get azure instances")
		return map[string]*compute.VirtualMachine{}, err
	}

	instanceList := map[string]*compute.VirtualMachine{}

	for result.NotDone() {
		VM := result.Value()
		for nodeName := range nodes {
			if nodeName == *VM.Name {
				instanceList[nodeName] = &VM
				break
			}
		}
		err := result.NextWithContext(context)
		if err != nil {
			i.log.Error(err, "unable to get next azure instance from instance iterator")
			return map[string]*compute.VirtualMachine{}, err
		}
	}

	return instanceList, nil
}

//GetUsedIPsByCIDR returns a map of reserved IPs by CIDR, this IPs cannot be used for assigning to namespaces
func (i *AzureInfra) GetUsedIPsByCIDR(rc *reconcilecontext.ReconcileContext) (map[string][]net.IP, error) {
	IPsByCIDR, err := i.getAzureUsedIPsByCIDR(rc)
	if err != nil {
		i.log.Error(err, "unable to get used IPs by CIDR")
		return map[string][]net.IP{}, err
	}
	//adding always taken IPs per Azure rules
	for cidr := range IPsByCIDR {
		base, _, err := net.ParseCIDR(cidr)
		if err != nil {
			i.log.Error(err, "unable to parse", "cidr", cidr)
			return map[string][]net.IP{}, err
		}
		IPsByCIDR[cidr] = append(IPsByCIDR[cidr], ipmath.DeltaIP(base, 1), ipmath.DeltaIP(base, 2), ipmath.DeltaIP(base, 3))
	}
	return IPsByCIDR, nil
}

func (i *AzureInfra) getAzureUsedIPsByCIDR(rc *reconcilecontext.ReconcileContext) (map[string][]net.IP, error) {
	//get network
	result, err := i.vnet.Get(rc.Context, rc.Infrastructure.Status.PlatformStatus.Azure.NetworkResourceGroupName, rc.Infrastructure.Status.InfrastructureName+"-vnet", "subnets/ipConfigurations")
	if err != nil {
		i.log.Error(err, "unable to get", "virtual network", rc.Infrastructure.Status.InfrastructureName+"-vnet")
		return map[string][]net.IP{}, err
	}

	usedIPsByCIDR := map[string][]net.IP{}
	for _, cidr := range rc.CIDRs {
		usedIPsByCIDR[cidr] = []net.IP{}
		_, CIDR, err := net.ParseCIDR(cidr)
		if err != nil {
			i.log.Error(err, "unable to parse ", "CIDR", cidr)
			return map[string][]net.IP{}, err
		}
		//get subnets
		for _, subnet := range *result.Subnets {
			for _, ipConfiguration := range *subnet.IPConfigurations {
				IP := net.ParseIP(*ipConfiguration.PrivateIPAddress)
				if IP == nil {
					i.log.Error(err, "unable to parse ", "IP", *ipConfiguration.PrivateIPAddress)
					return map[string][]net.IP{}, err
				}
				if CIDR.Contains(IP) {
					usedIPsByCIDR[cidr] = append(usedIPsByCIDR[cidr], IP)
				}
			}
		}
	}
	return usedIPsByCIDR, nil
}

//ReconcileInstanceSecondaryIPs will make sure that Assigned Egress IPs to instances are correclty reconciled
//this includes adding and possibly removing secondary IPs to selected instances.
func (i *AzureInfra) ReconcileInstanceSecondaryIPs(rc *reconcilecontext.ReconcileContext) error {
	err := i.reconcileAzureAssignedIPs(rc)
	if err != nil {
		i.log.Error(err, "unable to assign egress IPs to Azure VMs")
		return err
	}
	return nil
}

// RemoveAllAssignedIPs uncoditionally remoevs all the assigned IPs to VMs, used in clean-up login
func (i *AzureInfra) RemoveAllAssignedIPs(rc *reconcilecontext.ReconcileContext) error {
	return i.removeAllAzureSecondaryIPs(rc)
}

// removes Azure secondary IPs that are currently assigned but not needed
func (i *AzureInfra) removeAllAzureSecondaryIPs(rc *reconcilecontext.ReconcileContext) error {
	results := make(chan error)
	defer close(results)
	for node := range rc.SelectedNodes {
		nodec := node
		go func() {
			instance, ok := rc.SelectedInstances[nodec].(*compute.VirtualMachine)
			if !ok {
				err := errors.New("type assertion failed")
				i.log.Error(err, "*compute.VirtualMachine type assertion failed")
				results <- err
				return
			}
			networkInterface := network.Interface{}
			for _, netif := range *instance.NetworkProfile.NetworkInterfaces {
				if *netif.Primary {
					//load network interface
					var err error
					networkInterface, err = i.networkInterface.Get(rc.Context, rc.Infrastructure.Status.PlatformStatus.Azure.NetworkResourceGroupName, getNameFromResourceID(*netif.ID), "")
					if err != nil {
						i.log.Error(err, "unable to get", "network interface", netif)
						results <- err
						return
					}
				}
			}
			ipConfigurations := []network.InterfaceIPConfiguration{}
			for _, ipConfiguration := range *networkInterface.IPConfigurations {
				if *ipConfiguration.Primary {
					ipConfigurations = append(ipConfigurations, ipConfiguration)
				}
			}
			networkInterface.IPConfigurations = &ipConfigurations
			result, err := i.networkInterface.CreateOrUpdate(rc.Context, rc.Infrastructure.Status.PlatformStatus.Azure.NetworkResourceGroupName, *networkInterface.Name, networkInterface)
			if err != nil {
				i.log.Error(err, "unable to update", "network interface", networkInterface.Name)
				results <- err
				return
			}
			err = result.WaitForCompletionRef(rc.Context, i.networkInterface.Client)
			if err != nil {
				i.log.Error(err, "unable to update", "network interface", networkInterface.Name)
				results <- err
				return
			}
			results <- nil
			return
		}()
	}
	result := &multierror.Error{}
	for range rc.SelectedNodes {
		multierror.Append(result, <-results)
	}

	return result.ErrorOrNil()
}

// assigns secondary IPs to Azure machines
func (i *AzureInfra) reconcileAzureAssignedIPs(rc *reconcilecontext.ReconcileContext) error {
	results := make(chan error)
	defer close(results)
	for node, ips := range rc.FinallyAssignedIPsByNode {
		nodec := node
		ipsc := ips
		go func() {
			instance, ok := rc.SelectedInstances[nodec].(*compute.VirtualMachine)
			if !ok {
				err := errors.New("type assertion failed")
				i.log.Error(err, "*compute.VirtualMachine type assertion failed")
				results <- err
				return
			}
			azureAssignedIPs := []string{}
			networkInterface := network.Interface{}
			for _, netif := range *instance.NetworkProfile.NetworkInterfaces {
				if *netif.Primary {
					//load network interface
					var err error
					networkInterface, err = i.networkInterface.Get(rc.Context, rc.Infrastructure.Status.PlatformStatus.Azure.NetworkResourceGroupName, getNameFromResourceID(*netif.ID), "")
					if err != nil {
						i.log.Error(err, "unable to get", "network interface", netif)
						results <- err
						return
					}
					//exclude first IP, add secondary IPs
					for _, ipConfiguration := range *networkInterface.IPConfigurations {
						if !*ipConfiguration.Primary {
							azureAssignedIPs = append(azureAssignedIPs, *ipConfiguration.PrivateIPAddress)
						}
					}
				}
			}
			toBeRemovedIPs := strset.Difference(strset.New(azureAssignedIPs...), strset.New(ipsc...)).List()
			toBeAssignedIPs := strset.Difference(strset.New(ipsc...), strset.New(azureAssignedIPs...)).List()
			//remove unneeded ips
			ipConfigurations := []network.InterfaceIPConfiguration{}
			i.log.Info("vm", "instance ", instance.Name, " will be freed from IPs ", toBeRemovedIPs)
			for _, ipConfiguration := range *networkInterface.IPConfigurations {
				found := false
				for i := range toBeRemovedIPs {
					if toBeRemovedIPs[i] == *ipConfiguration.PrivateIPAddress {
						found = true
					}
				}
				if !found {
					ipConfigurations = append(ipConfigurations, ipConfiguration)
				}
			}

			//add needed IPs
			if len(toBeAssignedIPs) > 0 {
				i.log.Info("vm", "instance ", instance.Name, " will be freed from IPs ", toBeRemovedIPs)
				for _, ip := range toBeAssignedIPs {
					name := "EgressIP" + ip
					untrue := false
					newIPConfiguration := network.InterfaceIPConfiguration{
						Name: &name,
						InterfaceIPConfigurationPropertiesFormat: &network.InterfaceIPConfigurationPropertiesFormat{
							PrivateIPAddress:                &ip,
							PrivateIPAllocationMethod:       network.Static,
							Subnet:                          (*networkInterface.IPConfigurations)[0].Subnet,
							Primary:                         &untrue,
							LoadBalancerBackendAddressPools: (*networkInterface.IPConfigurations)[0].LoadBalancerBackendAddressPools,
						},
					}
					ipConfigurations = append(ipConfigurations, newIPConfiguration)
				}
			}
			networkInterface.IPConfigurations = &ipConfigurations
			result, err := i.networkInterface.CreateOrUpdate(rc.Context, rc.Infrastructure.Status.PlatformStatus.Azure.NetworkResourceGroupName, *networkInterface.Name, networkInterface)
			if err != nil {
				i.log.Error(err, "unable to update", "network interface", networkInterface.Name)
				results <- err
				return
			}
			err = result.WaitForCompletionRef(rc.Context, i.networkInterface.Client)
			if err != nil {
				i.log.Error(err, "unable to update", "network interface", networkInterface.Name)
				results <- err
				return
			}

			results <- nil
			return
		}()
	}
	result := &multierror.Error{}
	for range rc.FinallyAssignedIPsByNode {
		multierror.Append(result, <-results)
	}

	return result.ErrorOrNil()
}

func GetAzureCredentialsRequestProviderSpec() *cloudcredentialv1.AzureProviderSpec {
	return &cloudcredentialv1.AzureProviderSpec{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "cloudcredential.openshift.io/v1",
			Kind:       "AzureProviderSpec",
		},
		RoleBindings: []cloudcredentialv1.RoleBinding{
			// {
			// 	Role: "Network Contributor",
			// },
			// {
			// 	Role: "Virtual Machine Contributor",
			// },
			{
				Role: "Contributor",
			},
		},
	}
}

func getNameFromResourceID(id string) string {
	return id[strings.LastIndex(id, "/"):]
}
