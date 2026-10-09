package engine

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// A target column shares the UPDATE frame, but never source SELECT authority.
const queryTargetLeaf = -2

type queryColumn struct {
	descriptor     ColumnDescriptor
	slot           int
	leaf, physical int // -1 identifies a computed merged value.
}
type queryQualifier struct {
	name, schema, original, alias string
	ref                           ObjectRef
	columns                       []queryColumn
	excludeColumnDiagnostics      bool // Whole-join and USING aliases are not error RTE candidates.
	lateralForbidden              bool // A DML target cannot correlate into its source, even with LATERAL.
}
type queryNamespace struct {
	columns                   []queryColumn
	qualifiers                []queryQualifier
	frame                     []ColumnDescriptor
	values                    bool // Synthetic VALUES names retain PostgreSQL spelling hints.
	identityValuesOrderDemand []bool
}

func rebaseQueryColumns(columns []queryColumn, offset int) []queryColumn {
	out := append([]queryColumn(nil), columns...)
	for i := range out {
		out[i].slot += offset
	}
	return out
}
func rebaseQueryNamespace(ns queryNamespace, offset int) queryNamespace {
	ns.columns = rebaseQueryColumns(ns.columns, offset)
	ns.qualifiers = append([]queryQualifier(nil), ns.qualifiers...)
	for i := range ns.qualifiers {
		ns.qualifiers[i].columns = rebaseQueryColumns(ns.qualifiers[i].columns, offset)
	}
	return ns
}
func combineQueryNamespace(left, right queryNamespace) (queryNamespace, error) {
	for _, l := range left.qualifiers {
		for _, r := range right.qualifiers {
			if l.name == r.name && (l.alias != "" || r.alias != "" || l.ref == r.ref || l.ref == (ObjectRef{}) || r.ref == (ObjectRef{})) {
				return queryNamespace{}, sqlError("42712", fmt.Sprintf(`table name "%s" specified more than once`, l.name))
			}
		}
	}
	return queryNamespace{
		columns:    append(append([]queryColumn(nil), left.columns...), right.columns...),
		qualifiers: append(append([]queryQualifier(nil), left.qualifiers...), right.qualifiers...),
		frame:      append(append([]ColumnDescriptor(nil), left.frame...), right.frame...),
	}, nil
}

func (b *expressionBinder) queryColumnAt(c queryColumn, pos parser.Position) BoundExpr {
	var origin ObjectRef
	if c.leaf == queryTargetLeaf {
		origin = b.useColumn(c.physical)
	} else if c.leaf >= 0 {
		leaf := &b.querySources[c.leaf]
		origin = leaf.ref
		origin.Address.SubID = leaf.relation.attributes[c.physical]
		b.dependency(origin, leaf.relation.revision)
		found := false
		for _, slot := range leaf.uses {
			if slot == c.physical {
				found = true
				break
			}
		}
		if !found {
			leaf.uses = append(leaf.uses, c.physical)
		}
	}
	return columnExpression{expressionInfo: b.info(c.descriptor.Usage, pos, ExprProperties{RowDependent: true}), origin: origin, scope: 1, frame: 1, slot: c.slot}
}

