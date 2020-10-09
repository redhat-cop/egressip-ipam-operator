package egressipam

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/redhat-cop/operator-utils/pkg/util"
	"net"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/hashicorp/go-multierror"
	cloudcredentialv1 "github.com/openshift/cloud-credential-operator/pkg/apis/cloudcredential/v1"
	"github.com/scylladb/go-set/strset"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

var _ Cloudprovider = &AwsCloudprovider{}

type AwsCloudprovider struct {
	// TODO 2020-10-03 klenkes74 it's needed to acces the CreateOrUpdate method. We need a nicer solution to this hack
	util.ReconcilerBase

	OcpClient *OcpClient

	//aws specific
	AwsRegion string

	awsClient            *ec2.EC2
	selectedAWSInstances map[string]*ec2.Instance
	awsUsedIPsByCIDR     map[string][]net.IP
}

func (r *AwsCloudprovider) FailureRegion(region string) error {
	r.AwsRegion = region

	return nil
}

func (r *AwsCloudprovider) GetUsedIPs(_ *ReconcileContext) map[string][]net.IP {
	return r.awsUsedIPsByCIDR
}

func (r *AwsCloudprovider) RemoveAssignedIPs(rc *ReconcileContext) error {
	err := r.initializeProvider()
	if err != nil {
		return err
	}

	results := make(chan error)
	defer close(results)
	for _, node := range rc.SelectedNodes {
		nodec := node.DeepCopy()
		go func() {
			instance := r.selectedAWSInstances[r.getAWSIDFromNode(*nodec)]
			for _, secondaryIP := range instance.NetworkInterfaces[0].PrivateIpAddresses[1:] {
				input := &ec2.UnassignPrivateIpAddressesInput{
					NetworkInterfaceId: instance.NetworkInterfaces[0].NetworkInterfaceId,
					PrivateIpAddresses: []*string{secondaryIP.PrivateIpAddress},
				}
				_, err := r.awsClient.UnassignPrivateIpAddresses(input)
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
	for range rc.SelectedNodes {
		_ = multierror.Append(result, <-results)
	}
	return result.ErrorOrNil()
}

func (r *AwsCloudprovider) ManageCloudIPs(rc *ReconcileContext) error {
	if r.awsClient == nil {
		err := r.getAWSClient()
		if err != nil {
			log.Error(err, "unable to intialize the AWS client")
			return err
		}
	}

	err := r.removeAWSUnusedIPs(rc)
	if err != nil {
		log.Error(err, "unable to remove assigned AWS IPs")
		return err
	}

	err = r.reconcileAWSAssignedIPs(rc)
	if err != nil {
		log.Error(err, "unable to assign egress IPs to aws machines")
		return err
	}

	return nil
}

func (r *AwsCloudprovider) HarvestCloudData(rc *ReconcileContext) error {
	if r.awsClient == nil {
		err := r.getAWSClient()
		if err != nil {
			log.Error(err, "unable to intialize the AWS client")
			return err
		}
	}

	results := make(chan error)
	defer close(results)

	go func() {
		selectedAWSInstances, err := r.getAWSIstances(rc.SelectedNodes)
		if err != nil {
			log.Error(err, "unable to get selected AWS Instances")
			results <- err
			return
		}
		r.selectedAWSInstances = selectedAWSInstances

		log.V(1).Info("", "selectedAWSInstances", r.getAWSInstancesMapKeys(r.selectedAWSInstances))
		results <- nil
		return
	}()

	go func() {
		awsUsedIPsByCIDR, err := r.getAWSUsedIPsByCIDR(rc)
		if err != nil {
			log.Error(err, "unable to get used AWS IPs by CIDR")
			results <- err
			return
		}
		r.awsUsedIPsByCIDR = awsUsedIPsByCIDR

		log.V(1).Info("", "awsUsedIPsByCIDR", r.awsUsedIPsByCIDR)
		results <- nil
		return
	}()
	// collect results
	result := &multierror.Error{}
	for range []string{"awsinstance", "usedIPS"} {
		err := <-results
		log.V(1).Info("receiving", "error", err)
		_ = multierror.Append(result, err)
	}
	log.V(1).Info("after aws initialization", "multierror", result.Error, "ErrorOrNil", result.ErrorOrNil())
	if result.ErrorOrNil() != nil {
		log.Error(result, "unable ro run parallel aws initialization")
	}

	return nil
}

func (r *AwsCloudprovider) initializeProvider() error {
	if r.awsClient == nil {
		err := r.getAWSClient()
		if err != nil {
			log.Error(err, "unable to intialize the AWS client")
			return err
		}
	}
	return nil
}

func (r *AwsCloudprovider) getAWSClient() error {
	id, key, err := r.getAWSCredentials()
	if err != nil {
		log.Error(err, "unable to get aws credentials")
		return err
	}
	if r.AwsRegion == "" {
		log.Error(errors.New("no region given"), "unable to determine region of AWS")
		return err
	}

	mySession := session.Must(session.NewSession())
	r.awsClient = ec2.New(mySession, aws.NewConfig().WithCredentials(credentials.NewStaticCredentials(id, key, "")).WithRegion(r.AwsRegion))
	return nil
}

func (r *AwsCloudprovider) getAWSIstances(nodes map[string]corev1.Node) (map[string]*ec2.Instance, error) {
	var instanceIds []*string
	for _, node := range nodes {
		instanceIds = append(instanceIds, aws.String(r.getAWSIDFromProviderID(node.Spec.ProviderID)))
	}
	input := &ec2.DescribeInstancesInput{
		InstanceIds: instanceIds,
	}
	result, err := r.awsClient.DescribeInstances(input)
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

func (r *AwsCloudprovider) getAWSInstancesMapKeys(instances map[string]*ec2.Instance) []string {
	var instanceIDs []string
	for id := range instances {
		instanceIDs = append(instanceIDs, id)
	}
	return instanceIDs
}

func (r *AwsCloudprovider) getAWSIDFromProviderID(providerID string) string {
	strs := strings.Split(providerID, "/")
	return strs[len(strs)-1]
}

func (r *AwsCloudprovider) getAWSIDFromNode(node corev1.Node) string {
	return r.getAWSIDFromProviderID(node.Spec.ProviderID)
}

// removes AWS secondary IPs that are currently assigned but not needed
func (r *AwsCloudprovider) removeAWSUnusedIPs(rc *ReconcileContext) error {
	results := make(chan error)
	defer close(results)
	for node, ips := range rc.FinallyAssignedIPsByNode {
		nodec := node
		ipsc := ips
		go func() {
			instance := r.selectedAWSInstances[r.getAWSIDFromNode(rc.AllNodes[nodec])]
			var awsAssignedIPs []string
			for _, ipipas := range instance.NetworkInterfaces[0].PrivateIpAddresses {
				if !(*ipipas.Primary) {
					awsAssignedIPs = append(awsAssignedIPs, *ipipas.PrivateIpAddress)
				}
			}
			toBeRemovedIPs := strset.Difference(strset.New(awsAssignedIPs...), strset.New(ipsc...)).List()
			if len(toBeRemovedIPs) > 0 {
				log.Info("vm", "instance ", instance.InstanceId, " will be freed from IPs ", toBeRemovedIPs)
				var toBeRemovedIPsAWSStr []*string
				for _, ip := range toBeRemovedIPs {
					copyip := ip
					toBeRemovedIPsAWSStr = append(toBeRemovedIPsAWSStr, &copyip)
				}

				input := &ec2.UnassignPrivateIpAddressesInput{
					NetworkInterfaceId: instance.NetworkInterfaces[0].NetworkInterfaceId,
					PrivateIpAddresses: toBeRemovedIPsAWSStr,
				}
				_, err := r.awsClient.UnassignPrivateIpAddresses(input)
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
	for range rc.FinallyAssignedIPsByNode {
		_ = multierror.Append(result, <-results)
	}

	return result.ErrorOrNil()
}

// assigns secondary IPs to AWS machines
func (r *AwsCloudprovider) reconcileAWSAssignedIPs(rc *ReconcileContext) error {
	results := make(chan error)
	defer close(results)
	for node, assignedIPsToNode := range rc.FinallyAssignedIPsByNode {
		nodec := node
		ipsc := assignedIPsToNode
		go func() {
			instance := r.selectedAWSInstances[r.getAWSIDFromNode(rc.AllNodes[nodec])]
			var awsAssignedIPs []string
			for _, ipipas := range instance.NetworkInterfaces[0].PrivateIpAddresses {
				if !*ipipas.Primary {
					awsAssignedIPs = append(awsAssignedIPs, *ipipas.PrivateIpAddress)
				}
			}
			toBeAssignedIPs := strset.Difference(strset.New(ipsc...), strset.New(awsAssignedIPs...)).List()
			if len(toBeAssignedIPs) > 0 {
				log.Info("vm", "instance ", instance.InstanceId, " will be assigned IPs ", toBeAssignedIPs)
				var toBeAssignedIPsAWSStr []*string
				for _, ip := range toBeAssignedIPs {
					copyip := ip
					toBeAssignedIPsAWSStr = append(toBeAssignedIPsAWSStr, &copyip)
				}

				input := &ec2.AssignPrivateIpAddressesInput{
					NetworkInterfaceId: instance.NetworkInterfaces[0].NetworkInterfaceId,
					PrivateIpAddresses: toBeAssignedIPsAWSStr,
				}
				_, err := r.awsClient.AssignPrivateIpAddresses(input)
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
	for range rc.FinallyAssignedIPsByNode {
		_ = multierror.Append(result, <-results)
	}
	return result.ErrorOrNil()
}

func (r *AwsCloudprovider) CreateCredentialRequest() error {
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
	namespace, err := (*r.OcpClient).GetOperatorNamespace()
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
				Name:      CredentialsSecretName,
				Namespace: namespace,
			},
			ProviderSpec: &runtime.RawExtension{
				Object: &awsSpec,
			},
		},
	}

	err = r.CreateOrUpdateResource(nil, "", &request)
	if err != nil {
		log.Error(err, "unable to create or update ", "credential request", request)
		return err
	}

	return nil
}

func (r *AwsCloudprovider) getAWSCredentials() (id string, key string, err error) {
	awsCredentialSecret, err := (*r.OcpClient).GetCredentialSecret()
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

//getTakenIPsByCIDR this function will lookup all the subnets in which nodes are deployed and then lookup all the ips allocated by those subnest
//currently it is requred that cidr declared in the egress map, correspon to cidr declared in subnets.
func (r *AwsCloudprovider) getAWSUsedIPsByCIDR(rc *ReconcileContext) (map[string][]net.IP, error) {
	machinesetList, err := (*r.OcpClient).ListMachineSets(context.TODO())
	if err != nil {
		log.Error(err, "unable to load all machinesets")
		return map[string][]net.IP{}, err
	}

	var subnetTagNames []*string
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

	describeSubnetResult, err := r.awsClient.DescribeSubnets(describeSubnetInput)
	if err != nil {
		log.Error(err, "unable to retrieve subnets ", "with request", describeSubnetInput)
		return map[string][]net.IP{}, err
	}
	log.V(1).Info("", "describeSubnetResult", describeSubnetResult)

	var subnetIDs []*string
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

	describeNetworkInterfacesResult, err := r.awsClient.DescribeNetworkInterfaces(describeNetworkInterfacesInput)
	if err != nil {
		log.Error(err, "unable to retrieve network interfaces ", "with request", describeNetworkInterfacesInput)
		return map[string][]net.IP{}, err
	}
	log.V(1).Info("", "describeNetworkInterfacesResult", describeNetworkInterfacesResult)

	usedIPsByCIDR := map[string][]net.IP{}
	for _, cidr := range rc.CIDRs {
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
