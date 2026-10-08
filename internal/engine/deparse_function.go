package engine

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type deparseExpression struct {
	expressionInfo
	constraint bool
	args       []BoundExpr
}

func (b *expressionBinder) bindDeparse(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	constraint := raw.Name.Parts[len(raw.Name.Parts)-1].Text == "pg_get_constraintdef"
	minimum := 2
	if constraint {
		minimum = 1
	}
	if !visible || raw.Variadic || len(raw.Args) < minimum || len(raw.Args) > minimum+1 {
		return nil, b.functionSignatureError(raw)
	}
	args := make([]BoundExpr, len(raw.Args))
	properties := ExprProperties{Volatility: Stable}
	for i, child := range raw.Args {
		id := sqltype.Oid
		if !constraint && i == 0 {
			id = sqltype.PgNodeTree
		}
		if i == minimum {
			id = sqltype.Bool
		}
		arg, err := b.bind(child, expressionPolicy{context: functionArgumentContext, expected: b.typ(id).Usage(), mode: sqltype.Implicit})
		if err != nil {
			if d, ok := err.(*sqlstate.Error); ok && (d.Code == "42804" || d.Code == "42846") {
				return nil, b.functionSignatureError(raw)
			}
			return nil, err
		}
		args[i] = arg
		properties = mergeExpressionProperties(properties, arg.Properties())
	}
	return deparseExpression{b.info(b.typ(sqltype.Text).Usage(), raw.Pos, properties), constraint, args}, nil
}

func (e deparseExpression) Eval(c *EvalContext) (sqltype.Value, error) {
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
		return sqltype.Null(e.usage.Type), nil
	}
	if c.statementScope == nil || c.statementScope.commentCatalog.state == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing deparse catalog snapshot")
	}
	view := c.statementScope.commentCatalog
	minimum := 2
	if e.constraint {
		minimum = 1
	}
	pretty := false
	if len(e.args) > minimum {
		var err error
		pretty, err = values[minimum].Boolean()
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	renderer := pgRenderer{view: view, pretty: pretty, path: c.statementScope.deparsePath,
		output: c.statementScope.output, nonstandardStrings: c.statementScope.op.nonstandardStrings}
	index := 1
	if e.constraint {
		index = 0
	}
	id, err := values[index].OID()
	if err != nil {
		return sqltype.Value{}, err
	}
	var text string
	if e.constraint {
		found := false
		for _, descriptor := range view.state.descriptors {
			relation, ok := descriptor.(*relationDescriptor)
			if !ok {
				continue
			}
			for _, def := range relation.notNullConstraints {
				if def.OID != OID(id) {
					continue
				}
				found = true
				name, err := deparseAttribute(relation, def.Attribute)
				if err != nil {
					return sqltype.Value{}, err
				}
				text = "NOT NULL " + name
			}
			for _, def := range relation.checkConstraints {
				if def.OID != OID(id) {
					continue
				}
				found = true
				renderer.relation = relation
				rendered, err := renderer.sql(c.Context, def.expression, 0)
				if err != nil {
					return sqltype.Value{}, err
				}
				text = "CHECK (" + rendered + ")"
				if def.NoInherit {
					text += " NO INHERIT"
				}
			}
		}
		if !found {
			return sqltype.Null(e.usage.Type), nil
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
		var relation *relationDescriptor
		if id != 0 {
			relation, ok = view.state.descriptors[ObjectAddress{ClassID: RelationClassID, ObjectID: OID(id)}].(*relationDescriptor)
			if !ok {
				return sqltype.Null(e.usage.Type), nil
			}
		}
		renderer.relation = relation
		text, err = renderer.sql(c.Context, tree.expression, 0)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	return sqltype.String(e.usage.Type, text), nil
}

func deparseAttribute(relation *relationDescriptor, attr int32) (string, error) {
	if relation == nil {
		return "", sqlError("22023", "expression contains variables")
	}
	for i, live := range relation.attributes {
		if live == attr {
			return pgIdentifier(relation.columns[i].Name), nil
		}
	}
	return "", sqlError("XX000", fmt.Sprintf("invalid attnum %d for relation %q", attr, relation.name))
}

func validateDeparseExpression(e deparseExpression) error {
	minimum := 2
	if e.constraint {
		minimum = 1
	}
	if len(e.args) < minimum || len(e.args) > minimum+1 || e.usage.Type == nil || e.usage.Type.ID() != sqltype.Text {
		return fmt.Errorf("stuntgres: invalid stored deparse signature")
	}
	properties := ExprProperties{Volatility: Stable}
	for i, arg := range e.args {
		id := sqltype.Oid
		if !e.constraint && i == 0 {
			id = sqltype.PgNodeTree
		}
		if i == minimum {
			id = sqltype.Bool
		}
		if arg == nil || arg.ResultUsage().Type == nil || arg.ResultUsage().Type.ID() != id {
			return fmt.Errorf("stuntgres: invalid stored deparse argument")
		}
		properties = mergeExpressionProperties(properties, arg.Properties())
	}
	if e.Properties() != properties {
		return fmt.Errorf("stuntgres: invalid stored deparse properties")
	}
	return nil
}
