package v1alpha1

import (
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
}

type CIDRAssignment struct {
	// +kubebuilder:validation:Required
	CIDR string `json:"CIDR"`

	// +kubebuilder:validation:Required
	LabelValue string `json:"labelValue"`
}

// EgressIPAMStatus defines the observed state of EgressIPAM
type EgressIPAMStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book-v1.book.kubebuilder.io/beyond_basics/generating_crd.html
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
