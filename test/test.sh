#/bin/bash

#invoke with
# test.sh <command> <#namespaces>
# where command can be create or delete


test (){
  region=$(oc get infrastructure cluster -o jsonpath='{.status.platformStatus.aws.region}')
  subnetTagList=$(oc get machinesets -n openshift-machine-api -o json | jq -r .items[].spec.template.spec.providerSpec.value.subnet.filters[].values[])
  for tag in ${subnetTagList}; do 
    subnetId=$(aws ec2 --region ${region} describe-subnets --filters Name=tag:Name,Values=${tag}| jq -r .Subnets[].SubnetId)
    aws ec2 --region ${region} describe-network-interfaces --filters Name=subnet-id,Values=${subnetId} | jq .NetworkInterfaces[].PrivateIpAddresses[].PrivateIpAddress
  done  
}

create_namespaces () {
  COUNT=1
  while [ $COUNT -lt $1 ]; do
    echo "
      apiVersion: v1
      kind: Namespace
      metadata:
        annotations:
          egressip-ipam-operator.redhat-cop.io/egressipam: egressipam-aws
        name: ics-egressip-test${COUNT}
    " | oc apply -f -
    COUNT=`expr $COUNT + 1`
    sleep 10
  done
}

delete_namespaces () {
  COUNT=1
  while [ $COUNT -lt $1 ]; do
    oc delete namespace ics-egressip-test${COUNT}
    COUNT=`expr $COUNT + 1`
  done
}

if [ "create" == $1 ]; then
  create_namespaces $2
fi
if [ "delete" == $1 ]; then
  delete_namespaces $2
fi    