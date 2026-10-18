package engine

import (
	"fmt"
	"sort"
	"strconv"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func operatorSeeded(d *operatorDescriptor) bool {
	index := sort.Search(len(builtinOperators), func(i int) bool { return builtinOperators[i].oid >= d.Address().ObjectID })
	if index == len(builtinOperators) {
		return false
	}
	row := builtinOperators[index]
	return row.oid == d.Address().ObjectID && d.Namespace() == (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}) && row.name == d.Name() && row.left == d.Left && row.right == d.Right
}

type operatorProvider struct{}

func (operatorProvider) ClassID() ids.OID { return ids.OperatorClassID }
func (operatorProvider) Validate(v CatalogRead, descriptor Descriptor) error {
	d, ok := descriptor.(*operatorDescriptor)
	if !ok || d.Address().ClassID != ids.OperatorClassID || d.Address().ObjectID == 0 || d.Address().SubID != 0 || d.Revision() == 0 || d.Name() == "" || v.NameAddress(catalog.DescriptorNameKey(d)) != d.Address() {
		return fmt.Errorf("stuntgres: invalid operator descriptor")
	}
	if _, ok := v.Namespace(d.Namespace()); !ok {
		return fmt.Errorf("stuntgres: invalid operator namespace")
	}
	for _, id := range []ids.OID{d.Left, d.Right} {
		if id != 0 {
			if _, ok := v.LookupType(sqltype.ID(id)); !ok {
				return fmt.Errorf("stuntgres: invalid operator operand")
			}
		}
	}
	if d.Right == 0 || (d.Function == 0) != (d.Result == 0) {
		return fmt.Errorf("stuntgres: invalid operator definition")
	}
	seeded := operatorSeeded(d)
	if seeded {
		// Released metadata can refer to lookup-only routine identities. It is
		// not a declaration of a callable implementation provider.
		index := sort.Search(len(supportedOperatorMetadata), func(i int) bool { return supportedOperatorMetadata[i].oid >= d.Address().ObjectID })
		var result, function ids.OID
		if index < len(supportedOperatorMetadata) && supportedOperatorMetadata[index].oid == d.Address().ObjectID {
			row := supportedOperatorMetadata[index]
			result, function = ids.OID(row.result), ids.OID(row.procedure)
		}
		if d.Result != result || d.Function != function {
			return fmt.Errorf("stuntgres: invalid seeded operator definition")
		}
	} else if !validOperatorName(d.Name()) {
		return fmt.Errorf("stuntgres: invalid operator name")
	}
	if d.Function != 0 {
		if !seeded {
			procedure, ok := v.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: d.Function})
			if !ok {
				return fmt.Errorf("stuntgres: invalid operator implementation")
			}
			signature := strconv.FormatUint(uint64(d.Right), 10)
			if d.Left != 0 {
				signature = strconv.FormatUint(uint64(d.Left), 10) + "," + signature
			}
			if procedure.Signature != procedureSignature(signature) || procedure.Result != d.Result || procedure.RoutineKind != functionRoutine {
				return fmt.Errorf("stuntgres: invalid operator function signature")
			}
		}
		if _, ok := v.LookupType(sqltype.ID(d.Result)); !ok {
			return fmt.Errorf("stuntgres: invalid operator result")
		}
	}
	if !seeded {
		for i, id := range [...]ids.OID{d.Commutator, d.Negator} {
			if id == 0 {
				continue
			}
			peer, ok := v.Operator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: id})
			left, right := d.Right, d.Left
			if i == 1 {
				left, right = d.Left, d.Right
			}
			if !ok || peer.Left != left || peer.Right != right {
				return fmt.Errorf("stuntgres: invalid operator link")
			}
			if i == 1 && (id == d.Address().ObjectID || d.Result != 0 && d.Result != ids.OID(sqltype.Bool)) {
				return fmt.Errorf("stuntgres: invalid operator negator")
			}
		}
	}
	return nil
}
func (e *Engine) initializeOperators() {
	e.catalog.state.SetOperatorNames(seededOperatorNames)
	ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	for _, row := range builtinOperators {
		d := &operatorDescriptor{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: row.oid}, ObjectNamespace: ns, ObjectRevision: 1, ObjectName: row.name, Left: row.left, Right: row.right, Owner: e.authorizationDomain.Principal(authorization.BootstrapPrincipalID)}
		e.catalog.state.SetDescriptor(d.Address(), d)
		e.catalog.state.BindName(catalog.DescriptorNameKey(d), d.Address())
	}
	for _, row := range supportedOperatorMetadata {
		d := e.catalog.state.MustOperator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: row.oid})
		d.Result, d.Function = ids.OID(row.result), ids.OID(row.procedure)
		d.Commutator, d.Negator = ids.OID(row.commutator), ids.OID(row.negator)
		d.Restriction, d.Join = ids.OID(row.restriction), ids.OID(row.join)
		d.Hash, d.Merge = row.hash, row.merge
	}
}
