package v1alpha1

import (
	"github.com/redhat-cop/operator-utils/pkg/util/apis"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// EgressIPAMSpec defines the desired state of EgressIPAM
type EgressIPAMSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book-v1.book.kubebuilder.io/beyond_basics/generating_crd.html

	// +kubebuilder:validation:Optional
	// +listType=map
	// +listMapKey=CIDR
	CIDRAssignments []CIDRAssignment `json:"cidrAssignment,omitempty"`

	// +kubebuilder:validation:Required
	NodeLabel string `json:"nodeLabel"`

	// +kubebuilder:validation:Optional
	NodeSelector metav1.LabelSelector `json:"nodeSelector,omitempty"`
}

type CIDRAssignment struct {
	// +kubebuilder:validation:Required
	// TODO this is not working...
	// kubebuilder:validation:Pattern=^(([0-9]|[1-9][0-9]|1[0-9]{2}|2[0-4][0-9]|25[0-5])\.){3}([0-9]|[1-9][0-9]|1[0-9]{2}|2[0-4][0-9]|25[0-5])(\/(3[0-2]|[1-2][0-9]|[0-9]))$
	// kubebuilder:validation:Pattern=^((25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.){3}(25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)(/(3[0-2]|2[0-9]|1[0-9]|[0-9]))?$
	CIDR string `json:"CIDR"`

	// +kubebuilder:validation:Required
	LabelValue string `json:"labelValue"`
}

// EgressIPAMStatus defines the observed state of EgressIPAM
type EgressIPAMStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book-v1.book.kubebuilder.io/beyond_basics/generating_crd.html
	apis.ReconcileStatus `json:",inline"`
}

func (m *EgressIPAM) GetReconcileStatus() apis.ReconcileStatus {
	return m.Status.ReconcileStatus
}

func (m *EgressIPAM) SetReconcileStatus(reconcileStatus apis.ReconcileStatus) {
	m.Status.ReconcileStatus = reconcileStatus
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// EgressIPAM is the Schema for the egressipams API
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=egressipams,scope=Cluster
type EgressIPAM struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   EgressIPAMSpec   `json:"spec,omitempty"`
	Status EgressIPAMStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// EgressIPAMList contains a list of EgressIPAM
type EgressIPAMList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []EgressIPAM `json:"items"`
}

func init() {
	SchemeBuilder.Register(&EgressIPAM{}, &EgressIPAMList{})
}
