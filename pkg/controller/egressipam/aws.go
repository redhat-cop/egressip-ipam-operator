package egressipam

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
	multierror "github.com/hashicorp/go-multierror"
	cloudcredentialv1 "github.com/openshift/cloud-credential-operator/pkg/apis/cloudcredential/v1"
	machinev1beta1 "github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"
	"github.com/scylladb/go-set/strset"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *ReconcileEgressIPAM) getAWSClient() (*ec2.EC2, error) {
	infrastructure, err := r.getInfrastructure()
	if err != nil {
		log.Error(err, "unable to get infrastructure")
		return nil, err
	}
	id, key, err := r.getAWSCredentials()
	if err != nil {
		log.Error(err, "unable to get aws credentials")
		return nil, err
	}
	mySession := session.Must(session.NewSession())
	client := ec2.New(mySession, aws.NewConfig().WithCredentials(credentials.NewStaticCredentials(id, key, "")).WithRegion(infrastructure.Status.PlatformStatus.AWS.Region))
	return client, nil
}

func getAWSIstances(client *ec2.EC2, nodes map[string]corev1.Node) (map[string]*ec2.Instance, error) {
	instanceIds := []*string{}
	for _, node := range nodes {
		instanceIds = append(instanceIds, aws.String(getAWSIDFromProviderID(node.Spec.ProviderID)))
	}
	input := &ec2.DescribeInstancesInput{
		InstanceIds: instanceIds,
	}
	result, err := client.DescribeInstances(input)
	if err != nil {
		log.Error(err, "unable to get aws ", "instances", instanceIds)
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

func getAWSIstancesMapKeys(instances map[string]*ec2.Instance) []string {
	instanceIDs := []string{}
	for id := range instances {
		instanceIDs = append(instanceIDs, id)
	}
	return instanceIDs
}

func getAWSIDFromProviderID(providerID string) string {
	strs := strings.Split(providerID, "/")
	return strs[len(strs)-1]
}

func getAWSIDFromNode(node corev1.Node) string {
	return getAWSIDFromProviderID(node.Spec.ProviderID)
}

// removes AWS secondary IPs that are currently assigned but not needed
func (r *ReconcileEgressIPAM) removeAWSUnusedIPs(rc *reconcileContext) error {
	results := make(chan error)
	defer close(results)
	for node, ips := range rc.finallyAssignedIPsByNode {
		nodec := node
		ipsc := ips
		go func() {
			instance := rc.selectedAWSInstances[getAWSIDFromNode(rc.allNodes[nodec])]
			awsAssignedIPs := []string{}
			for _, ipipas := range instance.NetworkInterfaces[0].PrivateIpAddresses {
				if !(*ipipas.Primary) {
					awsAssignedIPs = append(awsAssignedIPs, *ipipas.PrivateIpAddress)
				}
			}
			toBeRemovedIPs := strset.Difference(strset.New(awsAssignedIPs...), strset.New(ipsc...)).List()
			if len(toBeRemovedIPs) > 0 {
				log.Info("vm", "instance ", instance.InstanceId, " will be freed from IPs ", toBeRemovedIPs)
				toBeRemovedIPsAWSStr := []*string{}
				for _, ip := range toBeRemovedIPs {
					copyip := ip
					toBeRemovedIPsAWSStr = append(toBeRemovedIPsAWSStr, &copyip)
				}

				input := &ec2.UnassignPrivateIpAddressesInput{
					NetworkInterfaceId: instance.NetworkInterfaces[0].NetworkInterfaceId,
					PrivateIpAddresses: toBeRemovedIPsAWSStr,
				}
				_, err := rc.awsClient.UnassignPrivateIpAddresses(input)
				if err != nil {
					log.Error(err, "unable to remove IPs from ", "instance", instance.InstanceId)
					results <- err
					return
				}
			}
			results <- nil
			return
		}()
	}
	result := &multierror.Error{}
	for range rc.finallyAssignedIPsByNode {
		multierror.Append(result, <-results)
	}

	return result.ErrorOrNil()
}

// assigns secondary IPs to AWS machines
func (r *ReconcileEgressIPAM) reconcileAWSAssignedIPs(rc *reconcileContext) error {
	results := make(chan error)
	defer close(results)
	for node, assignedIPsToNode := range rc.finallyAssignedIPsByNode {
		nodec := node
		ipsc := assignedIPsToNode
		go func() {
			instance := rc.selectedAWSInstances[getAWSIDFromNode(rc.allNodes[nodec])]
			awsAssignedIPs := []string{}
			for _, ipipas := range instance.NetworkInterfaces[0].PrivateIpAddresses {
				if !*ipipas.Primary {
					awsAssignedIPs = append(awsAssignedIPs, *ipipas.PrivateIpAddress)
				}
			}
			toBeAssignedIPs := strset.Difference(strset.New(ipsc...), strset.New(awsAssignedIPs...)).List()
			if len(toBeAssignedIPs) > 0 {
				log.Info("vm", "instance ", instance.InstanceId, " will be assigned IPs ", toBeAssignedIPs)
				toBeAssignedIPsAWSStr := []*string{}
				for _, ip := range toBeAssignedIPs {
					copyip := ip
					toBeAssignedIPsAWSStr = append(toBeAssignedIPsAWSStr, &copyip)
				}

				input := &ec2.AssignPrivateIpAddressesInput{
					NetworkInterfaceId: instance.NetworkInterfaces[0].NetworkInterfaceId,
					PrivateIpAddresses: toBeAssignedIPsAWSStr,
				}
				_, err := rc.awsClient.AssignPrivateIpAddresses(input)
				if err != nil {
					log.Error(err, "unable to assigne IPs to ", "instance", instance.InstanceId)
					results <- err
					return
				}
			}
			results <- nil
			return
		}()
	}
	result := &multierror.Error{}
	for range rc.finallyAssignedIPsByNode {
		multierror.Append(result, <-results)
	}
	return result.ErrorOrNil()
}

func (r *ReconcileEgressIPAM) createAWSCredentialRequest() error {
	awsSpec := cloudcredentialv1.AWSProviderSpec{
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
				},
				Effect:   "Allow",
				Resource: "*",
				// "Key": "kubernetes.io/cluster/cluster-b92f-78s8h",
				// "Value": "owned"
			},
		},
	}
	namespace, err := getOperatorNamespace()
	if err != nil {
		log.Error(err, "unable to get operator's namespace")
		return err
	}
	request := cloudcredentialv1.CredentialsRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "egress-ipam-operator",
			Namespace: "openshift-cloud-credential-operator",
		},
		TypeMeta: metav1.TypeMeta{
			APIVersion: "cloudcredential.openshift.io/v1",
			Kind:       "CredentialsRequest",
		},
		Spec: cloudcredentialv1.CredentialsRequestSpec{
			SecretRef: corev1.ObjectReference{
				Name:      credentialsSecretName,
				Namespace: namespace,
			},
			ProviderSpec: &runtime.RawExtension{
				Object: &awsSpec,
			},
		},
	}
	c, err := r.getDirectClient()
	if err != nil {
		log.Error(err, "unable to create direct client")
		return err
	}
	err = r.createOrUpdateResourceWithClient(c, nil, "", &request)
	if err != nil {
		log.Error(err, "unable to create or update ", "credential request", request)
		return err
	}

	return nil
}

