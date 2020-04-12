package egressipam

import (
	"context"

	redhatcopv1alpha1 "github.com/redhat-cop/egressip-ipam-operator/pkg/apis/redhatcop/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *ReconcileEgressIPAM) getAllEgressIPAM() ([]redhatcopv1alpha1.EgressIPAM, error) {
	egressIPAMList := &redhatcopv1alpha1.EgressIPAMList{}
	err := r.GetClient().List(context.TODO(), egressIPAMList, &client.ListOptions{})
	if err != nil {
		log.Error(err, "unable to list all EgressIPAM resources")
		return []redhatcopv1alpha1.EgressIPAM{}, err
	}
	return egressIPAMList.Items, nil
}
