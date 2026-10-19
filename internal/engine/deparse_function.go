package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type deparseSignature uint8

const (
	deparseConstraint deparseSignature = iota
	deparseConstraintPretty
	deparseExpr
	deparseExprPretty
	deparseIndex
	deparseIndexColumn
	deparseViewText
	deparseViewOID
	deparseViewTextPretty
	deparseViewOIDPretty
	deparseViewWrap
	deparseSerialSequence
)

type deparseSignatureDefinition struct {
	name string
	args []sqltype.ID
}

var deparseSignatures = [...]deparseSignatureDefinition{
	{"pg_get_constraintdef", []sqltype.ID{sqltype.Oid}},
	{"pg_get_constraintdef", []sqltype.ID{sqltype.Oid, sqltype.Bool}},
	{"pg_get_expr", []sqltype.ID{sqltype.PgNodeTree, sqltype.Oid}},
	{"pg_get_expr", []sqltype.ID{sqltype.PgNodeTree, sqltype.Oid, sqltype.Bool}},
	{"pg_get_indexdef", []sqltype.ID{sqltype.Oid}},
	{"pg_get_indexdef", []sqltype.ID{sqltype.Oid, sqltype.Int4, sqltype.Bool}},
	{"pg_get_viewdef", []sqltype.ID{sqltype.Text}},
	{"pg_get_viewdef", []sqltype.ID{sqltype.Oid}},
	{"pg_get_viewdef", []sqltype.ID{sqltype.Text, sqltype.Bool}},
	{"pg_get_viewdef", []sqltype.ID{sqltype.Oid, sqltype.Bool}},
	{"pg_get_viewdef", []sqltype.ID{sqltype.Oid, sqltype.Int4}},
	{"pg_get_serial_sequence", []sqltype.ID{sqltype.Text, sqltype.Text}},
}

type deparseExpression struct {
	expr.Info
	signature deparseSignature
	args      []expr.Expr
}

func (deparseExpression) ResultNullability(site expr.ResultSite) (nullable, known bool) {
	if site == expr.ResultSelect {
		return true, true
	}
	return false, false
}