func (r *ReconcileEgressIPAM) getAWSCredentials() (id string, key string, err error) {
	awsCredentialSecret, err := r.getCredentialSecret()
	if err != nil {
		log.Error(err, "unable to get credential secret")
		return "", "", err
	}

	// aws_access_key_id: QUtJQVRKVjUyWVhTV1dTRFhQTEI=
	// aws_secret_access_key: ZzlTQzR1VEd5YUV5ejhRZXVCYnMzOTgzZDlEQ216K1NESjJFVFNTYQ==

	awsAccessKeyID, ok := awsCredentialSecret.Data["aws_access_key_id"]
	if !ok {
		err := errors.New("unable to find key aws_access_key_id in secret " + awsCredentialSecret.String())
		log.Error(err, "")
		return "", "", err
	}

	awsSecretAccessKey, ok := awsCredentialSecret.Data["aws_secret_access_key"]
	if !ok {
		err := errors.New("unable to find key aws_secret_access_key in secret " + awsCredentialSecret.String())
		log.Error(err, "")
		return "", "", err
	}

	return string(awsAccessKeyID), string(awsSecretAccessKey), nil
}

func (r *ReconcileEgressIPAM) removeAWSAssignedIPs(rc *reconcileContext) error {
	results := make(chan error)
	defer close(results)
	for _, node := range rc.selectedNodes {
		nodec := node.DeepCopy()
		go func() {
			instance := rc.selectedAWSInstances[getAWSIDFromNode(*nodec)]
			for _, secondaryIP := range instance.NetworkInterfaces[0].PrivateIpAddresses[1:] {
				input := &ec2.UnassignPrivateIpAddressesInput{
					NetworkInterfaceId: instance.NetworkInterfaces[0].NetworkInterfaceId,
					PrivateIpAddresses: []*string{secondaryIP.PrivateIpAddress},
				}
				_, err := rc.awsClient.UnassignPrivateIpAddresses(input)
				if err != nil {
					log.Error(err, "unable to remove IPs from ", "instance", instance.InstanceId)
					results <- err
					return
				}
			}
			results <- nil
			return
		}()
	}
	result := &multierror.Error{}
	for range rc.selectedNodes {
		multierror.Append(result, <-results)
	}
	return result.ErrorOrNil()
}

