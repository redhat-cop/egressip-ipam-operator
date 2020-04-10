COUNT=1
while [ $COUNT -lt 40 ]; do

oc delete namespace ics-egressip-test${COUNT}

COUNT=`expr $COUNT + 1`

done