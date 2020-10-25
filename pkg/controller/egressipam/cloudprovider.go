package egressipam

import (
	corev1 "k8s.io/api/core/v1"
	"net"
)

const (
	// CredentialsSecretName is the cloud independent name of the k8s secret with the credentials
	CredentialsSecretName = "egress-ipam-operator-cloud-credentials"
)

type Cloudprovider interface {
	// Initialize the cloud provider.
	Initialize(r *ReconcileEgressIPAM) error

	// Reconcile is the cloud provider specific reconciliation. It may call methods on the generic reconciler so it
	// needs the reconciler as parameter.
	Reconcile(rc *ReconcileContext) error

	// CollectCloudData collects the needed data from the cloud provider or throws an error.
	CollectCloudData(rc *ReconcileContext) error

	// AssignIPsToNamespace adds new IPs to the nodes.
	AssignIPsToNamespace(rc *ReconcileContext, IPsByCIDR map[string][]net.IP) ([]corev1.Namespace, error)

	// CleanUpCloudProvider removes the old IPs from the cloud provider. It is called from the generic clean up logic.
	CleanUpCloudProvider(rc *ReconcileContext) error
}
