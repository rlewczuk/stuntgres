package engine

import (
	"context"
	"fmt"
	"math"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqltype"
)

// REL_18_0 catalog headers declare indexes for these exact represented tables;
// pinned PostgreSQL 18.0 confirms the flags. Install this fact on their owning
// descriptors without fabricating missing native index relations. This is not
// the bounded pg_tables summary-inclusion predicate.
func representedCatalogHasIndexes(oid OID) bool {
	switch oid {
	case pgTypeOID, 1213, 1249, 1255, 1259, 1262, 2224, 2396, 2600, pgAMOID, 2604, 2605, 2606, 2607,
		2608, 2609, 2610, 2612, 2615, 2616, 2617, 2618, 2964, 3079, 3456, 3501, pgRangeOID, 3600, 3764, ParameterClassID:
		return true
	}
	return false
}

// catalogRelation describes execution, not PostgreSQL relation kind. Keep this
// classification separate from virtualView, which controls source dispatch.
func pgClassKind(catalog CatalogRead, relation *relationDescriptor) string {
	switch relation.relationKind {
	case foreignRelation:
		return "f"
	case sequenceRelation:
		return "S"
	case viewRelation:
		return "v"
	case materializedViewRelation:
		return "m"
	case compositeRelation:
		return "c"
	case partitionedRelation:
		return "p"
	case indexRelation:
		if parent, ok := catalog.state.descriptors[relation.sqlIndex.parent].(*relationDescriptor); ok && parent.partition != nil {
			return "I"
		}
		return "i"
	case catalogRelation:
		if relation.virtualView || representedSummaryViewDefinition(relation.address.ObjectID) != "" {
			return "v"
		}
	}
	return "r"
}

func pgClassPersistence(catalog CatalogRead, relation *relationDescriptor) string {
	if relation.sqlIndex != nil {
		if parent, ok := catalog.state.descriptors[relation.sqlIndex.parent].(*relationDescriptor); ok {
			relation = parent
		}
	}
	if relation.sequence != nil {
		switch relation.sequence.persistence {
		case "temporary":
			return "t"
		case "unlogged":
			return "u"
		}
	}
	if namespace, ok := catalog.state.descriptors[relation.namespace].(*namespaceDescriptor); ok && namespace.temporary != (SessionToken{}) {
		return "t"
	}
	return "p"
}

