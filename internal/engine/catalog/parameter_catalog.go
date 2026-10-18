package catalog

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

// A parameter descriptor represents an ACL, not a setting value or provider.
// Baseline-equivalent ACLs have no catalog row.
type Parameter struct {
	ObjectAddress  ids.ObjectAddress
	ObjectRevision ids.DefinitionRevision
	ObjectName     string
	Owner          authorization.PrincipalRef
	ACL            authorization.ACLValue
}

func (d *Parameter) Address() ids.ObjectAddress { return d.ObjectAddress }

func (d *Parameter) Revision() ids.DefinitionRevision { return d.ObjectRevision }

func (*Parameter) Namespace() ids.ObjectAddress { return ids.ObjectAddress{} }

func (d *Parameter) Name() string { return d.ObjectName }

func (*Parameter) Kind() ids.ObjectKind { return ids.ParameterObject }
