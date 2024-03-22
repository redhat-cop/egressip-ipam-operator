package aws

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/go-logr/logr"
	multierror "github.com/hashicorp/go-multierror"
	"github.com/jpillora/ipmath"
	ocpconfigv1 "github.com/openshift/api/config/v1"
	cloudcredentialv1 "github.com/openshift/cloud-credential-operator/pkg/apis/cloudcredential/v1"
	machinev1beta1 "github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"
	"github.com/redhat-cop/egressip-ipam-operator/controllers/egressipam/reconcilecontext"
	"github.com/scylladb/go-set/strset"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type AWSInfra struct {
	//direct ocp client (not cached client)
	dc                client.Client
	c                 *ec2.EC2
	log               logr.Logger
	selectedInstances map[string]*ec2.Instance
	//region in which this cluster is running
	region string
}

func NewAWSInfra(directClient client.Client, rc *reconcilecontext.ReconcileContext) (reconcilecontext.Infra, error) {
	aWSInfra := &AWSInfra{
		log:    ctrl.Log.WithName("AWSInfra"),
		dc:     directClient,
		region: rc.Infrastructure.Status.PlatformStatus.AWS.Region,
	}
	c, err := aWSInfra.getAWSClient(rc.Infrastructure, rc.CloudCredentialsSecret)
	if err != nil {
		aWSInfra.log.Error(err, "unable to create aws client")
		return nil, err
	}
	aWSInfra.c = c
	selectedInstances, err := aWSInfra.getAWSInstances(rc.SelectedNodes)
	if err != nil {
		aWSInfra.log.Error(err, "unable to retrieve selected instances")
		return nil, err
	}
	aWSInfra.selectedInstances = selectedInstances
	return aWSInfra, nil
}

// GetUsedIPsByCIDR returns a map of reserved IPs by CIDR, this IPs cannot be used for assigning to namespaces
func (i *AWSInfra) GetUsedIPsByCIDR(rc *reconcilecontext.ReconcileContext) (map[string][]net.IP, error) {
	IPsByCIDR, err := i.getAWSUsedIPsByCIDR(rc)
	if err != nil {
		i.log.Error(err, "unable to get used IPs by CIDR")
		return map[string][]net.IP{}, nil
	}
	//adding always taken IPs per AWS rules
	for cidr := range IPsByCIDR {
		base, _, err := net.ParseCIDR(cidr)
		if err != nil {
			i.log.Error(err, "unable to parse", "cidr", cidr)
			return map[string][]net.IP{}, nil
		}
		IPsByCIDR[cidr] = append(IPsByCIDR[cidr], ipmath.DeltaIP(base, 1), ipmath.DeltaIP(base, 2), ipmath.DeltaIP(base, 3))
	}
	return IPsByCIDR, nil
}

// ReconcileInstanceSecondaryIPs will make sure that Assigned Egress IPs to instances are correclty reconciled
// this includes adding and possibly removing secondary IPs to selected instances.
func (i *AWSInfra) ReconcileInstanceSecondaryIPs(rc *reconcilecontext.ReconcileContext) error {
	err := i.removeAWSUnusedIPs(rc)
	if err != nil {
		i.log.Error(err, "unable to remove assigned AWS IPs")
		return err
	}

	err = i.reconcileAWSAssignedIPs(rc)
	if err != nil {
		i.log.Error(err, "unable to assign egress IPs to aws machines")
		return err
	}
	return nil
}

// RemoveAllAssignedIPs uncoditionally remoevs all the assigned IPs to VMs, used in clean-up login
func (i *AWSInfra) RemoveAllAssignedIPs(rc *reconcilecontext.ReconcileContext) error {
	return i.removeAllAWSAssignedIPs(rc)
}

func (i *AWSInfra) getAWSInstances(nodes map[string]corev1.Node) (map[string]*ec2.Instance, error) {
	instanceIds := []*string{}
	for _, node := range nodes {
		instanceIds = append(instanceIds, aws.String(getAWSIDFromProviderID(node.Spec.ProviderID)))
	}
	input := &ec2.DescribeInstancesInput{
		InstanceIds: instanceIds,
	}
	result, err := i.c.DescribeInstances(input)
	if err != nil {
		i.log.Error(err, "unable to get aws ", "instances", instanceIds)
		return map[string]*ec2.Instance{}, err
	}
	instances := map[string]*ec2.Instance{}
	for _, reservation := range result.Reservations {
		for _, instance := range reservation.Instances {
			instances[*instance.InstanceId] = instance
		}
	}
	return instances, nil
}

func getAWSIDFromProviderID(providerID string) string {
	strs := strings.Split(providerID, "/")
	return strs[len(strs)-1]
}

func getAWSIDFromNode(node corev1.Node) string {
	return getAWSIDFromProviderID(node.Spec.ProviderID)
}

