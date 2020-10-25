package egressipam

import (
	"context"
	errs "errors"
	machinev1beta1 "github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"
	"github.com/operator-framework/operator-sdk/pkg/k8sutil"
	"github.com/redhat-cop/operator-utils/pkg/util/apis"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"os"
	"reflect"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type OcpClient interface {
	// Initialize initializes the client
	Initialize() error

	// GetOperatorNamespace returns the namespace this operator is working on. It is primarily retrieved from the
	// k8sutil but has a fallback to the environment variable 'NAMESPACE'. If the fallback can't deliver a namespace
	// name it will throw an error.
	GetOperatorNamespace() (string, error)

	GetCredentialSecret() (*corev1.Secret, error)

	// ListMachineSets lists all machine sets defined or returns the error thrown by k8s while retrieving it.
	ListMachineSets(ctx context.Context) (*machinev1beta1.MachineSetList, error)

	// CreateOrUpdateResourceWithClient creates or updates the resource.
	CreateOrUpdateResource(owner apis.Resource, namespace string, obj apis.Resource) error
}

var _ OcpClient = &OcpClientImplementation{}

type OcpClientImplementation struct {
	credentials corev1.Secret

	ClientConfig *rest.Config
	Client       *client.Client
	Scheme       *runtime.Scheme
}

func (o *OcpClientImplementation) Initialize() error {
	c, err := client.New(o.ClientConfig, client.Options{})
	if err != nil {
		//goland:noinspection SpellCheckingInspection
		log.Error(err, "unable to create result", "with restconfig", o.ClientConfig)
		return err
	}
	o.Client = &c

	return nil
}

func (o *OcpClientImplementation) GetOperatorNamespace() (string, error) {
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

func (o *OcpClientImplementation) GetCredentialSecret() (*corev1.Secret, error) {
	if !reflect.DeepEqual(o.credentials, corev1.Secret{}) {
		return &o.credentials, nil
	}
	namespace, err := o.GetOperatorNamespace()
	if err != nil {
		log.Error(err, "unable to get operator's namespace")
		return &corev1.Secret{}, err
	}
	credentialSecret := &corev1.Secret{}

	err = (*o.Client).Get(context.TODO(), types.NamespacedName{
		Name:      CredentialsSecretName,
		Namespace: namespace,
	}, credentialSecret)
	if err != nil {
		log.Error(err, "unable to retrieve aws credential ", "secret", types.NamespacedName{
			Name:      CredentialsSecretName,
			Namespace: namespace,
		})
		return &corev1.Secret{}, err
	}
	o.credentials = *credentialSecret
	return &o.credentials, nil
}

func (o *OcpClientImplementation) ListMachineSets(ctx context.Context) (*machinev1beta1.MachineSetList, error) {
	result := &machinev1beta1.MachineSetList{}
	err := (*o.Client).List(ctx, result)

	return result, err
}

func (o *OcpClientImplementation) CreateOrUpdateResource(owner apis.Resource, namespace string, obj apis.Resource) error {
	if owner != nil {
		_ = controllerutil.SetControllerReference(owner, obj, o.Scheme)
	}
	if namespace != "" {
		obj.SetNamespace(namespace)
	}

	obj2 := obj.DeepCopyObject()

	err := (*o.Client).Get(context.TODO(), types.NamespacedName{
		Namespace: obj.GetNamespace(),
		Name:      obj.GetName(),
	}, obj2)

	if apierrors.IsNotFound(err) {
		err = (*o.Client).Create(context.TODO(), obj)
		if err != nil {
			log.Error(err, "unable to create object", "object", obj)
			return err
		}
		return nil
	}
	if err == nil {
		obj3, ok := obj2.(metav1.Object)
		if !ok {
			err := errs.New("unable to convert to metav1.Object")
			log.Error(err, "unable to convert to metav1.Object", "object", obj2)
			return err
		}
		obj.SetResourceVersion(obj3.GetResourceVersion())
		err = (*o.Client).Update(context.TODO(), obj)
		if err != nil {
			log.Error(err, "unable to update object", "object", obj)
			return err
		}
		return nil

	}
	log.Error(err, "unable to lookup object", "object", obj)
	return err
}
