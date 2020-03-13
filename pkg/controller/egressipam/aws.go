package egressipam

import (
	"context"
	"errors"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	ocpconfigv1 "github.com/openshift/api/config/v1"
	cloudcredentialv1 "github.com/openshift/cloud-credential-operator/pkg/apis/cloudcredential/v1"
	"github.com/scylladb/go-set/strset"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
)

const aWSCredentialsSecretName = "egress-ipam-operator-cloud-credentials"

func getAWSClient(id string, key string, infra *ocpconfigv1.Infrastructure) (*ec2.EC2, error) {
	mySession := session.Must(session.NewSession())
	client := ec2.New(mySession, aws.NewConfig().WithCredentials(credentials.NewStaticCredentials(id, key, "")).WithRegion(infra.Status.PlatformStatus.AWS.Region))
	return client, nil
}

func getAWSInstance(client *ec2.EC2, node corev1.Node) (*ec2.Instance, error) {
	input := &ec2.DescribeInstancesInput{
		InstanceIds: []*string{
			aws.String(getAWSIDFromProviderID(node.Spec.ProviderID)),
		},
	}
	result, err := client.DescribeInstances(input)
	if err != nil {
		log.Error(err, "unable to get aws ", "instance", getAWSIDFromProviderID(node.Spec.ProviderID))
		return &ec2.Instance{}, err
	}
	return result.Reservations[0].Instances[0], nil
}

func getAWSIDFromProviderID(providerID string) string {
	strs := strings.Split(providerID, "/")
	return strs[len(strs)-1]
}

// removes AWS secondary IPs that are currently assigned but not needed
func (r *ReconcileEgressIPAM) removeAWSUnusedIPs(client *ec2.EC2, nodeMap map[string]corev1.Node, assignedIPsByNode map[string][]string) error {
	for node, assidnedIPsToNode := range assignedIPsByNode {
		instance, err := getAWSInstance(client, nodeMap[node])
		if err != nil {
			log.Error(err, "unable to get aws instance for", "node", node)
			return err
		}
		awsAssignedIPs := []string{}
		for _, ipipas := range instance.NetworkInterfaces[0].PrivateIpAddresses {
			if !(*ipipas.Primary) {
				awsAssignedIPs = append(awsAssignedIPs, *ipipas.PrivateIpAddress)
			}
		}
		toBeRemovedIPs := strset.Difference(strset.New(awsAssignedIPs...), strset.New(assidnedIPsToNode...)).List()
		if len(toBeRemovedIPs) > 0 {
			log.Info("vm", "instance ", instance.InstanceId, " will be freed from IPs ", toBeRemovedIPs)
			toBeRemovedIPsAWSStr := []*string{}
			for _, ip := range toBeRemovedIPs {
				toBeRemovedIPsAWSStr = append(toBeRemovedIPsAWSStr, &ip)
			}

			input := &ec2.UnassignPrivateIpAddressesInput{
				NetworkInterfaceId: instance.NetworkInterfaces[0].NetworkInterfaceId,
				PrivateIpAddresses: toBeRemovedIPsAWSStr,
			}
			_, err = client.UnassignPrivateIpAddresses(input)
			if err != nil {
				log.Error(err, "unable to remove IPs from ", "instance", instance.InstanceId)
				return err
			}
		}
	}

	return nil
}

// assigns secondary IPs to AWS machines
func (r *ReconcileEgressIPAM) reconcileAWSAssignedIPs(client *ec2.EC2, nodeMap map[string]corev1.Node, assignedIPsByNode map[string][]string) error {
	for node, assidnedIPsToNode := range assignedIPsByNode {
		instance, err := getAWSInstance(client, nodeMap[node])
		if err != nil {
			log.Error(err, "unable to get aws instance for", "node", node)
			return err
		}
		awsAssignedIPs := []string{}
		for _, ipipas := range instance.NetworkInterfaces[0].PrivateIpAddresses {
			if !*ipipas.Primary {
				awsAssignedIPs = append(awsAssignedIPs, *ipipas.PrivateIpAddress)
			}
		}
		toBeAssignedIPs := strset.Difference(strset.New(assidnedIPsToNode...), strset.New(awsAssignedIPs...)).List()
		if len(toBeAssignedIPs) > 0 {
			log.Info("vm", "instance ", instance.InstanceId, " will be assigned IPs ", toBeAssignedIPs)
			toBeAssignedIPsAWSStr := []*string{}
			for _, ip := range toBeAssignedIPs {
				toBeAssignedIPsAWSStr = append(toBeAssignedIPsAWSStr, &ip)
			}

			input := &ec2.AssignPrivateIpAddressesInput{
				NetworkInterfaceId: instance.NetworkInterfaces[0].NetworkInterfaceId,
				PrivateIpAddresses: toBeAssignedIPsAWSStr,
			}
			_, err = client.AssignPrivateIpAddresses(input)
			if err != nil {
				log.Error(err, "unable to assigne IPs to ", "instance", instance.InstanceId)
				return err
			}
		}
	}
	return nil
}

func (r *ReconcileEgressIPAM) createAWSCredentialRequest() error {
	awsSpec := cloudcredentialv1.AWSProviderSpec{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "cloudcredential.openshift.io/v1",
			Kind:       "AWSProviderSpec",
		},
		StatementEntries: []cloudcredentialv1.StatementEntry{
			cloudcredentialv1.StatementEntry{
				Action: []string{
					"ec2:DescribeInstances",
					"ec2:UnassignPrivateIpAddresses",
					"ec2:AssignPrivateIpAddresses",
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
				Name:      aWSCredentialsSecretName,
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

func (r *ReconcileEgressIPAM) getAWSCredentials() (id string, key string, err error) {
	namespace, err := getOperatorNamespace()
	if err != nil {
		log.Error(err, "unable to get operator's namespace")
		return "", "", err
	}
	awsCredentialSecret := &corev1.Secret{}
	err = r.GetClient().Get(context.TODO(), types.NamespacedName{
		Name:      aWSCredentialsSecretName,
		Namespace: namespace,
	}, awsCredentialSecret)
	if err != nil {
		log.Error(err, "unable to retrive aws credential ", "secret", types.NamespacedName{
			Name:      aWSCredentialsSecretName,
			Namespace: namespace,
		})
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