func (b *expressionBinder) queryQualifier(name parser.ObjectName, pos parser.Position) (*queryQualifier, error) {
	label := name.Parts[len(name.Parts)-1].Text
	var match *queryQualifier
	for i := range b.queryNamespace.qualifiers {
		q := &b.queryNamespace.qualifiers[i]
		if q.name != label || len(name.Parts) == 2 && (q.alias != "" || q.schema != name.Parts[0].Text) {
			continue
		}
		if match != nil {
			return nil, expressionError("42P09", fmt.Sprintf(`table reference "%s" is ambiguous`, label), pos)
		}
		match = q
	}
	if match != nil {
		return match, nil
	}
	for _, q := range b.queryHidden {
		if q.original != label && q.name != label {
			continue
		}
		if len(name.Parts) == 2 && q.schema != name.Parts[0].Text {
			continue
		}
		err := expressionError("42P01", fmt.Sprintf(`invalid reference to FROM-clause entry for table "%s"`, label), pos).(*sqlstate.Error)
		visibleAlias := false
		if q.original == label && q.alias != "" && q.alias != label {
			for _, visible := range b.queryNamespace.qualifiers {
				if visible.name == q.alias && visible.ref == q.ref {
					visibleAlias = true
					break
				}
			}
		}
		if visibleAlias {
			err.Hint = fmt.Sprintf(`Perhaps you meant to reference the table alias "%s".`, q.alias)
		} else {
			err.Detail = fmt.Sprintf(`There is an entry for table "%s", but it cannot be referenced from this part of the query.`, q.name)
			if b.queryNonLateral && !q.excludeColumnDiagnostics && !q.lateralForbidden {
				err.Hint = "To reference that table, you must mark this subquery with LATERAL."
			}
		}
		return nil, err
	}
	return nil, expressionError("42P01", fmt.Sprintf(`missing FROM-clause entry for table "%s"`, label), pos)
}
func (b *expressionBinder) queryColumn(raw parser.ColumnExpr) (BoundExpr, error) {
	parts := raw.Name.Parts
	if len(parts) == 0 || len(parts) > 3 {
		return nil, fmt.Errorf("stuntgres: invalid query column name")
	}
	name := parts[len(parts)-1].Text
	columns := b.queryNamespace.columns
	if len(parts) > 1 {
		q, err := b.queryQualifier(parser.ObjectName{Parts: parts[:len(parts)-1]}, raw.Pos)
		if err != nil {
			return nil, err
		}
		columns = q.columns
	}
	var match *queryColumn
	for i := range columns {
		if columns[i].descriptor.Name != name {
			continue
		}
		if match != nil {
			return nil, expressionError("42702", fmt.Sprintf(`column reference "%s" is ambiguous`, name), raw.Pos)
		}
		match = &columns[i]
	}
	if match != nil {
		if demand := b.queryNamespace.identityValuesOrderDemand; len(demand) != 0 {
			demand[match.slot] = true
		}
		return b.queryColumnAt(*match, raw.Pos), nil
	}
	if b.queryNamespace.values && len(parts) == 1 {
		return nil, valuesOrderColumnError(b.queryNamespace.frame, name, raw.Pos)
	}
	qualifier := ""
	if len(parts) > 1 {
		qualifier = parts[len(parts)-2].Text
	}
	detail, hint, err := queryColumnDiagnostic(b.queryHidden, qualifier, name, raw.Pos, b.queryNonLateral)
	if err != nil {
		return nil, err
	}
	var message string
	if len(parts) > 1 {
		message = fmt.Sprintf("column %s.%s does not exist", qualifier, name)
	} else {
		message = fmt.Sprintf(`column "%s" does not exist`, name)
	}
	missing := expressionError("42703", message, raw.Pos).(*sqlstate.Error)
	missing.Detail, missing.Hint = detail, hint
	return nil, missing
}

