package egressipam

import (
	"context"
	"errors"
	"net"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	ocpconfigv1 "github.com/openshift/api/config/v1"
	cloudcredentialv1 "github.com/openshift/cloud-credential-operator/pkg/apis/cloudcredential/v1"
	redhatcopv1alpha1 "github.com/redhat-cop/egressip-ipam-operator/pkg/apis/redhatcop/v1alpha1"
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

// returns nodes selected by this egressIPAM sorted by the CIDR
func (r *ReconcileEgressIPAM) getAWSAssignedIPsByNode(egressIPAM *redhatcopv1alpha1.EgressIPAM) (map[*corev1.Node][]net.IP, error) {
	//the CIDR and egressIPAM can be used to lookup the AZ.
	return map[*corev1.Node][]net.IP{}, errors.New("not implemented")
}

// removes AWS secondary IPs that are currently assigned but not needed
func (r *ReconcileEgressIPAM) removeAWSUnusedIPs(awsAssignedIPsByNode map[*corev1.Node][]net.IP, assignedIPsByNode map[string][]net.IP) error {
	return errors.New("not implemented")
}

// assigns secondary IPs to AWS machines
func (r *ReconcileEgressIPAM) reconcileAWSAssignedIPs(assignedIPsByNode map[string][]net.IP, egressIPAM *redhatcopv1alpha1.EgressIPAM) error {
	return errors.New("not implemented")
}

func (r *ReconcileEgressIPAM) createAWSCredentialRequest() error {
	awsSpec := cloudcredentialv1.AWSProviderSpec{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "cloudcredential.openshift.io/v1",
			Kind:       "AWSProviderSpec",
		},
		StatementEntries: []cloudcredentialv1.StatementEntry{},
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

	awsAccessKeyId, ok := awsCredentialSecret.Data["aws_access_key_id"]
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

	return string(awsAccessKeyId), string(awsSecretAccessKey), nil
}
