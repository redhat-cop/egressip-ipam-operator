module github.com/redhat-cop/egressip-ipam-operator

go 1.16

require (
	github.com/Azure/azure-sdk-for-go v49.2.0+incompatible
	github.com/Azure/go-autorest/autorest v0.11.12
	github.com/Azure/go-autorest/autorest/adal v0.9.5
	github.com/Azure/go-autorest/autorest/to v0.4.0 // indirect
	github.com/Azure/go-autorest/autorest/validation v0.3.0 // indirect
	github.com/aws/aws-sdk-go v1.36.15
	github.com/go-logr/logr v0.4.0
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/hashicorp/go-multierror v1.1.1
	github.com/jpillora/ipmath v0.0.0-20180121110145-ebede80a2ab9
	github.com/onsi/ginkgo v1.16.4
	github.com/onsi/gomega v1.13.0
	//0a191b5b9bb0ff4bf39e44be0fb4ffd7381766d3 pin to OCP 4.5
	github.com/openshift/api v0.0.0-20200917102736-0a191b5b9bb0
	//4ef74fd4ae81ff9eb4cdfa8805abf6775e4561d0 pin to OCP 4.5
	github.com/openshift/cloud-credential-operator v0.0.0-20200926024851-4ef74fd4ae81
	//d19e8d007f7cc19dc0daa7e61fe09ba8ecae3777 pin to OCP 4.5
	github.com/openshift/machine-api-operator v0.2.1-0.20200529045911-d19e8d007f7c
	github.com/prometheus/client_golang v1.11.0
	github.com/redhat-cop/operator-utils v1.1.4
	github.com/scylladb/go-set v1.0.2
	golang.org/x/oauth2 v0.0.0-20200902213428-5d25da1a8d43 // indirect
	k8s.io/api v0.21.2
	k8s.io/apimachinery v0.21.2
	k8s.io/client-go v0.21.2
	sigs.k8s.io/controller-runtime v0.9.2
)
