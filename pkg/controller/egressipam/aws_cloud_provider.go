package egressipam

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jpillora/ipmath"
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
	OcpClient  *OcpClient
	reconciler *ReconcileEgressIPAM

	//aws specific
	AwsRegion string

	awsClient            *ec2.EC2
	selectedAWSInstances map[string]*ec2.Instance
	awsUsedIPsByCIDR     map[string][]net.IP
}

// Reconcile does the AWS specific handling of the reconciliation.
//
// 1. load all the nodes that comply with the additional filters of this EgressIPAM
//
// 2. sort the nodes by node_label/value so to have a maps of CIDR:[]node
//
// 3. load all the secondary IPs assigned to AWS instances by node
//
// 4. comparing with general #3, free the assigned AWS secondary IPs that are not needed anymore
//
// 5. assign IPs to nodes, considering the currently assigned IPs (we try not to move the currently assigned IPs)
//
// 6. reconcile assigned IP to nodes with secondary IPs assigned to AWS machines
//
// 7. reconcile assigned IP to nodes with corresponding hostsubnet
func (a *AwsCloudprovider) Reconcile(rc *ReconcileContext) error {
	assignedIPsByNode := a.reconciler.GetAssignedIPsByNode(rc)
	rc.InitiallyAssignedIPsByNode = assignedIPsByNode

	log.V(1).Info("", "initiallyAssignedIPsByNode", rc.InitiallyAssignedIPsByNode)

	finallyAssignedIPsByNode, err := a.reconciler.AssignIPsToNodes(rc)
	if err != nil {
		log.Error(err, "unable to assign egress IPs to nodes")
		return err
	}
	log.V(1).Info("", "finallyAssignedIPsByNode", finallyAssignedIPsByNode)

	rc.FinallyAssignedIPsByNode = finallyAssignedIPsByNode
	err = a.manageCloudIPs(rc)
	if err != nil {
		return err
	}
	log.V(1).Info("", "finallyAssignedIPsByNode", finallyAssignedIPsByNode)

	err = a.reconciler.ReconcileHSAssignedIPs(rc)
	if err != nil {
		log.Error(err, "unable to reconcile hostsubnet with ", "nodes", assignedIPsByNode)
		return err
	}

	return nil
}

func (a *AwsCloudprovider) AssignIPsToNamespace(rc *ReconcileContext, IPsByCIDR map[string][]net.IP) ([]corev1.Namespace, error) {
	//add some reserved IPs
	for cidr := range IPsByCIDR {
		base, _, err := net.ParseCIDR(cidr)
		if err != nil {
			log.Error(err, "unable to parse", "cidr", cidr)
			return []corev1.Namespace{}, err
		}
		IPsByCIDR[cidr] = append(IPsByCIDR[cidr], ipmath.DeltaIP(base, 1), ipmath.DeltaIP(base, 2), ipmath.DeltaIP(base, 3))
		IPsByCIDR[cidr] = append(IPsByCIDR[cidr], a.getUsedIPs(rc)[cidr]...)
	}

	return nil, nil
}

func (a *AwsCloudprovider) getUsedIPs(_ *ReconcileContext) map[string][]net.IP {
	return a.awsUsedIPsByCIDR
}