// removes AWS secondary IPs that are currently assigned but not needed
func (i *AWSInfra) removeAWSUnusedIPs(rc *reconcilecontext.ReconcileContext) error {
	results := make(chan error)
	defer close(results)
	for node, ips := range rc.FinallyAssignedIPsByNode {
		nodec := node
		ipsc := ips
		go func() {
			instance := i.selectedInstances[getAWSIDFromNode(rc.AllNodes[nodec])]
			awsAssignedIPs := []string{}
			for _, ipipas := range instance.NetworkInterfaces[0].PrivateIpAddresses {
				if !(*ipipas.Primary) {
					awsAssignedIPs = append(awsAssignedIPs, *ipipas.PrivateIpAddress)
				}
			}
			toBeRemovedIPs := strset.Difference(strset.New(awsAssignedIPs...), strset.New(ipsc...)).List()
			if len(toBeRemovedIPs) > 0 {
				i.log.Info("vm", "instance ", instance.InstanceId, " will be freed from IPs ", toBeRemovedIPs)
				toBeRemovedIPsAWSStr := []*string{}
				for _, ip := range toBeRemovedIPs {
					copyip := ip
					toBeRemovedIPsAWSStr = append(toBeRemovedIPsAWSStr, &copyip)
				}

				input := &ec2.UnassignPrivateIpAddressesInput{
					NetworkInterfaceId: instance.NetworkInterfaces[0].NetworkInterfaceId,
					PrivateIpAddresses: toBeRemovedIPsAWSStr,
				}
				_, err := i.c.UnassignPrivateIpAddresses(input)
				if err != nil {
					i.log.Error(err, "unable to remove IPs from ", "instance", instance.InstanceId)
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

// assigns secondary IPs to AWS machines
func (i *AWSInfra) reconcileAWSAssignedIPs(rc *reconcilecontext.ReconcileContext) error {
	results := make(chan error)
	defer close(results)
	for node, assignedIPsToNode := range rc.FinallyAssignedIPsByNode {
		nodec := node
		ipsc := assignedIPsToNode
		go func() {
			instance := i.selectedInstances[getAWSIDFromNode(rc.AllNodes[nodec])]
			awsAssignedIPs := []string{}
			for _, ipipas := range instance.NetworkInterfaces[0].PrivateIpAddresses {
				if !*ipipas.Primary {
					awsAssignedIPs = append(awsAssignedIPs, *ipipas.PrivateIpAddress)
				}
			}
			toBeAssignedIPs := strset.Difference(strset.New(ipsc...), strset.New(awsAssignedIPs...)).List()
			if len(toBeAssignedIPs) > 0 {
				i.log.Info("vm", "instance ", instance.InstanceId, " will be assigned IPs ", toBeAssignedIPs)
				toBeAssignedIPsAWSStr := []*string{}
				for _, ip := range toBeAssignedIPs {
					copyip := ip
					toBeAssignedIPsAWSStr = append(toBeAssignedIPsAWSStr, &copyip)
				}

				input := &ec2.AssignPrivateIpAddressesInput{
					NetworkInterfaceId: instance.NetworkInterfaces[0].NetworkInterfaceId,
					PrivateIpAddresses: toBeAssignedIPsAWSStr,
				}
				_, err := i.c.AssignPrivateIpAddresses(input)
				if err != nil {
					i.log.Error(err, "unable to assigne IPs to ", "instance", instance.InstanceId)
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

func GetAWSCredentialsRequestProviderSpec() *cloudcredentialv1.AWSProviderSpec {
	return &cloudcredentialv1.AWSProviderSpec{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "cloudcredential.openshift.io/v1",
			Kind:       "AWSProviderSpec",
		},
		StatementEntries: []cloudcredentialv1.StatementEntry{
			{
				Action: []string{
					"ec2:DescribeInstances",
					"ec2:UnassignPrivateIpAddresses",
					"ec2:AssignPrivateIpAddresses",
					"ec2:DescribeSubnets",
					"ec2:DescribeNetworkInterfaces",
					"ec2:DescribeInstanceTypes",
				},
				Effect:   "Allow",
				Resource: "*",
				// "Key": "kubernetes.io/cluster/cluster-b92f-78s8h",
				// "Value": "owned"
			},
		},
	}
}

func (i *AWSInfra) removeAllAWSAssignedIPs(rc *reconcilecontext.ReconcileContext) error {
	results := make(chan error)
	defer close(results)
	for _, node := range rc.SelectedNodes {
		nodec := node.DeepCopy()
		go func() {
			instance, ok := i.selectedInstances[getAWSIDFromNode(*nodec)]
			if !ok {
				err := errors.New("type assertion failed")
				i.log.Error(err, "*ec2.Instance type assertion failed")
				results <- err
				return
			}
			for _, secondaryIP := range instance.NetworkInterfaces[0].PrivateIpAddresses[1:] {
				input := &ec2.UnassignPrivateIpAddressesInput{
					NetworkInterfaceId: instance.NetworkInterfaces[0].NetworkInterfaceId,
					PrivateIpAddresses: []*string{secondaryIP.PrivateIpAddress},
				}
				_, err := i.c.UnassignPrivateIpAddresses(input)
				if err != nil {
					i.log.Error(err, "unable to remove IPs from ", "instance", instance.InstanceId)
					results <- err
					return
				}
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

// getTakenIPsByCIDR this function will lookup all the subnets in which nodes are deployed and then lookup all the ips allocated by those subnest
// currently it is requred that cidr declared in the egress map, correspon to cidr declared in subnets.
func (i *AWSInfra) getAWSUsedIPsByCIDR(rc *reconcilecontext.ReconcileContext) (map[string][]net.IP, error) {
	machinesetList := &machinev1beta1.MachineSetList{}
	err := i.dc.List(rc.Context, machinesetList, &client.ListOptions{})
	if err != nil {
		i.log.Error(err, "unable to load all machinesets")
		return map[string][]net.IP{}, err
	}

	subnetIDs := []*string{}
	subnetTagNames := []*string{}

	for _, machine := range machinesetList.Items {
		aWSMachineProviderConfig := AWSMachineProviderConfig{}
		err := json.Unmarshal(machine.Spec.Template.Spec.ProviderSpec.Value.Raw, &aWSMachineProviderConfig)
		if err != nil {
			i.log.Error(err, "unable to unmarshall into awsproviderv1alpha2.AWSMachineSpec", "raw json", string(machine.Spec.Template.Spec.ProviderSpec.Value.Raw))
			return map[string][]net.IP{}, err
		}
		// if a subnet is selected by ID, we use the ID directly
		if aWSMachineProviderConfig.Subnet.ID != nil {
			subnetIDs = append(subnetIDs, aWSMachineProviderConfig.Subnet.ID)
			// if subnets are selected by tag, we save the tag to find the corresponding IDs later on
		} else {
			for _, filter := range aWSMachineProviderConfig.Subnet.Filters {

				if filter.Name == "tag:Name" {
					for _, value := range filter.Values {
						subnetTagNames = append(subnetTagNames, aws.String(value))
					}
				}
			}
		}
	}

	if len(subnetTagNames) > 0 {
		describeSubnetInput := &ec2.DescribeSubnetsInput{}
		describeSubnetInput.Filters = []*ec2.Filter{
			{
				Name:   aws.String("tag:Name"),
				Values: subnetTagNames,
			},
		}

		describeSubnetResult, err := i.c.DescribeSubnets(describeSubnetInput)
		if err != nil {
			i.log.Error(err, "unable to retrieve subnets ", "with request", describeSubnetInput)
			return map[string][]net.IP{}, err
		}

		for _, subnet := range describeSubnetResult.Subnets {
			subnetIDs = append(subnetIDs, subnet.SubnetId)
		}
	}

	describeNetworkInterfacesInput := &ec2.DescribeNetworkInterfacesInput{
		Filters: []*ec2.Filter{
			{
				Name:   aws.String("subnet-id"),
				Values: subnetIDs,
			},
		},
	}

	describeNetworkInterfacesResult, err := i.c.DescribeNetworkInterfaces(describeNetworkInterfacesInput)
	if err != nil {
		i.log.Error(err, "unable to retrieve network interfaces ", "with request", describeNetworkInterfacesInput)
		return map[string][]net.IP{}, err
	}
	i.log.V(1).Info("", "describeNetworkInterfacesResult", describeNetworkInterfacesResult)

	usedIPsByCIDR := map[string][]net.IP{}
	for _, cidr := range rc.CIDRs {
		usedIPsByCIDR[cidr] = []net.IP{}
		_, CIDR, err := net.ParseCIDR(cidr)
		if err != nil {
			i.log.Error(err, "unable to parse ", "CIDR", cidr)
			return map[string][]net.IP{}, err
		}
		for _, networkInterface := range describeNetworkInterfacesResult.NetworkInterfaces {
			for _, address := range networkInterface.PrivateIpAddresses {
				IP := net.ParseIP(*address.PrivateIpAddress)
				if IP == nil {
					i.log.Error(err, "unable to parse ", "IP", *address.PrivateIpAddress)
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

// this is a hack to work around current dependecy hell

// AWSMachineProviderConfig is the Schema for the awsmachineproviderconfigs API
// +k8s:openapi-gen=true
type AWSMachineProviderConfig struct {
	//TODO As par of the hack I deleted all the other fields in the hope this will make it more stable.
	// Subnet is a reference to the subnet to use for this instance
	Subnet AWSResourceReference `json:"subnet"`
}

// AWSResourceReference is a reference to a specific AWS resource by ID, ARN, or filters.
// Only one of ID, ARN or Filters may be specified. Specifying more than one will result in
// a validation error.
type AWSResourceReference struct {
	// ID of resource
	// +optional
	ID *string `json:"id,omitempty"`

	// ARN of resource
	// +optional
	ARN *string `json:"arn,omitempty"`

	// Filters is a set of filters used to identify a resource
	Filters []Filter `json:"filters,omitempty"`
}

// Filter is a filter used to identify an AWS resource
type Filter struct {
	// Name of the filter. Filter names are case-sensitive.
	Name string `json:"name"`

	// Values includes one or more filter values. Filter values are case-sensitive.
	Values []string `json:"values,omitempty"`
}

func (i *AWSInfra) getAWSClient(infrastructure *ocpconfigv1.Infrastructure, cloudCredentialsSecret *corev1.Secret) (*ec2.EC2, error) {
	id, key, err := i.getAWSCredentials(cloudCredentialsSecret)
	if err != nil {
		i.log.Error(err, "unable to get aws credentials")
		return nil, err
	}
	mySession := session.Must(session.NewSession())
	client := ec2.New(mySession, aws.NewConfig().WithCredentials(credentials.NewStaticCredentials(id, key, "")).WithRegion(infrastructure.Status.PlatformStatus.AWS.Region))
	return client, nil
}

func (i *AWSInfra) getAWSCredentials(cloudCredentialsSecret *corev1.Secret) (id string, key string, err error) {

	// aws_access_key_id: XXX
	// aws_secret_access_key: YYY

	awsAccessKeyID, ok := cloudCredentialsSecret.Data["aws_access_key_id"]
	if !ok {
		err := errors.New("unable to find key aws_access_key_id in secret " + cloudCredentialsSecret.String())
		i.log.Error(err, "")
		return "", "", err
	}

	awsSecretAccessKey, ok := cloudCredentialsSecret.Data["aws_secret_access_key"]
	if !ok {
		err := errors.New("unable to find key aws_secret_access_key in secret " + cloudCredentialsSecret.String())
		i.log.Error(err, "")
		return "", "", err
	}

	return string(awsAccessKeyID), string(awsSecretAccessKey), nil
}

func (i *AWSInfra) GetIPCapacity(node *corev1.Node) (uint32, error) {
	itc, err := i.getInstanceTypeCache(context.TODO())
	if err != nil {
		i.log.Error(err, "unable to get instance type cache")
		return 0, err
	}
	instanceType, ok := node.Labels["beta.kubernetes.io/instance-type"]
	if !ok {
		err := errors.New("unable to determinte instance type")
		i.log.Error(err, "unable to get capacity for", "node", node)
		return 0, err
	}
	capacity, err := itc.getCapacity(instanceType)
	if err != nil {
		i.log.Error(err, "unable to get capacity for", "node", node)
		return 0, err
	}
	return capacity, nil
}

type instanceTypeCache struct {
	instanceTypeMap map[string]ec2.InstanceTypeInfo
}

var theInstanceTypeCache *instanceTypeCache

func (i *AWSInfra) getInstanceTypeCache(ctx context.Context) (*instanceTypeCache, error) {
	if theInstanceTypeCache == nil {
		theInstanceTypeCache = &instanceTypeCache{}
	}
	if len(theInstanceTypeCache.instanceTypeMap) == 0 {
		err := i.initializeIntanceTypeCache(ctx, theInstanceTypeCache)
		if err != nil {
			i.log.Error(err, "unable to initialize instance type cache")
			return nil, err
		}
	}
	return theInstanceTypeCache, nil
}

func (i *AWSInfra) initializeIntanceTypeCache(ctx context.Context, cache *instanceTypeCache) error {
	instanceTypeMap := map[string]ec2.InstanceTypeInfo{}
	err := i.c.DescribeInstanceTypesPagesWithContext(ctx, &ec2.DescribeInstanceTypesInput{}, func(dito *ec2.DescribeInstanceTypesOutput, b bool) bool {
		for i := range dito.InstanceTypes {
			instanceTypeMap[*dito.InstanceTypes[i].InstanceType] = *dito.InstanceTypes[i]
		}
		return true
	})
	if err != nil {
		i.log.Error(err, "unable to list instance types info")
		return err
	}
	cache.instanceTypeMap = instanceTypeMap
	return nil
}

func (itc *instanceTypeCache) getCapacity(instanceType string) (uint32, error) {
	iti, ok := itc.instanceTypeMap[instanceType]
	if !ok {
		err := errors.New("unable to find instance type in instance type cache")
		return 0, err
	}
	return uint32(*iti.NetworkInfo.Ipv4AddressesPerInterface), nil
}
