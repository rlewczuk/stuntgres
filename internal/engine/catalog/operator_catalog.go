package catalog

import (
	"strconv"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

type Operator struct {
	ObjectAddress, ObjectNamespace                           ids.ObjectAddress
	Extension                                                ids.ObjectAddress
	Origin                                                   *InstalledOrigin
	ObjectRevision                                           ids.DefinitionRevision
	ObjectName                                               string
	Left, Right                                              ids.OID
	Owner                                                    authorization.PrincipalRef
	Result, Function, Commutator, Negator, Restriction, Join ids.OID
	Hash, Merge                                              bool
}

func (d *Operator) Address() ids.ObjectAddress { return d.ObjectAddress }

func (d *Operator) Namespace() ids.ObjectAddress { return d.ObjectNamespace }

func (d *Operator) Revision() ids.DefinitionRevision { return d.ObjectRevision }

func (d *Operator) Name() string { return d.ObjectName }

func (*Operator) Kind() ids.ObjectKind { return ids.OperatorObject }

func (d *Operator) signature() string {
	return strconv.FormatUint(uint64(d.Left), 10) + "," + strconv.FormatUint(uint64(d.Right), 10)
}