func (b *expressionBinder) bindFrom(item parser.FromItem) (root LogicalNode, namespace queryNamespace, failure error) {
	// FROM preorder is shared with the renderer. Capture the namespace after
	// aliases and USING have been applied, before a parent rebases its slots.
	ordinal := -1
	if b.noParameters && item != nil {
		ordinal = len(b.viewNamespaces)
		b.viewNamespaces = append(b.viewNamespaces, viewNamespace{})
		defer func() {
			if failure == nil {
				b.viewNamespaces[ordinal].columns = b.freezeViewColumns(namespace.columns)
				for _, q := range namespace.qualifiers {
					b.viewNamespaces[ordinal].qualifiers = append(b.viewNamespaces[ordinal].qualifiers, viewNamespaceQualifier{q.name, b.freezeViewColumns(q.columns)})
				}
			}
		}()
	}
	if err := b.ctx.Err(); err != nil {
		return nil, queryNamespace{}, err
	}
	switch n := item.(type) {
	case nil:
		return &singletonNode{}, queryNamespace{}, nil
	case *parser.ValuesQuery:
		query, err := b.bindValues(n, false)
		if err != nil {
			return nil, queryNamespace{}, err
		}
		schema := query.Root.Schema()
		columns := make([]queryColumn, len(schema))
		for i, column := range schema {
			columns[i] = queryColumn{descriptor: column, slot: i, leaf: -1, physical: -1}
		}
		b.queryHidden = append(b.queryHidden, queryQualifier{name: "unnamed_subquery", columns: columns})
		return query.Root, queryNamespace{columns: columns, frame: schema}, nil
	case *parser.TableReference:
		ref, err := b.engine.resolveReference(*b.queryAuth, n.Name)
		if err != nil {
			return nil, queryNamespace{}, err
		}
		relation := b.queryAuth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
		if relation.relationKind == indexRelation {
			return nil, queryNamespace{}, sqlError("42809", fmt.Sprintf(`cannot read from index "%s"`, relation.name))
		}
		leaf := len(b.querySources)
		b.querySources = append(b.querySources, queryLeaf{ref: ref, relation: relation, storage: b.engine.authorizationStorageLocked(*b.queryAuth, ref.Address)})
		columns := make([]queryColumn, len(relation.columns))
		for i, c := range relation.columns {
			columns[i] = queryColumn{c, i, leaf, i}
		}
		q := queryQualifier{name: relation.name, original: relation.name, schema: b.queryAuth.catalog.state.descriptors[relation.namespace].Name(), ref: ref, columns: columns}
		b.queryHidden = append(b.queryHidden, q)
		return &sourceNode{querySchema: relation.columns, ref: ref, occurrence: leaf}, queryNamespace{columns: columns, qualifiers: []queryQualifier{q}, frame: relation.columns}, nil
	case *parser.AliasReference:
		root, ns, err := b.bindFrom(n.Input)
		if err != nil {
			return nil, queryNamespace{}, err
		}
		if len(n.Columns) > len(ns.columns) {
			kind := "join expression"
			switch n.Input.(type) {
			case *parser.TableReference, *parser.ValuesQuery:
				kind = "table"
			}
			return nil, queryNamespace{}, sqlError("42P10", fmt.Sprintf(`%s "%s" has %d columns available but %d columns specified`, kind, n.Name.Text, len(ns.columns), len(n.Columns)))
		}
		ns.columns = append([]queryColumn(nil), ns.columns...)
		for i, name := range n.Columns {
			ns.columns[i].descriptor.Name = name.Text
		}
		q := queryQualifier{name: n.Name.Text, alias: n.Name.Text, columns: ns.columns}
		if _, table := n.Input.(*parser.TableReference); table {
			q.ref, q.schema, q.original = ns.qualifiers[0].ref, ns.qualifiers[0].schema, ns.qualifiers[0].original
			b.queryHidden[len(b.queryHidden)-1] = q
		} else if _, values := n.Input.(*parser.ValuesQuery); values {
			b.queryHidden[len(b.queryHidden)-1] = q
		} else {
			q.excludeColumnDiagnostics = true
			b.queryHidden = append(b.queryHidden, q)
		}
		ns.qualifiers = []queryQualifier{q}
		return root, ns, nil
	case *parser.JoinReference:
		joinOrdinal := b.viewJoinCount
		b.viewJoinCount++
		left, lns, err := b.bindFrom(n.Left)
		if err != nil {
			return nil, queryNamespace{}, err
		}
		right, rns, err := b.bindFrom(n.Right)
		if err != nil {
			return nil, queryNamespace{}, err
		}
		rns = rebaseQueryNamespace(rns, len(lns.frame))
		ns, err := combineQueryNamespace(lns, rns)
		if err != nil {
			return nil, queryNamespace{}, err
		}
		join := &joinNode{querySchema: ns.frame, kind: n.Kind, left: left, right: right, leftWidth: len(lns.frame), rightWidth: len(rns.frame)}
		old := b.queryNamespace
		b.queryNamespace = &ns
		defer func() { b.queryNamespace = old }()
		if n.On != nil {
			b.aggregateClause = "JOIN conditions"
			join.predicate, err = b.bindBoolean(n.On, filterContext, "JOIN/ON")
			if err != nil {
				return nil, queryNamespace{}, err
			}
			if b.noParameters {
				if err := b.captureViewLiterals(n.On, join.predicate, viewJoinLiteral, joinOrdinal); err != nil {
					return nil, queryNamespace{}, err
				}
			}
		}
		keys := n.Using
		if n.Natural {
			for _, l := range lns.columns {
				for _, r := range rns.columns {
					if l.descriptor.Name == r.descriptor.Name {
						keys = append(keys, parser.Identifier{Text: l.descriptor.Name})
						break
					}
				}
			}
		}
		if ordinal >= 0 {
			for _, key := range keys {
				b.viewNamespaces[ordinal].keys = append(b.viewNamespaces[ordinal].keys, key.Text)
			}
		}
		if len(keys) > 0 {
			merged := make([]queryColumn, 0, len(ns.columns))
			for i, key := range keys {
				for _, prior := range keys[:i] {
					if prior.Text == key.Text {
						return nil, queryNamespace{}, sqlError("42701", fmt.Sprintf(`column name "%s" appears more than once in USING clause`, key.Text))
					}
				}
				l, err := usingQueryColumn(lns.columns, key.Text, "left")
				if err != nil {
					return nil, queryNamespace{}, err
				}
				r, err := usingQueryColumn(rns.columns, key.Text, "right")
				if err != nil {
					return nil, queryNamespace{}, err
				}
				pos := parser.Position{Offset: -1}
				le, re := b.queryColumnAt(l, pos), b.queryColumnAt(r, pos)
				predicate, err := sqltype.BindComparisonPredicate(sqltype.CompareEqual, le.ResultUsage().Type, re.ResultUsage().Type, b.typ(sqltype.Bool))
				if err != nil {
					return nil, queryNamespace{}, equalityError(err, le.ResultUsage().Type, re.ResultUsage().Type, pos)
				}
				eq := comparisonExpression{b.info(b.typ(sqltype.Bool).Usage(), pos, ExprProperties{RowDependent: true}), le, re, predicate, "=", ""}
				if join.predicate == nil {
					join.predicate = eq
				} else {
					join.predicate = logicalExpression{b.info(b.typ(sqltype.Bool).Usage(), pos, ExprProperties{RowDependent: true}), parser.LogicalAnd, join.predicate, eq}
				}
				operands := []comparisonOperand{{node: le}, {node: re}}
				usage, err := commonExpressionUsage(operands, b.typ(sqltype.Text).Usage(), "JOIN/USING")
				if err != nil {
					return nil, queryNamespace{}, err
				}
				column := l
				if n.Kind == parser.JoinRight || n.Kind == parser.JoinInner && l.descriptor.Usage != usage && r.descriptor.Usage == usage {
					column = r
				}
				chosen := le
				if column.slot == r.slot {
					chosen = re
				}
				if n.Kind == parser.JoinFull || column.descriptor.Usage != usage {
					var value BoundExpr
					if n.Kind == parser.JoinFull {
						lv, err := b.coerceConditional(comparisonOperand{node: le}, usage, "JOIN/USING")
						if err != nil {
							return nil, queryNamespace{}, err
						}
						rv, err := b.coerceConditional(comparisonOperand{node: re}, usage, "JOIN/USING")
						if err != nil {
							return nil, queryNamespace{}, err
						}
						value = conditionalExpression{expressionInfo: b.info(usage, pos, ExprProperties{RowDependent: true}), kind: parser.Coalesce, args: []BoundExpr{lv, rv}}
					} else {
						value, err = b.coerceConditional(comparisonOperand{node: chosen}, usage, "JOIN/USING")
						if err != nil {
							return nil, queryNamespace{}, err
						}
					}
					if value.ResultUsage() != usage {
						// A same-type common key can still lose its typmod.
						// Publish that relabel in the recipe as well as the slot.
						plan, err := sqltype.PlanCoercion(value.ResultUsage(), usage, sqltype.Implicit)
						if err != nil {
							return nil, queryNamespace{}, err
						}
						info, err := collatedInfo(b.info(usage, pos, coercionProperties(value, plan)), value)
						if err != nil {
							return nil, queryNamespace{}, err
						}
						value = coercionExpression{info, value, plan, sqltype.Implicit}
					}
					column = queryColumn{descriptor: ColumnDescriptor{Name: key.Text, Usage: usage}, slot: len(ns.frame), leaf: -1, physical: -1}
					join.merges = append(join.merges, value)
					ns.frame = append(ns.frame, column.descriptor)
				}
				column.descriptor.Name = key.Text
				merged = append(merged, column)
			}
			if n.UsingAlias != nil {
				q := queryQualifier{name: n.UsingAlias.Text, alias: n.UsingAlias.Text, columns: append([]queryColumn(nil), merged...), excludeColumnDiagnostics: true}
				for _, prior := range ns.qualifiers {
					if prior.name == q.name {
						return nil, queryNamespace{}, sqlError("42712", fmt.Sprintf(`table name "%s" specified more than once`, q.name))
					}
				}
				ns.qualifiers = append(ns.qualifiers, q)
			}
			for _, c := range ns.columns {
				key := false
				for _, k := range keys {
					if k.Text == c.descriptor.Name {
						key = true
						break
					}
				}
				if !key {
					merged = append(merged, c)
				}
			}
			ns.columns = merged
		}
		if n.Kind != parser.JoinInner {
			// ON and USING bind against the original operands. Only the outward
			// namespace acquires nullable proofs; child/catalog descriptors stay immutable.
			nullable := func(slot int) bool {
				return n.Kind == parser.JoinFull && slot < join.leftWidth+join.rightWidth ||
					n.Kind == parser.JoinLeft && slot >= join.leftWidth && slot < join.leftWidth+join.rightWidth ||
					n.Kind == parser.JoinRight && slot < join.leftWidth
			}
			for i := range ns.frame {
				if nullable(i) {
					ns.frame[i].Nullable, ns.frame[i].NullabilityKnown = true, true
				}
			}
			for i := range ns.columns {
				c := &ns.columns[i]
				if nullable(c.slot) {
					c.descriptor.Nullable, c.descriptor.NullabilityKnown = true, true
				}
			}
			for i := range ns.qualifiers {
				copied := false
				for j, column := range ns.qualifiers[i].columns {
					if !nullable(column.slot) {
						continue
					}
					if !copied {
						ns.qualifiers[i].columns = append([]queryColumn(nil), ns.qualifiers[i].columns...)
						copied = true
					}
					c := &ns.qualifiers[i].columns[j]
					c.descriptor.Nullable, c.descriptor.NullabilityKnown = true, true
				}
			}
		}
		join.querySchema = ns.frame
		return join, ns, nil
	}
	return nil, queryNamespace{}, fmt.Errorf("stuntgres: invalid FROM item")
}

func usingQueryColumn(columns []queryColumn, name, side string) (queryColumn, error) {
	var result queryColumn
	count := 0
	for _, c := range columns {
		if c.descriptor.Name == name {
			result = c
			count++
		}
	}
	if count == 0 {
		return result, sqlError("42703", fmt.Sprintf(`column "%s" specified in USING clause does not exist in %s table`, name, side))
	}
	if count > 1 {
		return result, sqlError("42702", fmt.Sprintf(`common column name "%s" appears more than once in %s table`, name, side))
	}
	return result, nil
}
