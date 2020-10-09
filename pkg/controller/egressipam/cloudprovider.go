package egressipam

import "net"

const (
	// CredentialsSecretName is the cloud independent name of the k8s secret with the credentials
	CredentialsSecretName = "egress-ipam-operator-cloud-credentials"
)

type Cloudprovider interface {
	// FailureRegion defines the failure region (in AWS "Region") of the cluster within the cloud providers infrastructure.
	FailureRegion(region string) error

	// CreateCredentialRequest will create a CredentialOperator request for the cloud provider
	CreateCredentialRequest() error

	// HarvestCloudData collects the needed data from the cloud provider or throws an error.
	HarvestCloudData(rc *ReconcileContext) error

	// ManageCloudIPs will remove the unassigned IPs and assign the newly assigned IPs on the cloud. Will return an
	// error for the very first error.
	ManageCloudIPs(rc *ReconcileContext) error

	GetUsedIPs(rc *ReconcileContext) map[string][]net.IP

	RemoveAssignedIPs(rc *ReconcileContext) error
}
