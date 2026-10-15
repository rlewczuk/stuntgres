package engine

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

var pgConstraintTypeIDs = [28]sqltype.ID{
	sqltype.Oid, sqltype.Name, sqltype.Oid, sqltype.Char,
	sqltype.Bool, sqltype.Bool, sqltype.Bool, sqltype.Bool,
	sqltype.Oid, sqltype.Oid, sqltype.Oid, sqltype.Oid, sqltype.Oid,
	sqltype.Char, sqltype.Char, sqltype.Char, sqltype.Bool, sqltype.Int2,
	sqltype.Bool, sqltype.Bool, 1005, 1005, 1028, 1028, 1028, 1005, 1028, sqltype.PgNodeTree,
}

// Each capture projects the caller's immutable owner generation, including its
// transaction overlay. There is no independently mutable constraint registry.
func capturePGConstraint(ctx context.Context, catalog CatalogRead, ref ObjectRef, source *relationDescriptor) (*querySource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(source.columns) != len(pgConstraintTypeIDs) {
		return nil, fmt.Errorf("stuntgres: invalid constraint source shape")
	}
	var types [28]*sqltype.Descriptor
	for i, id := range pgConstraintTypeIDs {
		owned, ok := catalog.LookupType(id)
		if !ok || owned != source.columns[i].Usage.Type {
			return nil, fmt.Errorf("stuntgres: foreign constraint source type")
		}
		types[i] = owned
	}
	count := 0
	for _, descriptor := range catalog.state.descriptors {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if table, ok := descriptor.(*relationDescriptor); ok {
			count += len(table.notNullConstraints) + len(table.checkConstraints) + len(table.uniqueConstraints) + len(table.foreignKeys) + len(table.exclusionConstraints)
		}
	}
	rows := make([][]sqltype.Value, count)
	values := make([]sqltype.Value, count*28)
	builder := pgConstraintBuilder{types: types}
	at := 0
	for _, descriptor := range catalog.state.descriptors {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		table, ok := descriptor.(*relationDescriptor)
		if !ok {
			continue
		}
		next := func(id OID, name, kind string) ([]sqltype.Value, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if id == 0 || table.address.ClassID != RelationClassID || table.address.ObjectID == 0 {
				return nil, fmt.Errorf("stuntgres: invalid constraint owner identity")
			}
			row := values[at*28 : (at+1)*28 : (at+1)*28]
			rows[at] = row
			at++
			builder.initialize(row, table, id, name, kind)
			return row, builder.err
		}
		for _, c := range table.notNullConstraints {
			row, err := next(c.OID, c.Name, "n")
			if err != nil {
				return nil, err
			}
			local, count := nonuniqueConstraintProvenance(catalog, table, c.Name, c.Attribute, false)
			builder.boolean(row, 7, !c.NotValid)
			builder.boolean(row, 16, local || c.partitionLocal)
			builder.integer(row, 17, count)
			builder.boolean(row, 18, c.NoInherit)
			builder.keys(row, 20, []int32{c.Attribute})
		}
		for _, c := range table.checkConstraints {
			row, err := next(c.OID, c.Name, "c")
			if err != nil {
				return nil, err
			}
			local, count := nonuniqueConstraintProvenance(catalog, table, c.Name, 0, true)
			builder.boolean(row, 7, !c.NotValid)
			builder.boolean(row, 16, local)
			builder.integer(row, 17, count)
			builder.boolean(row, 18, c.NoInherit)
			builder.checkKeys(ctx, row, table, c.expression)
			row[27], err = catalogTree(types[27], c.expression)
			if err != nil {
				return nil, err
			}
		}
		for _, c := range table.uniqueConstraints {
			kind := "u"
			if c.Primary {
				kind = "p"
			}
			row, err := next(c.OID, c.Name, kind)
			if err != nil {
				return nil, err
			}
			builder.oid(row, 10, c.Index.ObjectID)
			builder.oid(row, 11, c.Parent)
			builder.boolean(row, 16, c.IsLocal)
			builder.boolean(row, 18, c.Parent == 0)
			if c.Parent != 0 {
				builder.integer(row, 17, 1)
			}
			builder.keys(row, 20, c.Attributes)
		}
		for _, c := range table.foreignKeys {
			row, err := next(c.OID, c.Name, "f")
			if err != nil {
				return nil, err
			}
			builder.boolean(row, 7, !c.NotValid)
			builder.boolean(row, 18, true)
			builder.oid(row, 10, c.Index.ObjectID)
			builder.oid(row, 12, c.Target.ObjectID)
			row[13], err = constraintAction(types[13], c.UpdateAction)
			if err != nil {
				return nil, err
			}
			row[14], err = constraintAction(types[14], c.DeleteAction)
			if err != nil {
				return nil, err
			}
			match := "s"
			if c.Full {
				match = "f"
			}
			row[15] = sqltype.String(types[15], match)
			builder.keys(row, 20, c.Attributes)
			builder.keys(row, 21, c.Referenced)
			builder.keys(row, 25, c.DeleteAttributes)
			if err := builder.foreignOperators(ctx, catalog, row, table, c); err != nil {
				return nil, err
			}
		}
		for _, c := range table.exclusionConstraints {
			row, err := next(c.OID, c.Name, "x")
			if err != nil {
				return nil, err
			}
			builder.oid(row, 10, c.Index.ObjectID)
			builder.boolean(row, 4, c.Deferrable)
			builder.boolean(row, 5, c.InitiallyDeferred)
			builder.boolean(row, 18, true)
			index, ok := catalog.state.descriptors[c.Index].(*relationDescriptor)
			if !ok || index.sqlIndex == nil || len(index.sqlIndex.keys) != len(c.Operators) {
				return nil, fmt.Errorf("stuntgres: missing exclusion metadata provider")
			}
			keys := make([]sqltype.Value, len(c.Operators))
			operators := make([]sqltype.Value, len(c.Operators))
			for i, key := range index.sqlIndex.keys {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				attribute, _, err := indexCatalogKey(table, key)
				if err != nil {
					return nil, err
				}
				keys[i], err = sqltype.Integer(types[20].Element(), int64(attribute))
				if err != nil {
					return nil, err
				}
				op := c.Operators[i]
				inputs, err := op.EqualityInputs()
				if err != nil {
					return nil, err
				}
				id, err := constraintOperator(catalog, op.Name, inputs[0], inputs[1])
				if err != nil {
					return nil, err
				}
				operators[i], err = sqltype.OIDValue(types[26].Element(), uint32(id))
				if err != nil {
					return nil, err
				}
			}
			builder.vector(row, 20, keys)
			builder.vector(row, 26, operators)
		}
		if builder.err != nil {
			return nil, builder.err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &querySource{ref: ref, anonymous: rows}, nil
}

type pgConstraintBuilder struct {
	types [28]*sqltype.Descriptor
	err   error
}

func (b *pgConstraintBuilder) initialize(row []sqltype.Value, table *relationDescriptor, id OID, name, kind string) {
	for i, typ := range b.types {
		switch pgConstraintTypeIDs[i] {
		case sqltype.Oid:
			b.oid(row, i, 0)
		case sqltype.Bool:
			b.boolean(row, i, false)
		case sqltype.Int2:
			b.integer(row, i, 0)
		case sqltype.Char:
			row[i] = sqltype.String(typ, " ")
		default:
			row[i] = sqltype.Null(typ)
		}
	}
	b.oid(row, 0, id)
	row[1] = sqltype.String(b.types[1], name)
	b.oid(row, 2, table.namespace.ObjectID)
	row[3] = sqltype.String(b.types[3], kind)
	b.boolean(row, 6, true)
	b.boolean(row, 7, true)
	b.oid(row, 8, table.address.ObjectID)
	b.boolean(row, 16, true)
}
func (b *pgConstraintBuilder) oid(row []sqltype.Value, i int, id OID) {
	value, err := sqltype.OIDValue(b.types[i], uint32(id))
	row[i] = value
	if b.err == nil {
		b.err = err
	}
}
func (b *pgConstraintBuilder) integer(row []sqltype.Value, i int, n int64) {
	value, err := sqltype.Integer(b.types[i], n)
	row[i] = value
	if b.err == nil {
		b.err = err
	}
}
func (b *pgConstraintBuilder) boolean(row []sqltype.Value, i int, value bool) {
	row[i] = sqltype.Boolean(b.types[i], value)
}
func (b *pgConstraintBuilder) vector(row []sqltype.Value, i int, values []sqltype.Value) {
	if len(values) == 0 {
		return
	}
	value, err := sqltype.OwnedArrayValue(b.types[i], []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(values))}}, values)
	row[i] = value
	if b.err == nil {
		b.err = err
	}
}
func (b *pgConstraintBuilder) keys(row []sqltype.Value, i int, attributes []int32) {
	if len(attributes) == 0 {
		return
	}
	values := make([]sqltype.Value, len(attributes))
	for at, attribute := range attributes {
		value, err := sqltype.Integer(b.types[i].Element(), int64(attribute))
		values[at] = value
		if b.err == nil {
			b.err = err
		}
	}
	b.vector(row, i, values)
}
func (b *pgConstraintBuilder) checkKeys(ctx context.Context, row []sqltype.Value, table *relationDescriptor, expression BoundExpr) {
	var values []sqltype.Value
	walkBoundExpression(expression, func(node BoundExpr) {
		if b.err != nil {
			return
		}
		if err := ctx.Err(); err != nil {
			b.err = err
			return
		}
		column, ok := node.(columnExpression)
		if !ok || column.origin.Address.ClassID != RelationClassID || column.origin.Address.ObjectID != table.address.ObjectID {
			return
		}
		attribute := column.origin.Address.SubID
		for _, value := range values {
			n, _ := value.Integer()
			if n == int64(attribute) {
				return
			}
		}
		value, err := sqltype.Integer(b.types[20].Element(), int64(attribute))
		if b.err == nil {
			b.err = err
		}
		values = append(values, value)
	})
	b.vector(row, 20, values)
}

