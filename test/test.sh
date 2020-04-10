#/bin/bash
COUNT=1
while [ $COUNT -lt 40 ]; do

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