


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
export token=$(oc serviceaccounts get-token 'egressip-ipam-operator')
oc login --token=${token}
OPERATOR_NAME='egressip-ipam-operator' operator-sdk --verbose up local --namespace ""
```

## Testing


## Release Process

To release execute the following:

```shell
git tag -a "<version>" -m "release <version>"
git push upstream <version>
```

use this version format: vM.m.z