func constraintAction(typ *sqltype.Descriptor, action parser.ForeignKeyAction) (sqltype.Value, error) {
	var code string
	switch action {
	case parser.ForeignKeyNoAction:
		code = "a"
	case parser.ForeignKeyRestrict:
		code = "r"
	case parser.ForeignKeyCascade:
		code = "c"
	case parser.ForeignKeySetNull:
		code = "n"
	case parser.ForeignKeySetDefault:
		code = "d"
	default:
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid foreign-key action metadata")
	}
	return sqltype.String(typ, code), nil
}

func constraintOperator(catalog CatalogRead, name string, left, right sqltype.ID) (OID, error) {
	for _, operand := range []*sqltype.ID{&left, &right} {
		typ, ok := catalog.LookupType(*operand)
		if !ok {
			return 0, fmt.Errorf("stuntgres: missing constraint operator input")
		}
		if typ.IsEnum() {
			*operand = 3500
		}
	}
	namespace := ObjectAddress{ClassID: NamespaceClassID, ObjectID: PGCatalogNamespaceOID}
	signature := strconv.FormatUint(uint64(left), 10) + "," + strconv.FormatUint(uint64(right), 10)
	address, ok := catalog.state.names[NameKey{ClassID: OperatorClassID, Namespace: namespace, Name: name, Signature: signature}]
	if !ok || address.ObjectID == 0 {
		return 0, fmt.Errorf("stuntgres: missing constraint operator identity")
	}
	op, ok := catalog.state.descriptors[address].(*operatorDescriptor)
	if !ok || op.left != OID(left) || op.right != OID(right) {
		return 0, fmt.Errorf("stuntgres: invalid constraint operator identity")
	}
	return address.ObjectID, nil
}

