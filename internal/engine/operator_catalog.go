package engine

import (
	"fmt"
	"sort"
	"strconv"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type operatorDescriptor struct {
	address, namespace                                       ids.ObjectAddress
	revision                                                 ids.DefinitionRevision
	name                                                     string
	left, right                                              ids.OID
	owner                                                    authorization.PrincipalRef
	result, function, commutator, negator, restriction, join ids.OID
	hash, merge                                              bool
}

func (d *operatorDescriptor) Address() ids.ObjectAddress       { return d.address }
func (d *operatorDescriptor) Namespace() ids.ObjectAddress     { return d.namespace }
func (d *operatorDescriptor) Revision() ids.DefinitionRevision { return d.revision }
func (d *operatorDescriptor) Name() string                     { return d.name }
func (*operatorDescriptor) Kind() ids.ObjectKind               { return ids.OperatorObject }
func (d *operatorDescriptor) signature() string {
	return strconv.FormatUint(uint64(d.left), 10) + "," + strconv.FormatUint(uint64(d.right), 10)
}
func (d *operatorDescriptor) seeded() bool {
	index := sort.Search(len(builtinOperators), func(i int) bool { return builtinOperators[i].oid >= d.address.ObjectID })
	if index == len(builtinOperators) {
		return false
	}
	row := builtinOperators[index]
	return row.oid == d.address.ObjectID && d.namespace == (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}) && row.name == d.name && row.left == d.left && row.right == d.right
}

type operatorProvider struct{}

func (operatorProvider) ClassID() ids.OID { return ids.OperatorClassID }
func (operatorProvider) Validate(v CatalogRead, descriptor Descriptor) error {
	d, ok := descriptor.(*operatorDescriptor)
	if !ok || d.address.ClassID != ids.OperatorClassID || d.address.ObjectID == 0 || d.address.SubID != 0 || d.revision == 0 || d.name == "" || v.NameAddress(descriptorNameKey(d)) != d.address {
		return fmt.Errorf("stuntgres: invalid operator descriptor")
	}
	if _, ok := v.Namespace(d.namespace); !ok {
		return fmt.Errorf("stuntgres: invalid operator namespace")
	}
	for _, id := range []ids.OID{d.left, d.right} {
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
		var result, function ids.OID
		if index < len(supportedOperatorMetadata) && supportedOperatorMetadata[index].oid == d.address.ObjectID {
			row := supportedOperatorMetadata[index]
			result, function = ids.OID(row.result), ids.OID(row.procedure)
		}
		if d.result != result || d.function != function {
			return fmt.Errorf("stuntgres: invalid seeded operator definition")
		}
	} else if !validOperatorName(d.name) {
		return fmt.Errorf("stuntgres: invalid operator name")
	}
	if d.function != 0 {
		if !seeded {
			procedure, ok := v.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: d.function})
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
		for i, id := range [...]ids.OID{d.commutator, d.negator} {
			if id == 0 {
				continue
			}
			peer, ok := v.Operator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: id})
			left, right := d.right, d.left
			if i == 1 {
				left, right = d.left, d.right
			}
			if !ok || peer.left != left || peer.right != right {
				return fmt.Errorf("stuntgres: invalid operator link")
			}
			if i == 1 && (id == d.address.ObjectID || d.result != 0 && d.result != ids.OID(sqltype.Bool)) {
				return fmt.Errorf("stuntgres: invalid operator negator")
			}
		}
	}
	return nil
}
func (e *Engine) initializeOperators() {
	e.catalog.state.operatorNames = seededOperatorNames
	ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	for _, row := range builtinOperators {
		d := &operatorDescriptor{address: ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: row.oid}, namespace: ns, revision: 1, name: row.name, left: row.left, right: row.right, owner: e.authorizationDomain.Principal(authorization.BootstrapPrincipalID)}
		e.catalog.state.setDescriptor(d.address, d)
		e.catalog.state.bindName(descriptorNameKey(d), d.address)
	}
	for _, row := range supportedOperatorMetadata {
		d := e.catalog.state.MustOperator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: row.oid})
		d.result, d.function = ids.OID(row.result), ids.OID(row.procedure)
		d.commutator, d.negator = ids.OID(row.commutator), ids.OID(row.negator)
		d.restriction, d.join = ids.OID(row.restriction), ids.OID(row.join)
		d.hash, d.merge = row.hash, row.merge
	}
}
