package engine

import (
	"fmt"
	"sort"
	"strconv"
	"stuntgres/internal/sqltype"
)

const OperatorClassID OID = 2617

type operatorDescriptor struct {
	address, namespace ObjectAddress
	revision           DefinitionRevision
	name               string
	left, right        OID
}

func (d *operatorDescriptor) Address() ObjectAddress       { return d.address }
func (d *operatorDescriptor) Namespace() ObjectAddress     { return d.namespace }
func (d *operatorDescriptor) Revision() DefinitionRevision { return d.revision }
func (d *operatorDescriptor) Name() string                 { return d.name }
func (*operatorDescriptor) Kind() ObjectKind               { return OperatorObject }
func (d *operatorDescriptor) signature() string {
	return strconv.FormatUint(uint64(d.left), 10) + "," + strconv.FormatUint(uint64(d.right), 10)
}
func (d *operatorDescriptor) seeded() bool {
	index := sort.Search(len(builtinOperators), func(i int) bool { return builtinOperators[i].oid >= d.address.ObjectID })
	if index == len(builtinOperators) {
		return false
	}
	row := builtinOperators[index]
	return row.oid == d.address.ObjectID && d.namespace == (ObjectAddress{ClassID: NamespaceClassID, ObjectID: PGCatalogNamespaceOID}) && row.name == d.name && row.left == d.left && row.right == d.right
}

type operatorProvider struct{}

func (operatorProvider) ClassID() OID { return OperatorClassID }
func (operatorProvider) Validate(v CatalogRead, descriptor Descriptor) error {
	d, ok := descriptor.(*operatorDescriptor)
	if !ok || d.address.ClassID != OperatorClassID || d.address.ObjectID == 0 || d.address.SubID != 0 || d.revision == 0 || d.name == "" || v.state.names[descriptorNameKey(d)] != d.address {
		return fmt.Errorf("stuntgres: invalid operator descriptor")
	}
	if _, ok := v.state.descriptors[d.namespace].(*namespaceDescriptor); !ok {
		return fmt.Errorf("stuntgres: invalid operator namespace")
	}
	for _, id := range []OID{d.left, d.right} {
		if id != 0 {
			if _, ok := v.LookupType(sqltype.ID(id)); !ok {
				return fmt.Errorf("stuntgres: invalid operator operand")
			}
		}
	}
	return nil
}
func (e *Engine) initializeOperators() {
	ns := ObjectAddress{ClassID: NamespaceClassID, ObjectID: PGCatalogNamespaceOID}
	for _, row := range builtinOperators {
		d := &operatorDescriptor{address: ObjectAddress{ClassID: OperatorClassID, ObjectID: row.oid}, namespace: ns, revision: 1, name: row.name, left: row.left, right: row.right}
		e.state.descriptors[d.address] = d
		e.state.names[descriptorNameKey(d)] = d.address
	}
}
