# EgressIP IPAM Operator

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
  cidrAssignment:
    - labelValue: "true"
      CIDR: 192.169.0.0/24
  nodeLabel: egressGateway
  nodeSelector:
    node-role.kubernetes.io/worker: ""
```

This EgressIPAM specifies that all nodes that comply with the specified node selector and that also have labels `egressGateway: "true"` will be assigned egressIP from the specified CIDR.

Note that the `cidrAssigment` field is an array and therefore, multiple groups of nodes can be identified with the labelValue and different CIDRs can be assigned to them. This is usually not necessary on a bare metal deployment.

In the bare metal scenario, when this egressCRD is created, all the `hostsubnet` relative to the nodes selected by this EgressIPAM will be update to have the EgressCIDRs field equal to the specified CIDR (see below for cloud provider scenarios).

When a namespace is created with the opt-in annotation: `egressip-ipam-operator.redhat-cop.io/egressipam=<egressIPAM>`, an available egressIP is selected from the CIDR and assigned to the namespace.The `netnamespace` associated with this namespace is updated to use that egressIP.

## Passing EgressIPs as input

The normal mode of operatio of this operator is to pick a random IP from the configured CIDR. However it also support a scenario where egressIP are pciked by an exteanl process and passed as input.

In this case IPs must me passed as an annotation to the namespace, like this: `egressip-ipam-operator.redhat-cop.io/egressips=IP1,IP2...`. The value of the annotation is a comma separated array of ip with no spaces.

There must be exactly one IP per CIDR defined in the referenced egressIPAM. Moreover each IP must belong to the corresponding CIDR. It also also a responsibility of the progress picking the IPs to ensure that those IPs are actually available.

## Assumptions

The following assumptions apply when using this operator:

1. if multiple egressIPAM are defined, their selected nodes MUST NOT overlap
2. when using cloud provider the topological label MUST be `topology.kubernetes.io/zone`

## Support for AWS

In AWS as well as other cloud providers, one cannot freely assign IPs to machines. Additional steps need to be performed in this case. Considering this EgressIPAM

```yaml
apiVersion: redhatcop.redhat.io/v1alpha1
kind: EgressIPAM
metadata:
  name: egressipam-aws
spec:
  cidrAssignment:
    - labelValue: "eu-central-1a"
      CIDR: 10.0.128.0/20
    - labelValue: "eu-central-1b"
      CIDR: 10.0.144.0/20
    - labelValue: "eu-central-1c"
      CIDR: 10.0.160.0/20
  nodeLabel: topology.kubernetes.io/zone
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
OPERATOR_NAME='egressip-ipam-operator' NAMESPACE='egressip-ipam-operator' operator-sdk --verbose up local --namespace ""
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
for vmidurl in $(oc get nodes -l node-role.kubernetes.io/worker="" -o json | jq -r .items[].spec.providerID); do
  vmid=${vmidurl##*/}
  subnetid=$(aws ec2 --region eu-central-1 describe-instances --instance-ids ${vmid} | jq -r .Reservations[0].Instances[0].NetworkInterfaces[0].SubnetId)
  echo $(aws ec2 --region eu-central-1 describe-subnets --subnet-ids ${subnetid} | jq -r '.Subnets[0] | .CidrBlock + " " + .AvailabilityZone')
done
```  

```shell
oc apply -f test/egressIPAM-AWS.yaml
oc apply -f test/namespace-AWS.yaml
```

## Release Process

To release execute the following:

```shell
git tag -a "<version>" -m "release <version>"
git push upstream <version>
```

use this version format: vM.m.z