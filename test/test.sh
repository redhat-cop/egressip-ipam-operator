#/bin/bash

#invoke with
# test.sh <command> <#namespaces>
# where command can be create or delete


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