func (e *Engine) pgClassRow(ctx context.Context, catalog CatalogRead, roles authorization.Read, source, relation *relationDescriptor) ([]sqltype.Value, error) {
	var failure error
	checked := func(value sqltype.Value, err error) sqltype.Value {
		if failure == nil {
			failure = err
		}
		return value
	}
	oid := func(i int, n uint32) sqltype.Value { return checked(sqltype.OIDValue(source.columns[i].Usage.Type, n)) }
	integer := func(i int, n int64) sqltype.Value { return checked(sqltype.Integer(source.columns[i].Usage.Type, n)) }
	text := func(i int, s string) sqltype.Value { return sqltype.String(source.columns[i].Usage.Type, s) }
	boolean := func(i int, b bool) sqltype.Value { return sqltype.Boolean(source.columns[i].Usage.Type, b) }
	kind := pgClassKind(catalog, relation)
	method := uint32(0)
	if kind == "r" || kind == "m" {
		method = 2
	}
	if relation.sqlIndex != nil {
		method = 403
		if relation.sqlIndex.method == hashMethod {
			method = 405
		}
		if relation.sqlIndex.method == gistMethod {
			method = 783
		}
	}
	shared := relation.relationKind == catalogRelation && (relation.address.ObjectID == 1213 || relation.address.ObjectID == 1262 || relation.address.ObjectID == 2396 || relation.address.ObjectID == 2964 || relation.address.ObjectID == ParameterClassID)
	tablespace := uint32(0)
	if shared {
		tablespace = 1664
	}
	tuples := float64(-1)
	if kind == "S" {
		tuples = 1
	}
	replica := "n"
	if relation.relationKind != catalogRelation && (kind == "r" || kind == "m" || kind == "p") {
		replica = "d"
	}
	acl, err := e.pgClassACL(roles, source.columns[31].Usage.Type, relation.acl)
	if err != nil {
		return nil, err
	}
	options := sqltype.Null(source.columns[32].Usage.Type)
	if relation.view != nil && len(relation.view.options) != 0 {
		elements := make([]sqltype.Value, len(relation.view.options))
		for i, option := range relation.view.options {
			elements[i] = sqltype.String(options.Type().Element(), option)
		}
		options = checked(sqltype.OwnedArrayValue(options.Type(), []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(elements))}}, elements))
	}
	bound := sqltype.Null(source.columns[33].Usage.Type)
	if relation.membership != nil {
		parent, ok := catalog.state.descriptors[relation.membership.parent].(*relationDescriptor)
		if !ok || parent.partition == nil {
			return nil, fmt.Errorf("stuntgres: missing partition bound parent")
		}
		bound = checked(sqltype.Tree(bound.Type(), partitionBoundTree{membership: relation.membership, keys: parent.partition.keys}))
	}
	row := []sqltype.Value{
		oid(0, uint32(relation.address.ObjectID)), text(1, relation.name), oid(2, uint32(relation.namespace.ObjectID)),
		oid(3, uint32(relation.rowType)), oid(4, uint32(relation.ofType.ObjectID)), oid(5, uint32(relation.owner.ID())),
		oid(6, method), oid(7, 0), oid(8, tablespace), integer(9, 0), checked(sqltype.Float(source.columns[10].Usage.Type, tuples)),
		integer(11, 0), integer(12, 0), oid(13, 0), boolean(14, relation.hasIndexes), boolean(15, shared),
		text(16, pgClassPersistence(catalog, relation)), text(17, kind), integer(18, int64(relation.attributeHighWater)), integer(19, int64(len(relation.checkConstraints))),
		boolean(20, len(relation.rules) != 0 || relation.view != nil || relation.relationKind == catalogRelation && kind == "v"), boolean(21, false), boolean(22, relation.hasSubclasses),
		boolean(23, relation.rlsEnabled), boolean(24, relation.rlsForced), boolean(25, relation.relationKind != materializedViewRelation || relation.populated),
		text(26, replica), boolean(27, relation.membership != nil || relation.sqlIndex != nil && relation.sqlIndex.partitionParent != (ObjectAddress{})), oid(28, 0),
		checked(sqltype.XIDValue(source.columns[29].Usage.Type, 0)), checked(sqltype.XIDValue(source.columns[30].Usage.Type, 0)), acl, options, bound,
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return row, failure
}

func (e *Engine) pgClassACL(roles authorization.Read, array *sqltype.Descriptor, acl authorization.ACLValue) (sqltype.Value, error) {
	if !acl.Explicit {
		return sqltype.Null(array), nil
	}
	if !roles.BelongsTo(&e.authorizationDomain) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: foreign catalog ACL authority")
	}
	elements := make([]sqltype.Value, len(acl.Entries))
	for i, entry := range acl.Entries {
		if _, err := roles.Descriptor(entry.Grantor); err != nil {
			return sqltype.Value{}, err
		}
		if entry.Grantor.ID() > math.MaxUint32 {
			return sqltype.Value{}, fmt.Errorf("stuntgres: unrepresentable ACL grantor")
		}
		grantee := uint32(0)
		if !entry.Grantee.Public {
			if _, err := roles.Descriptor(entry.Grantee.Role); err != nil {
				return sqltype.Value{}, err
			}
			if entry.Grantee.Role.ID() > math.MaxUint32 {
				return sqltype.Value{}, fmt.Errorf("stuntgres: unrepresentable ACL grantee")
			}
			grantee = uint32(entry.Grantee.Role.ID())
		}
		mask, err := authorization.ACLItemBits(entry.Privileges)
		if err != nil {
			return sqltype.Value{}, err
		}
		options, err := authorization.ACLItemBits(entry.GrantOptions)
		if err != nil {
			return sqltype.Value{}, err
		}
		elements[i], err = sqltype.ACLItemValue(array.Element(), &e.aclAuthority, grantee, uint32(entry.Grantor.ID()), mask, options)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	var dimensions []sqltype.ArrayDimension
	if len(elements) != 0 {
		dimensions = []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(elements))}}
	}
	return sqltype.OwnedArrayValue(array, dimensions, elements)
}