func (b *expressionBinder) bindDeparse(raw parser.FunctionExpr, visible bool) (expr.Expr, error) {
	var args []expr.Expr
	var unknown []bool
	if len(raw.Args) <= 100 {
		args = make([]expr.Expr, len(raw.Args))
		unknown = make([]bool, len(raw.Args))
	}
	for i, child := range raw.Args {
		if literal, ok := child.(parser.LiteralExpr); ok && (literal.Value.Kind == parser.StringValue || literal.Value.Kind == parser.NullValue) {
			if args != nil {
				unknown[i] = true
				args[i] = literalExpression{Info: expr.Info{Pos: child.SourcePosition()}}
			}
			continue
		}
		arg, err := b.bind(child, expressionPolicy{context: projectionContext})
		if err != nil {
			return nil, err
		}
		if args != nil {
			args[i] = arg
			unknown[i] = arg.ResultUsage().Type == nil
		}
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	var candidates [len(deparseSignatures)]deparseSignature
	count, best := 0, -1
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	for index, signature := range deparseSignatures {
		if !visible || raw.Variadic || signature.name != name || len(signature.args) != len(args) {
			continue
		}
		score, viable := 0, true
		for i, arg := range args {
			if unknown[i] {
				continue
			}
			if _, err := sqltype.PlanCoercion(arg.ResultUsage(), b.typ(signature.args[i]).Usage(), sqltype.Implicit); err != nil {
				viable = false
				break
			}
			if arg.ResultUsage().Type.BaseType().ID() == signature.args[i] {
				score++
			}
		}
		if !viable || score < best {
			continue
		}
		if score > best {
			count, best = 0, score
		}
		candidates[count] = deparseSignature(index)
		count++
	}
	// Unknown category selection is all-or-nothing: a conflicting BOOL/numeric
	// slot prevents the string bias in another slot from resolving ambiguity.
	if count > 1 {
		var categories [3]sqltype.ID
		consistent := true
		for i, u := range unknown {
			if !u {
				continue
			}
			for _, candidate := range candidates[:count] {
				id := deparseSignatures[candidate].args[i]
				if id == sqltype.Text {
					categories[i] = sqltype.Text
					break
				}
				if categories[i] == 0 {
					categories[i] = id
				} else if categories[i] != id {
					consistent = false
				}
			}
		}
		if consistent {
			n := 0
			for _, candidate := range candidates[:count] {
				keep := true
				for i, u := range unknown {
					if u && deparseSignatures[candidate].args[i] != categories[i] {
						keep = false
					}
				}
				if keep {
					candidates[n] = candidate
					n++
				}
			}
			count = n
		}
	}
	if count > 1 {
		var known sqltype.ID
		homogeneous := true
		for i, arg := range args {
			if unknown[i] {
				continue
			}
			id := arg.ResultUsage().Type.BaseType().ID()
			if known != 0 && known != id {
				homogeneous = false
			}
			known = id
		}
		if homogeneous && known != 0 {
			n := 0
			for _, candidate := range candidates[:count] {
				keep := true
				for i, u := range unknown {
					if u {
						if _, err := sqltype.PlanCoercion(b.typ(known).Usage(), b.typ(deparseSignatures[candidate].args[i]).Usage(), sqltype.Implicit); err != nil {
							keep = false
						}
					}
				}
				if keep {
					candidates[n] = candidate
					n++
				}
			}
			if n > 0 {
				count = n
			}
		}
	}
	if count != 1 {
		return nil, functions.DirectResolutionError(b, raw, args, unknown, count > 1)
	}
	signature := candidates[0]
	properties := expr.Properties{Volatility: expr.Stable}
	for i, arg := range args {
		u := b.typ(deparseSignatures[signature].args[i]).Usage()
		if unknown[i] {
			var err error
			args[i], err = b.bind(raw.Args[i], expressionPolicy{context: functionArgumentContext, expected: u, mode: sqltype.Implicit})
			if err != nil {
				return nil, err
			}
		} else if arg.ResultUsage() != u {
			plan, err := sqltype.PlanCoercion(arg.ResultUsage(), u, sqltype.Implicit)
			if err != nil {
				return nil, err
			}
			args[i] = coercionExpression{b.info(u, raw.Args[i].SourcePosition(), arg.Properties()), arg, plan, sqltype.Implicit, false}
		}
		properties = expr.MergeProperties(properties, args[i].Properties())
	}
	info, err := b.builtinRoutineInfo(b.info(b.typ(sqltype.Text).Usage(), raw.Pos, properties), catalogRoutineProvider, name, deparseSignatures[signature].args...)
	if err != nil {
		return nil, err
	}
	return deparseExpression{info, signature, args}, nil
}

func (e deparseExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	c := evalContext(ctx)
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if err := validateDeparseExpression(e); err != nil {
		return sqltype.Value{}, err
	}
	var values [3]sqltype.Value
	null := false
	for i, arg := range e.args {
		value, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		if value.Type() != arg.ResultUsage().Type {
			return sqltype.Value{}, fmt.Errorf("stuntgres: invalid deparse argument")
		}
		values[i] = value
		null = null || value.IsNull()
	}
	if null {
		return sqltype.Null(e.Usage.Type), nil
	}
	if c.liveScope() == nil || c.liveScope().commentCatalog.State() == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing deparse catalog snapshot")
	}
	if e.signature == deparseSerialSequence {
		return e.evalSerialSequence(c, values)
	}
	view := c.liveScope().commentCatalog
	pretty := false
	definition := deparseSignatures[e.signature]
	if definition.args[len(definition.args)-1] == sqltype.Bool {
		var err error
		pretty, err = values[len(e.args)-1].Boolean()
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	renderer := pgRenderer{view: view, pretty: pretty, path: c.liveScope().deparsePath,
		output: c.liveScope().output, nonstandardStrings: c.liveScope().op.nonstandardStrings}
	index := 1
	if e.signature != deparseExpr && e.signature != deparseExprPretty {
		index = 0
	}
	if e.signature >= deparseIndex {
		return e.evalRelation(c, values, pretty)
	}
	id, err := values[index].OID()
	if err != nil {
		return sqltype.Value{}, err
	}
	var text string
	if e.signature == deparseConstraint || e.signature == deparseConstraintPretty {
		found := false
		for _, descriptor := range view.Descriptors() {
			if domain, ok := descriptor.(*catalog.Type); ok {
				for _, def := range domain.DomainChecks {
					if def.OID != ids.OID(id) {
						continue
					}
					found = true
					rendered, err := renderer.sql(c.Context, def.Expression, pgRenderParent{})
					if err != nil {
						return sqltype.Value{}, err
					}
					text = "CHECK (" + rendered + ")"
					if def.NotValid {
						text += " NOT VALID"
					}
				}
				continue
			}
			relation, ok := descriptor.(*catalog.Relation)
			if !ok {
				continue
			}
			for _, def := range relation.Constraints.NotNull {
				if def.OID != ids.OID(id) {
					continue
				}
				found = true
				name, err := renderer.attribute(relation, def.Attribute)
				if err != nil {
					return sqltype.Value{}, err
				}
				text = "NOT NULL " + name
				if def.NoInherit {
					text += " NO INHERIT"
				}
				if def.NotValid {
					text += " NOT VALID"
				}
			}
			for _, def := range relation.Constraints.Check {
				if def.OID != ids.OID(id) {
					continue
				}
				found = true
				renderer.relation = relation
				rendered, err := renderer.sql(c.Context, def.Expression, pgRenderParent{})
				if err != nil {
					return sqltype.Value{}, err
				}
				text = "CHECK (" + rendered + ")"
				if def.NoInherit {
					text += " NO INHERIT"
				}
				if def.NotValid {
					text += " NOT VALID"
				}
			}
			for _, def := range relation.Constraints.Unique {
				if def.OID != ids.OID(id) {
					continue
				}
				found = true
				text, err = renderer.uniqueConstraintSQL(relation, &def)
				if err != nil {
					return sqltype.Value{}, err
				}
			}
			for _, def := range relation.Constraints.ForeignKeys {
				if def.OID != ids.OID(id) {
					continue
				}
				found = true
				text, err = renderer.foreignKeyConstraintSQL(relation, def)
				if err != nil {
					return sqltype.Value{}, err
				}
			}
			for _, def := range relation.Constraints.Exclusion {
				if def.OID != ids.OID(id) {
					continue
				}
				found = true
				index, ok := view.Relation(def.Index)
				if !ok {
					return sqltype.Value{}, fmt.Errorf("stuntgres: missing exclusion backing index")
				}
				text, err = indexDefinitionSQL(c.Context, view, index, indexPresentation{exclusion: true, pretty: pretty, path: c.liveScope().deparsePath, output: c.liveScope().output})
				if err != nil {
					return sqltype.Value{}, err
				}
			}
		}
		if !found {
			return sqltype.Null(e.Usage.Type), nil
		}
	} else {
		carrier, err := values[0].Tree()
		if err != nil {
			return sqltype.Value{}, err
		}
		tree, ok := carrier.(boundTree)
		if !ok {
			return sqltype.Value{}, fmt.Errorf("stuntgres: foreign structural tree")
		}
		var relation *catalog.Relation
		if id != 0 {
			relation, ok = view.Relation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(id)})
			if !ok {
				return sqltype.Null(e.Usage.Type), nil
			}
		}
		renderer.relation = relation
		text, err = renderer.sql(c.Context, tree.expression, pgRenderParent{})
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	return sqltype.String(e.Usage.Type, text), nil
}

