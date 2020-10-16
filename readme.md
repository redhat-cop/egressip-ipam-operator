# EgressIP IPAM Operator

[![Build Status](https://travis-ci.org/redhat-cop/egressip-ipam-operator.svg?branch=master)](https://travis-ci.org/redhat-cop/egressip-ipam-operator) [![Docker Repository on Quay](https://quay.io/repository/redhat-cop/egressip-ipam-operator/status "Docker Repository on Quay")](https://quay.io/repository/redhat-cop/egressip-ipam-operator)

This operator automates the assignment of egressIPs to namespaces.
Namespaces can opt in to receiving one or more egressIPs with the following annotation `egressip-ipam-operator.redhat-cop.io/egressipam:<egressIPAM>`, where `egressIPAM` is the CRD that controls how egressIPs are assigned.
IPs assigned to the namespace can be looked up in the following annotation: `egressip-ipam-operator.redhat-cop.io/egressips`.

EgressIP assignments is managed by the EgressIPAM CRD. Here is an example of it:

```yaml
apiVersion: redhatcop.redhat.io/v1alpha1
kind: EgressIPAM
metadata:
  name: example-egressipam
spec:
  cidrAssignments:
    - labelValue: "true"
      CIDR: 192.169.0.0/24
      reservedIPs:
      - "192.159.0.5"
  topologyLabel: egressGateway
  nodeSelector:
    matchLabels:
      node-role.kubernetes.io/worker: ""
```

This EgressIPAM specifies that all nodes that comply with the specified node selector and that also have labels `egressGateway: "true"` will be assigned egressIP from the specified CIDR.

Note that the `cidrAssigments` field is an array and therefore, multiple groups of nodes can be identified with the labelValue and different CIDRs can be assigned to them. This is usually not necessary on a bare metal deployment.

In the bare metal scenario, when this egressCRD is created, all the `hostsubnet` relative to the nodes selected by this EgressIPAM will be update to have the EgressCIDRs field equal to the specified CIDR (see below for cloud provider scenarios).

When a namespace is created with the opt-in annotation: `egressip-ipam-operator.redhat-cop.io/egressipam=<egressIPAM>`, an available egressIP is selected from the CIDR and assigned to the namespace.The `netnamespace` associated with this namespace is updated to use that egressIP.

It is possible to specify a set of reserved IPs. These IPs must belong to the CIDR and will never be assigned.

## Passing EgressIPs as input

The normal mode of operation of this operator is to pick a random IP from the configured CIDR. However, it also supports a scenario where egressIPs are picked by an external process and passed as input.

In this case IPs must me passed as an annotation to the namespace, like this: `egressip-ipam-operator.redhat-cop.io/egressips=IP1,IP2...`. The value of the annotation is a comma separated array of ip with no spaces.

There must be exactly one IP per CIDR defined in the referenced egressIPAM. Moreover, each IP must belong to the corresponding CIDR. Because this situation can lead to inconsistencies, failing to have correspondence between IPs in the namespace annotation and CIDRs in the egressIPAM CR will cause the operator to error out and stop processing all the namespaces associated with the given EgressIPAM CR.

It also also a responsibility of the progress picking the IPs to ensure that those IPs are actually available.

## Assumptions

The following assumptions apply when using this operator:

1. If multiple EgressIPAMs are defined, their selected nodes MUST NOT overlap.
2. When using a cloud provider the topology label MUST be `failure-domain.beta.kubernetes.io/zone`.

## Support for AWS

In AWS as well as other cloud providers, one cannot freely assign IPs to machines. Additional steps need to be performed in this case. Considering this EgressIPAM

```yaml
apiVersion: redhatcop.redhat.io/v1alpha1
kind: EgressIPAM
metadata:
  name: egressipam-aws
spec:
  cidrAssignments:
    - labelValue: "eu-central-1a"
      CIDR: 10.0.128.0/20
    - labelValue: "eu-central-1b"
      CIDR: 10.0.144.0/20
    - labelValue: "eu-central-1c"
      CIDR: 10.0.160.0/20
  topologyLabel: failure-domain.beta.kubernetes.io/zone
  nodeSelector:
    matchLabels:
      node-role.kubernetes.io/worker: ""
```

When a namespace with the opt-in annotation is created, the following happens:

1. for each of the CIDRs, an available IP is selected and assigned to the namespace
2. the relative `netnamespace` is update to reflect the assignment (multiple IPs will be assigned in this case).
3. one node per zone is selected to carry the egressIP
4. the relative aws machines are assigned the additional IP on the main interface (support for secondary interfaces in not available)
5. the relative `hostsubnets` are updated to reflect the assigned IP, the `egressIP` field is updated.

## Support for vSphere

Egress-ipam-operator treats vSphere as a bare metal installation, so it will work with a network setup in which secondary IPs can be added to the VMs with no intereaction with the vSphere API.
You can use the egressip-ipam-operator on your vSphere installation assuming that the nodes you want to use are labelled according to the `topologyLabel`. You can label them manually, or by changing the MachineSet configuration:

```yaml
apiVersion: machine.openshift.io/v1beta1
kind: MachineSet
metadata:
  {...}
spec:
  template:
    metadata:
      {...}
    spec:
      metadata:
        labels:
          egressGateway: 'true'
```

This example MachineSet object lets the [machine-api-operator](https://github.com/openshift/machine-api-operator) label all newly created nodes with the label `egressGateway: 'true'` which can then be selected by the egressip-ipam-operator with this example EgressIPAM configuration:

```yaml
apiVersion: redhatcop.redhat.io/v1alpha1
kind: EgressIPAM
metadata:
  name: egressipam-vsphere
spec:
  cidrAssignments:
    - labelValue: "true"
      CIDR: 192.169.0.0/24
      reservedIPs:
      - "192.159.0.5"
  topologyLabel: egressGateway
  nodeSelector:
    matchLabels:
      node-role.kubernetes.io/worker: ""
```

## Support for oVirt / Red Hat Virtualization

Egress-ipam-operator treats RHV as a bare metal installation, so it will work with a network setup in which secondary IPs can be added to the VMs with no intereaction with the RHV API. Refer to the vSphere section for more details.
Example EgressIPAM configuration:

```yaml
apiVersion: redhatcop.redhat.io/v1alpha1
kind: EgressIPAM
metadata:
  name: egressipam-rhv
spec:
  cidrAssignments:
    - labelValue: "true"
      CIDR: 192.169.0.0/24
      reservedIPs:
      - "192.159.0.5"
  topologyLabel: egressGateway
  nodeSelector:
    matchLabels:
      node-role.kubernetes.io/worker: ""
```

## Deploying the Operator

This is a cluster-level operator that you can deploy in any namespace, `egressip-ipam-operator` is recommended.

You can either deploy it using [`Helm`](https://helm.sh/) or creating the manifests directly.

### Deploying with Helm

Here are the instructions to install the latest release with Helm.

```shell
oc new-project egressip-ipam-operator

helm repo add egressip-ipam-operator https://redhat-cop.github.io/egressip-ipam-operator
helm repo update
export egressip_ipam_operator_chart_version=$(helm search repo egressip-ipam-operator/egressip-ipam-operator | grep egressip-ipam-operator/egressip-ipam-operator | awk '{print $2}')

helm fetch egressip-ipam-operator/egressip-ipam-operator --version ${egressip_ipam_operator_chart_version}
helm template egressip-ipam-operator-${egressip_ipam_operator_chart_version}.tgz --namespace egressip-ipam-operator | oc apply -f - -n egressip-ipam-operator

rm egressip-ipam-operator-${egressip_ipam_operator_chart_version}.tgz
```

### Deploying directly with manifests

Here are the instructions to install the latest release creating the manifest directly in OCP.

```shell
git clone git@github.com:redhat-cop/egressip-ipam-operator.git; cd egressip-ipam-operator
oc apply -f deploy/crds/redhatcop.redhat.io_egressipams_crd.yaml
oc new-project egressip-ipam-operator
oc -n egressip-ipam-operator apply -f deploy
```

## Local Development

Execute the following steps to develop the functionality locally. It is recommended that development be done using a cluster with `cluster-admin` permissions.

```shell
go mod download
```

optionally:

```shell
go mod vendor
```

Using the [operator-sdk](https://github.com/operator-framework/operator-sdk), run the operator locally:

```shell
oc apply -f deploy/crds/redhatcop.redhat.io_egressipams_crd.yaml
oc new-project egressip-ipam-operator
oc apply -f deploy/service_account.yaml -n egressip-ipam-operator
oc apply -f deploy/role.yaml -n egressip-ipam-operator
oc apply -f deploy/role_binding.yaml -n egressip-ipam-operator
export token=$(oc serviceaccounts get-token 'egressip-ipam-operator' -n egressip-ipam-operator)
oc login --token=${token}
OPERATOR_NAME='egressip-ipam-operator' NAMESPACE='egressip-ipam-operator' operator-sdk --verbose run --local --namespace "" --operator-flags="--zap-level=debug"
```

## Testing

### Baremetal test

```shell
oc apply -f test/egressIPAM-baremetal.yaml
oc apply -f test/namespace-baremetal.yaml

```

### AWS test

based on the output of the below command, configure your egressIPAM for AWS.

```shell
export region=$(oc get infrastructure cluster -o jsonpath='{.status.platformStatus.aws.region}')
for vmidurl in $(oc get nodes -l node-role.kubernetes.io/worker="" -o json | jq -r .items[].spec.providerID); do
  vmid=${vmidurl##*/}
  subnetid=$(aws ec2 --region ${region} describe-instances --instance-ids ${vmid} | jq -r .Reservations[0].Instances[0].NetworkInterfaces[0].SubnetId)
  echo $(aws ec2 --region ${region} describe-subnets --subnet-ids ${subnetid} | jq -r '.Subnets[0] | .CidrBlock + " " + .AvailabilityZone')
done
```  

```shell
oc apply -f test/egressIPAM-AWS.yaml
oc apply -f test/namespace-AWS.yaml
#test erroneous namespace
oc apply -f test/erroneous-namespace-AWS.yaml
```

Bulk test. Change numbers based on instance type and number of nodes [see also](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/using-eni.html#AvailableIpPerENI)

```shell
./test/test.sh create 15
```

to clean up

```shell
./test/test.sh delete 15
```

## Release Process

To release execute the following:

```shell
git tag -a "<version>" -m "release <version>"
git push upstream <version>
```

use this version format: vM.m.z
