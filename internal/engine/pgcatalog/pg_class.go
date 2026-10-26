package pgcatalog

import (
	"fmt"
	"math"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// REL_18_0 catalog headers declare indexes for these exact represented tables;
// pinned PostgreSQL 18.0 confirms the flags. Install this fact on their owning
// descriptors without fabricating missing native index relations. This is not
// the bounded pg_tables summary-inclusion predicate.
func CatalogHasIndexes(oid ids.OID) bool {
	switch oid {
	case TypeOID, 1213, 1249, 1255, 1259, ids.RoleClassID, 1262, 2224, 2396, 2600, AMOID, 2602, 2604, 2605, 2606, 2607,
		2608, 2609, 2610, 2611, 2612, 2613, 2615, 2616, 2617, 2618, 2964, 3079, 3456, 3501, pgRangeOID, TransformOID, 3600, TSParserOID, TSConfigOID, 3764, ids.ParameterClassID, 826:
		return true
	case TSConfigMapOID:
		return true
	}
	return false
}

// catalogRelation describes execution, not PostgreSQL relation kind. Keep this
// classification separate from virtualView, which controls source dispatch.
func ClassKind(view catalog.Read, relation *catalog.Relation) string {
	switch relation.RelationKind {
	case catalog.ForeignRelation:
		return "f"
	case catalog.SequenceRelation:
		return "S"
	case catalog.ViewRelation:
		return "v"
	case catalog.MaterializedViewRelation:
		return "m"
	case catalog.CompositeRelation:
		return "c"
	case catalog.PartitionedRelation:
		return "p"
	case catalog.IndexRelation:
		if parent, ok := view.Relation(relation.SQLIndex.Parent); ok && parent.Partition != nil {
			return "I"
		}
		return "i"
	case catalog.CatalogRelation:
		if relation.CatalogView {
			return "v"
		}
	}
	return "r"
}

func ClassPersistence(catalog catalog.Read, relation *catalog.Relation) string {
	if relation.SQLIndex != nil {
		if parent, ok := catalog.Relation(relation.SQLIndex.Parent); ok {
			relation = parent
		}
	}
	if relation.Sequence != nil {
		switch relation.Sequence.Persistence {
		case "temporary":
			return "t"
		case "unlogged":
			return "u"
		}
	}
	if namespace, ok := catalog.Namespace(relation.Namespace()); ok && namespace.Temporary != (ids.SessionToken{}) {
		return "t"
	}
	return "p"
}

func ClassRow(in Input, relation *catalog.Relation) ([]sqltype.Value, error) {
	ctx, view, roles, source := in.Context, in.Catalog, in.Roles, in.Source
	var failure error
	checked := func(value sqltype.Value, err error) sqltype.Value {
		if failure == nil {
			failure = err
		}
		return value
	}
	oid := func(i int, n uint32) sqltype.Value { return checked(sqltype.OIDValue(source.Columns[i].Usage.Type, n)) }
	integer := func(i int, n int64) sqltype.Value { return checked(sqltype.Integer(source.Columns[i].Usage.Type, n)) }
	text := func(i int, s string) sqltype.Value { return sqltype.String(source.Columns[i].Usage.Type, s) }
	boolean := func(i int, b bool) sqltype.Value { return sqltype.Boolean(source.Columns[i].Usage.Type, b) }
	kind := ClassKind(view, relation)
	method := uint32(0)
	if kind == "r" || kind == "m" {
		method = 2
	}
	if relation.SQLIndex != nil {
		method = uint32(relation.SQLIndex.MethodIdentity())
	}
	shared := catalog.SharedRelation(relation)
	tablespace := uint32(0)
	if shared {
		tablespace = 1664
	}
	tuples := float64(-1)
	if kind == "S" {
		tuples = 1
	}
	frozenXID := uint32(0)
	if in.Runtime != nil {
		if fact, ok := in.Runtime.Maintenance(relation.Address()); ok {
			if fact.Scanned {
				tuples = float64(fact.Tuples)
			}
			frozenXID = uint32(fact.FrozenXID)
		}
	}
	replica := "n"
	if relation.RelationKind != catalog.CatalogRelation && (kind == "r" || kind == "m" || kind == "p") {
		replica = "d"
	}
	acl, err := ClassACL(in.Owner, roles, source.Columns[31].Usage.Type, relation.Security.ACL)
	if err != nil {
		return nil, err
	}
	options := sqltype.Null(source.Columns[32].Usage.Type)
	if relation.View != nil && len(relation.View.Options()) != 0 {
		viewOptions := relation.View.Options()
		elements := make([]sqltype.Value, len(viewOptions))
		for i, option := range viewOptions {
			elements[i] = sqltype.String(options.Type().Element(), option)
		}
		options = checked(sqltype.OwnedArrayValue(options.Type(), []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(elements))}}, elements))
	}
	if len(relation.StorageOptions.Base) != 0 {
		elements := make([]sqltype.Value, len(relation.StorageOptions.Base))
		for i, option := range relation.StorageOptions.Base {
			elements[i] = sqltype.String(options.Type().Element(), option.Name+"="+option.Text)
		}
		options = checked(sqltype.OwnedArrayValue(options.Type(), []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(elements))}}, elements))
	}
	bound := sqltype.Null(source.Columns[33].Usage.Type)
	if relation.Membership != nil {
		parent, ok := view.Relation(relation.Membership.Parent)
		if !ok || parent.Partition == nil {
			return nil, fmt.Errorf("stuntgres: missing partition bound parent")
		}
		bound = checked(sqltype.Tree(bound.Type(), partitionBoundTree{membership: relation.Membership, keys: parent.Partition.Keys}))
	}
	row := []sqltype.Value{
		oid(0, uint32(relation.Address().ObjectID)), text(1, relation.Name()), oid(2, uint32(relation.Namespace().ObjectID)),
		oid(3, uint32(relation.RowType)), oid(4, uint32(relation.OfType.ObjectID)), oid(5, uint32(relation.Owner.ID())),
		oid(6, method), oid(7, 0), oid(8, tablespace), integer(9, 0), checked(sqltype.Float(source.Columns[10].Usage.Type, tuples)),
		integer(11, 0), integer(12, 0), oid(13, 0), boolean(14, relation.History.HasIndexes), boolean(15, shared),
		text(16, ClassPersistence(view, relation)), text(17, kind), integer(18, int64(relation.ColumnMeta.HighWater)), integer(19, int64(len(relation.Constraints.Check))),
		boolean(20, relation.HasRules() || relation.View != nil || relation.RelationKind == catalog.CatalogRelation && kind == "v"), boolean(21, relation.HasTriggers()), boolean(22, relation.History.HasSubclasses),
		boolean(23, relation.Security.RLSEnabled), boolean(24, relation.Security.RLSForced), boolean(25, relation.RelationKind != catalog.MaterializedViewRelation || relation.Populated),
		text(26, replica), boolean(27, relation.Membership != nil || relation.SQLIndex != nil && relation.SQLIndex.PartitionParent != (ids.ObjectAddress{})), oid(28, 0),
		checked(sqltype.XIDValue(source.Columns[29].Usage.Type, frozenXID)), checked(sqltype.XIDValue(source.Columns[30].Usage.Type, 0)), acl, options, bound,
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return row, failure
}

// ClassACL encodes a stored ACL as an aclitem[] cell; an absent ACL is NULL.
func ClassACL(owner *catalog.Owner, roles authorization.Read, array *sqltype.Descriptor, acl authorization.ACLValue) (sqltype.Value, error) {
	if !acl.Explicit {
		return sqltype.Null(array), nil
	}
	if !roles.BelongsTo(owner.Principals) {
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
		elements[i], err = sqltype.ACLItemValue(array.Element(), owner.ACLAuthority, grantee, uint32(entry.Grantor.ID()), mask, options)
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
