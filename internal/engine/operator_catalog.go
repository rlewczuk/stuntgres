package engine

import (
	"fmt"
	"sort"
	"strconv"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqltype"
)

type operatorDescriptor struct {
	address, namespace                                       ObjectAddress
	revision                                                 DefinitionRevision
	name                                                     string
	left, right                                              OID
	owner                                                    authorization.PrincipalRef
	result, function, commutator, negator, restriction, join OID
	hash, merge                                              bool
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
	if d.right == 0 || (d.function == 0) != (d.result == 0) {
		return fmt.Errorf("stuntgres: invalid operator definition")
	}
	seeded := d.seeded()
	if seeded {
		// Released metadata can refer to lookup-only routine identities. It is
		// not a declaration of a callable implementation provider.
		index := sort.Search(len(supportedOperatorMetadata), func(i int) bool { return supportedOperatorMetadata[i].oid >= d.address.ObjectID })
		var result, function OID
		if index < len(supportedOperatorMetadata) && supportedOperatorMetadata[index].oid == d.address.ObjectID {
			row := supportedOperatorMetadata[index]
			result, function = OID(row.result), OID(row.procedure)
		}
		if d.result != result || d.function != function {
			return fmt.Errorf("stuntgres: invalid seeded operator definition")
		}
	} else if !validOperatorName(d.name) {
		return fmt.Errorf("stuntgres: invalid operator name")
	}
	if d.function != 0 {
		if !seeded {
			procedure, ok := v.state.descriptors[ObjectAddress{ClassID: ProcedureClassID, ObjectID: d.function}].(*procedureDescriptor)
			if !ok {
				return fmt.Errorf("stuntgres: invalid operator implementation")
			}
			signature := strconv.FormatUint(uint64(d.right), 10)
			if d.left != 0 {
				signature = strconv.FormatUint(uint64(d.left), 10) + "," + signature
			}
			if procedure.signature != procedureSignature(signature) || procedure.result != d.result || procedure.routineKind != functionRoutine {
				return fmt.Errorf("stuntgres: invalid operator function signature")
			}
		}
		if _, ok := v.LookupType(sqltype.ID(d.result)); !ok {
			return fmt.Errorf("stuntgres: invalid operator result")
		}
	}
	if !seeded {
		for i, id := range [...]OID{d.commutator, d.negator} {
			if id == 0 {
				continue
			}
			peer, ok := v.state.descriptors[ObjectAddress{ClassID: OperatorClassID, ObjectID: id}].(*operatorDescriptor)
			left, right := d.right, d.left
			if i == 1 {
				left, right = d.left, d.right
			}
			if !ok || peer.left != left || peer.right != right {
				return fmt.Errorf("stuntgres: invalid operator link")
			}
			if i == 1 && (id == d.address.ObjectID || d.result != 0 && d.result != OID(sqltype.Bool)) {
				return fmt.Errorf("stuntgres: invalid operator negator")
			}
		}
	}
	return nil
}
func (e *Engine) initializeOperators() {
	e.state.operatorNames = seededOperatorNames
	ns := ObjectAddress{ClassID: NamespaceClassID, ObjectID: PGCatalogNamespaceOID}
	for _, row := range builtinOperators {
		d := &operatorDescriptor{address: ObjectAddress{ClassID: OperatorClassID, ObjectID: row.oid}, namespace: ns, revision: 1, name: row.name, left: row.left, right: row.right, owner: e.authorizationDomain.Principal(authorization.BootstrapPrincipalID)}
		e.state.descriptors[d.address] = d
		e.state.names[descriptorNameKey(d)] = d.address
	}
	for _, row := range supportedOperatorMetadata {
		d := e.state.descriptors[ObjectAddress{ClassID: OperatorClassID, ObjectID: row.oid}].(*operatorDescriptor)
		d.result, d.function = OID(row.result), OID(row.procedure)
		d.commutator, d.negator = OID(row.commutator), OID(row.negator)
		d.restriction, d.join = OID(row.restriction), OID(row.join)
		d.hash, d.merge = row.hash, row.merge
	}
}