func (b *pgConstraintBuilder) foreignOperators(ctx context.Context, catalog CatalogRead, row []sqltype.Value, child *relationDescriptor, f foreignKeyConstraint) error {
	if len(f.Attributes) == 0 || len(f.Attributes) != len(f.Referenced) {
		return fmt.Errorf("stuntgres: invalid foreign-key metadata keys")
	}
	parent, ok := catalog.state.descriptors[f.Target].(*relationDescriptor)
	if !ok {
		return fmt.Errorf("stuntgres: missing foreign-key parent metadata")
	}
	index, ok := catalog.state.descriptors[f.Index].(*relationDescriptor)
	if !ok || index.sqlIndex == nil {
		return fmt.Errorf("stuntgres: missing foreign-key index metadata")
	}
	comparisons, err := bindForeignKeyComparison(catalog, child, parent, index, f)
	if err != nil {
		return err
	}
	var operators [3][]sqltype.Value
	for i := range operators {
		operators[i] = make([]sqltype.Value, len(f.Attributes))
	}
	for i, attribute := range f.Attributes {
		if err := ctx.Err(); err != nil {
			return err
		}
		childSlot, parentSlot := slices.Index(child.attributes, attribute), slices.Index(parent.attributes, f.Referenced[i])
		matched := -1
		for at, comparison := range comparisons {
			if comparison.child == childSlot && comparison.parent == parentSlot {
				if matched != -1 {
					return fmt.Errorf("stuntgres: ambiguous foreign-key operator metadata")
				}
				matched = at
			}
		}
		if matched < 0 {
			return fmt.Errorf("stuntgres: missing foreign-key operator metadata")
		}
		inputs, err := comparisons[matched].comparison.EqualityInputs()
		if err != nil {
			return err
		}
		for triple, pair := range inputs {
			id, err := constraintOperator(catalog, "=", pair[0], pair[1])
			if err != nil {
				return err
			}
			operators[triple][i], err = sqltype.OIDValue(b.types[22+triple].Element(), uint32(id))
			if err != nil {
				return err
			}
		}
	}
	for i, values := range operators {
		b.vector(row, 22+i, values)
	}
	return b.err
}
