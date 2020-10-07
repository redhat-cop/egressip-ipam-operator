module github.com/redhat-cop/egressip-ipam-operator

go 1.14

require (
	github.com/aws/aws-sdk-go v1.35.3
	github.com/hashicorp/go-multierror v1.1.0
	github.com/jpillora/ipmath v0.0.0-20180121110145-ebede80a2ab9
	//0a191b5b9bb0ff4bf39e44be0fb4ffd7381766d3 pin to OCP 4.5
	github.com/openshift/api v0.0.0-20200917102736-0a191b5b9bb0
	//4ef74fd4ae81ff9eb4cdfa8805abf6775e4561d0 pin to OCP 4.5
	github.com/openshift/cloud-credential-operator v0.0.0-20200926024851-4ef74fd4ae81
	//d19e8d007f7cc19dc0daa7e61fe09ba8ecae3777 pin to OCP 4.5
	github.com/openshift/machine-api-operator v0.2.1-0.20200529045911-d19e8d007f7c
	github.com/operator-framework/operator-sdk v0.18.1
	github.com/redhat-cop/operator-utils v0.3.5
	github.com/scylladb/go-set v1.0.2
	github.com/spf13/pflag v1.0.5
	k8s.io/api v0.18.6
	k8s.io/apimachinery v0.18.6
	k8s.io/client-go v12.0.0+incompatible
	sigs.k8s.io/controller-runtime v0.6.2

)

replace (
	github.com/Azure/go-autorest => github.com/Azure/go-autorest v13.3.2+incompatible // Required by OLM
	k8s.io/client-go => k8s.io/client-go v0.18.2 // Required by prometheus-operator
)
