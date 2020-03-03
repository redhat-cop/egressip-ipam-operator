package controller

import (
	"github.com/redhat-cop/egressip-ipam-operator/pkg/controller/egressipam"
)

func init() {
	// AddToManagerFuncs is a list of functions to create controllers and add them to a manager.
	AddToManagerFuncs = append(AddToManagerFuncs, egressipam.Add)
}