func deparseAttribute(relation *catalog.Relation, attr int32) (string, error) {
	if relation == nil {
		return "", sqlError("22023", "expression contains variables")
	}
	for i, live := range relation.Attributes {
		if live == attr {
			return pgIdentifier(relation.Columns[i].Name), nil
		}
	}
	return "", sqlError("XX000", fmt.Sprintf("invalid attnum %d for relation %q", attr, relation.Name()))
}

func validateDeparseExpression(e deparseExpression) error {
	if int(e.signature) >= len(deparseSignatures) || e.Usage.Type == nil || e.Usage.Type.ID() != sqltype.Text {
		return fmt.Errorf("stuntgres: invalid stored deparse signature")
	}
	definition := deparseSignatures[e.signature]
	if len(e.args) != len(definition.args) {
		return fmt.Errorf("stuntgres: invalid stored deparse signature")
	}
	properties := expr.Properties{Volatility: expr.Stable}
	for i, arg := range e.args {
		id := definition.args[i]
		if arg == nil || arg.ResultUsage().Type == nil || arg.ResultUsage().Type.ID() != id {
			return fmt.Errorf("stuntgres: invalid stored deparse argument")
		}
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	if e.Properties() != properties {
		return fmt.Errorf("stuntgres: invalid stored deparse properties")
	}
	return nil
}

func (e deparseExpression) evalRelation(c *EvalContext, values [3]sqltype.Value, pretty bool) (sqltype.Value, error) {
	scope := c.liveScope()
	catalog := scope.commentCatalog
	var address ids.ObjectAddress
	if e.signature == deparseViewText || e.signature == deparseViewTextPretty {
		text, err := values[0].Text()
		if err != nil {
			return sqltype.Value{}, err
		}
		name, err := parser.ParseRegclassName(text)
		if err != nil || len(name.Parts) > 2 {
			return sqltype.Value{}, sqlError("42602", "invalid name syntax")
		}
		for i := range name.Parts {
			if name.Parts[i].Text == "" {
				return sqltype.Value{}, sqlError("42602", "invalid name syntax")
			}
			name.Parts[i].Pos = parser.Position{}
		}
		ref, err := scope.deparseAuth.engine.resolveRelationSnapshot(scope.deparseAuth, name, true)
		if err != nil {
			return sqltype.Value{}, err
		}
		address = ref.Address
	} else {
		id, err := values[0].OID()
		if err != nil {
			return sqltype.Value{}, err
		}
		address = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(id)}
	}
	relation, ok := catalog.Relation(address)
	if !ok {
		return sqltype.Null(e.Usage.Type), nil
	}
	var text string
	var err error
	if e.signature == deparseIndex || e.signature == deparseIndexColumn {
		if relation.SQLIndex == nil {
			return sqltype.Null(e.Usage.Type), nil
		}
		var column int64
		if e.signature == deparseIndexColumn {
			column, err = values[1].Integer()
			if err != nil {
				return sqltype.Value{}, err
			}
		}
		text, err = indexDefinitionSQL(c.Context, catalog, relation, indexPresentation{column: column, pretty: pretty, path: scope.deparsePath, temporary: temporaryNamespace(scope.deparseAuth), output: scope.output})
	} else {
		if relation.View == nil {
			return sqltype.Null(e.Usage.Type), nil
		}
		var wrap int64
		if e.signature == deparseViewWrap {
			pretty = true
			wrap, err = values[1].Integer()
			if err != nil {
				return sqltype.Value{}, err
			}
		}
		text, err = viewOf(relation).selectSQL(c.Context, catalog, viewPresentation{deparse: true, pretty: pretty, wrap: wrap, path: scope.deparsePath, columns: relation.Columns, output: scope.output, nonstandardStrings: scope.op.nonstandardStrings})
		text += ";"
	}
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.String(e.Usage.Type, text), nil
}
