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

func operatorSeeded(d *catalog.Operator) bool {
	index := sort.Search(len(builtinOperators), func(i int) bool { return builtinOperators[i].oid >= d.Address().ObjectID })
	if index == len(builtinOperators) {
		return false
	}
	row := builtinOperators[index]
	return row.oid == d.Address().ObjectID && d.Namespace() == (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}) && row.name == d.Name() && row.left == d.Left && row.right == d.Right
}

type operatorProvider struct{}

func (operatorProvider) ClassID() ids.OID { return ids.OperatorClassID }
func (operatorProvider) Validate(v catalog.Read, descriptor catalog.Descriptor) error {
	d, ok := descriptor.(*catalog.Operator)
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
			if procedure.Signature != catalog.ProcedureSignature(signature) || procedure.Result != d.Result || procedure.RoutineKind != catalog.FunctionRoutine {
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

// builtinOperatorDescriptors returns a fresh descriptor for every seeded
// operator, in OID order, with its supported metadata merged in. Seeded
// operators share one engine-owned backing array, like built-in routines.
// Both tables are sorted by OID, so metadata is merged in one pass.
func builtinOperatorDescriptors(owner authorization.PrincipalRef) []catalog.Operator {
	ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	operators := make([]catalog.Operator, len(builtinOperators))
	metadata := supportedOperatorMetadata[:]
	for i, row := range builtinOperators {
		d := &operators[i]
		*d = catalog.Operator{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: row.oid}, ObjectNamespace: ns, ObjectRevision: 1, ObjectName: row.name, Left: row.left, Right: row.right, Owner: owner}
		if len(metadata) != 0 && metadata[0].oid == row.oid {
			m := metadata[0]
			metadata = metadata[1:]
			d.Result, d.Function = ids.OID(m.result), ids.OID(m.procedure)
			d.Commutator, d.Negator = ids.OID(m.commutator), ids.OID(m.negator)
			d.Restriction, d.Join = ids.OID(m.restriction), ids.OID(m.join)
			d.Hash, d.Merge = m.hash, m.merge
		}
	}
	if len(metadata) != 0 {
		panic("stuntgres: operator metadata without a seeded operator")
	}
	return operators
}
