package egressipam

import (
	corev1 "k8s.io/api/core/v1"
	"net"
)

var _ Cloudprovider = &VsphereCloudProvider{}

// The VsphereCloudProvider is the strategy for managing the Vsphere specific things. Currently it is a Null Object.
type VsphereCloudProvider struct {
}

// TODO 2020-10-25 klenkes74 Implement VsphereCloudProvider

func (v VsphereCloudProvider) Initialize(_ *ReconcileEgressIPAM) error {
	log.V(2).Info("Initialize() is not implemented for this provider")
	return nil
}

func (v VsphereCloudProvider) Reconcile(_ *ReconcileContext) error {
	log.V(2).Info("Reconcile() is not implemented for this provider")
	return nil
}

func (v VsphereCloudProvider) AssignIPsToNamespace(_ *ReconcileContext, _ map[string][]net.IP) ([]corev1.Namespace, error) {
	log.V(2).Info("AssignIPsToNamespace() is not implemented for this provider")
	return nil, nil
}

func (v VsphereCloudProvider) CollectCloudData(_ *ReconcileContext) error {
	log.V(2).Info("CollectCloudData() is not implemented for this provider")
	return nil
}

func (v VsphereCloudProvider) manageCloudIPs(_ *ReconcileContext) error {
	log.V(2).Info("manageCloudIPs() is not implemented for this provider")
	return nil
}

func (v VsphereCloudProvider) getUsedIPs(_ *ReconcileContext) map[string][]net.IP {
	log.V(2).Info("getUsedIPs() is not implemented for this provider")
	return nil
}

func (v VsphereCloudProvider) CleanUpCloudProvider(_ *ReconcileContext) error {
	log.V(2).Info("CleanUpCloudProvider() is not implemented for this provider")
	return nil
}