func (a *AwsCloudprovider) CleanUpCloudProvider(rc *ReconcileContext) error {
	results := make(chan error)
	defer close(results)
	for _, node := range rc.SelectedNodes {
		nodec := node.DeepCopy()
		go func() {
			instance := a.selectedAWSInstances[a.getAWSIDFromNode(*nodec)]
			for _, secondaryIP := range instance.NetworkInterfaces[0].PrivateIpAddresses[1:] {
				input := &ec2.UnassignPrivateIpAddressesInput{
					NetworkInterfaceId: instance.NetworkInterfaces[0].NetworkInterfaceId,
					PrivateIpAddresses: []*string{secondaryIP.PrivateIpAddress},
				}
				_, err := a.awsClient.UnassignPrivateIpAddresses(input)
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

func (a *AwsCloudprovider) manageCloudIPs(rc *ReconcileContext) error {
	err := a.removeAWSUnusedIPs(rc)
	if err != nil {
		log.Error(err, "unable to remove assigned AWS IPs")
		return err
	}

	err = a.reconcileAWSAssignedIPs(rc)
	if err != nil {
		log.Error(err, "unable to assign egress IPs to aws machines")
		return err
	}

	return nil
}

func (a *AwsCloudprovider) CollectCloudData(rc *ReconcileContext) error {
	results := make(chan error)
	defer close(results)

	go func() {
		selectedAWSInstances, err := a.getAWSInstances(rc.SelectedNodes)
		if err != nil {
			log.Error(err, "unable to get selected AWS Instances")
			results <- err
			return
		}
		a.selectedAWSInstances = selectedAWSInstances

		log.V(1).Info("", "selectedAWSInstances", a.getAWSInstancesMapKeys(a.selectedAWSInstances))
		results <- nil
		return
	}()

	go func() {
		awsUsedIPsByCIDR, err := a.getAWSUsedIPsByCIDR(rc)
		if err != nil {
			log.Error(err, "unable to get used AWS IPs by CIDR")
			results <- err
			return
		}
		a.awsUsedIPsByCIDR = awsUsedIPsByCIDR

		log.V(1).Info("", "awsUsedIPsByCIDR", a.awsUsedIPsByCIDR)
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

func (a *AwsCloudprovider) Initialize(r *ReconcileEgressIPAM) error {
	a.reconciler = r

	if a.awsClient == nil {
		if err := a.createCredentialRequest(); err != nil {
			log.Error(err, "unable to create the credential request")
			return err
		}

		if err := a.getAWSClient(); err != nil {
			log.Error(err, "unable to initialize the AWS Client")
			return err
		}
	}

	return nil
}

func (a *AwsCloudprovider) getAWSClient() error {
	id, key, err := a.getAWSCredentials()
	if err != nil {
		log.Error(err, "unable to get aws credentials")
		return err
	}
	if a.AwsRegion == "" {
		log.Error(errors.New("no region given"), "unable to determine region of AWS")
		return err
	}

	mySession := session.Must(session.NewSession())
	a.awsClient = ec2.New(mySession, aws.NewConfig().WithCredentials(credentials.NewStaticCredentials(id, key, "")).WithRegion(a.AwsRegion))
	return nil
}

func (a *AwsCloudprovider) getAWSInstances(nodes map[string]corev1.Node) (map[string]*ec2.Instance, error) {
	var instanceIds []*string
	for _, node := range nodes {
		instanceIds = append(instanceIds, aws.String(a.getAWSIDFromProviderID(node.Spec.ProviderID)))
	}
	input := &ec2.DescribeInstancesInput{
		InstanceIds: instanceIds,
	}
	result, err := a.awsClient.DescribeInstances(input)
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

func (a *AwsCloudprovider) getAWSInstancesMapKeys(instances map[string]*ec2.Instance) []string {
	var instanceIDs []string
	for id := range instances {
		instanceIDs = append(instanceIDs, id)
	}
	return instanceIDs
}

func (a *AwsCloudprovider) getAWSIDFromProviderID(providerID string) string {
	s := strings.Split(providerID, "/")
	return s[len(s)-1]
}

func (a *AwsCloudprovider) getAWSIDFromNode(node corev1.Node) string {
	return a.getAWSIDFromProviderID(node.Spec.ProviderID)
}

// removes AWS secondary IPs that are currently assigned but not needed
func (a *AwsCloudprovider) removeAWSUnusedIPs(rc *ReconcileContext) error {
	results := make(chan error)
	defer close(results)
	for node, ips := range rc.FinallyAssignedIPsByNode {
		nodec := node
		ipsc := ips
		go func() {
			instance := a.selectedAWSInstances[a.getAWSIDFromNode(rc.AllNodes[nodec])]
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
					copyIP := ip
					toBeRemovedIPsAWSStr = append(toBeRemovedIPsAWSStr, &copyIP)
				}

				input := &ec2.UnassignPrivateIpAddressesInput{
					NetworkInterfaceId: instance.NetworkInterfaces[0].NetworkInterfaceId,
					PrivateIpAddresses: toBeRemovedIPsAWSStr,
				}
				_, err := a.awsClient.UnassignPrivateIpAddresses(input)
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
func (a *AwsCloudprovider) reconcileAWSAssignedIPs(rc *ReconcileContext) error {
	results := make(chan error)
	defer close(results)
	for node, assignedIPsToNode := range rc.FinallyAssignedIPsByNode {
		nodec := node
		ipsc := assignedIPsToNode
		go func() {
			instance := a.selectedAWSInstances[a.getAWSIDFromNode(rc.AllNodes[nodec])]
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
					copyIp := ip
					toBeAssignedIPsAWSStr = append(toBeAssignedIPsAWSStr, &copyIp)
				}

				input := &ec2.AssignPrivateIpAddressesInput{
					NetworkInterfaceId: instance.NetworkInterfaces[0].NetworkInterfaceId,
					PrivateIpAddresses: toBeAssignedIPsAWSStr,
				}
				_, err := a.awsClient.AssignPrivateIpAddresses(input)
				if err != nil {
					log.Error(err, "unable to assign IPs to ", "instance", instance.InstanceId)
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

func (a *AwsCloudprovider) createCredentialRequest() error {
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
	namespace, err := (*a.OcpClient).GetOperatorNamespace()
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

	err = (*a.OcpClient).CreateOrUpdateResource(nil, "", &request)
	if err != nil {
		log.Error(err, "unable to create or update ", "credential request", request)
		return err
	}

	return nil
}

//goland:noinspection SpellCheckingInspection
func (a *AwsCloudprovider) getAWSCredentials() (id string, key string, err error) {
	awsCredentialSecret, err := (*a.OcpClient).GetCredentialSecret()
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

//getTakenIPsByCIDR this function will lookup all the subnets in which nodes are deployed and then lookup all the ips allocated by those subnet
//currently it is required that cidr declared in the egress map, corresponds to cidr declared in subnets.
func (a *AwsCloudprovider) getAWSUsedIPsByCIDR(rc *ReconcileContext) (map[string][]net.IP, error) {
	machinesetList, err := (*a.OcpClient).ListMachineSets(context.TODO())
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

	describeSubnetResult, err := a.awsClient.DescribeSubnets(describeSubnetInput)
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

	describeNetworkInterfacesResult, err := a.awsClient.DescribeNetworkInterfaces(describeNetworkInterfacesInput)
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

// this is a hack to work around current dependency hell

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