//getTakenIPsByCIDR this function will lookup all the subnets in which nodes are deployed and then lookup all the ips allocated by those subnest
//currently it is requred that cidr declared in the egress map, correspon to cidr declared in subnets.
func (r *ReconcileEgressIPAM) getAWSUsedIPsByCIDR(rc *reconcileContext) (map[string][]net.IP, error) {
	machinesetList := &machinev1beta1.MachineSetList{}
	err := r.GetClient().List(context.TODO(), machinesetList, &client.ListOptions{})
	if err != nil {
		log.Error(err, "unable to load all machinesets")
		return map[string][]net.IP{}, err
	}

	subnetTagNames := []*string{}
	for _, machine := range machinesetList.Items {
		aWSMachineProviderConfig := AWSMachineProviderConfig{}
		err := json.Unmarshal(machine.Spec.Template.Spec.ProviderSpec.Value.Raw, &aWSMachineProviderConfig)
		if err != nil {
			log.Error(err, "unable to unmarshall into wsproviderv1alpha2.AWSMachineSpec", "raw json", string(machine.Spec.Template.Spec.ProviderSpec.Value.Raw))
			return map[string][]net.IP{}, err
		}
		for _, filter := range aWSMachineProviderConfig.Subnet.Filters {
			if filter.Name == "tag:Name" {
				for _, value := range filter.Values {
					subnetTagNames = append(subnetTagNames, aws.String(value))
				}

			}
		}
	}

	describeSubnetInput := &ec2.DescribeSubnetsInput{
		Filters: []*ec2.Filter{
			{
				Name:   aws.String("tag:Name"),
				Values: subnetTagNames,
			},
		},
	}

	describeSubnetResult, err := rc.awsClient.DescribeSubnets(describeSubnetInput)
	if err != nil {
		log.Error(err, "unable to retrieve subnets ", "with request", describeSubnetInput)
		return map[string][]net.IP{}, err
	}
	log.V(1).Info("", "describeSubnetResult", describeSubnetResult)

	subnetIDs := []*string{}
	for _, subnet := range describeSubnetResult.Subnets {
		subnetIDs = append(subnetIDs, subnet.SubnetId)
	}

	describeNetworkInterfacesInput := &ec2.DescribeNetworkInterfacesInput{
		Filters: []*ec2.Filter{
			{
				Name:   aws.String("subnet-id"),
				Values: subnetIDs,
			},
		},
	}

	describeNetworkInterfacesResult, err := rc.awsClient.DescribeNetworkInterfaces(describeNetworkInterfacesInput)
	if err != nil {
		log.Error(err, "unable to retrieve network interfaces ", "with request", describeNetworkInterfacesInput)
		return map[string][]net.IP{}, err
	}
	log.V(1).Info("", "describeNetworkInterfacesResult", describeNetworkInterfacesResult)

	usedIPsByCIDR := map[string][]net.IP{}
	for _, cidr := range rc.cIDRs {
		usedIPsByCIDR[cidr] = []net.IP{}
		_, CIDR, err := net.ParseCIDR(cidr)
		if err != nil {
			log.Error(err, "unable to parse ", "CIDR", cidr)
			return map[string][]net.IP{}, err
		}
		for _, networkInterface := range describeNetworkInterfacesResult.NetworkInterfaces {
			for _, address := range networkInterface.PrivateIpAddresses {
				IP := net.ParseIP(*address.PrivateIpAddress)
				if IP == nil {
					log.Error(err, "unable to parse ", "IP", *address.PrivateIpAddress)
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
