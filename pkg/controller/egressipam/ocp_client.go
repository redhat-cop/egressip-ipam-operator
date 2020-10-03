package egressipam

import (
	"context"
	errs "errors"
	machinev1beta1 "github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"
	"github.com/operator-framework/operator-sdk/pkg/k8sutil"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"os"
	"reflect"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type OcpClient interface {
	// GetOperatorNamespace returns the namespace this operator is working on. It is primarily retrieved from the
	// k8sutil but has a failback to the environment variable 'NAMESPACE'. If the fallback can't deliver a namespace
	// name it will throw an error.
	GetOperatorNamespace() (string, error)

	GetCredentialSecret() (*corev1.Secret, error)

	// ListMachineSets lists all machine sets defined or returns the error thrown by k8s while retrieving it.
	ListMachineSets(ctx context.Context) (*machinev1beta1.MachineSetList, error)
}

var _ OcpClient = &OcpClientImplementation{}

type OcpClientImplementation struct {
	creds corev1.Secret

	client *client.Client
}

func (ocp *OcpClientImplementation) GetOperatorNamespace() (string, error) {
	namespace, err := k8sutil.GetOperatorNamespace()
	if err != nil {
		namespace, ok := os.LookupEnv("NAMESPACE")
		if !ok {
			return "", errs.New("unable to infer namespace in which operator is running")
		}
		return namespace, nil
	}
	return namespace, nil
}

func (ocp *OcpClientImplementation) GetCredentialSecret() (*corev1.Secret, error) {
	if !reflect.DeepEqual(ocp.creds, corev1.Secret{}) {
		return &ocp.creds, nil
	}
	namespace, err := ocp.GetOperatorNamespace()
	if err != nil {
		log.Error(err, "unable to get operator's namespace")
		return &corev1.Secret{}, err
	}
	credentialSecret := &corev1.Secret{}
	err = (*ocp.client).Get(context.TODO(), types.NamespacedName{
		Name:      CredentialsSecretName,
		Namespace: namespace,
	}, credentialSecret)
	if err != nil {
		log.Error(err, "unable to retrive aws credential ", "secret", types.NamespacedName{
			Name:      CredentialsSecretName,
			Namespace: namespace,
		})
		return &corev1.Secret{}, err
	}
	ocp.creds = *credentialSecret
	return &ocp.creds, nil
}

func (ocp *OcpClientImplementation) ListMachineSets(ctx context.Context) (*machinev1beta1.MachineSetList, error) {
	result := &machinev1beta1.MachineSetList{}
	err := (*ocp.client).List(ctx, result)

	return result, err
}
