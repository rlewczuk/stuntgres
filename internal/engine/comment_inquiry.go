package engine

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type commentInquiryKind uint8

const (
	columnDescription commentInquiryKind = iota
	objectDescription
	legacyDescription
	sharedDescription
)

type commentInquiryExpression struct {
	expressionInfo
	kind commentInquiryKind
	args []BoundExpr
}

func (b *expressionBinder) bindCommentInquiry(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	if b.defaultConsumer {
		return nil, expressionError("0A000", "functions are unsupported", raw.Pos)
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	kind, second := objectDescription, sqltype.Name
	if name == "col_description" {
		kind, second = columnDescription, sqltype.Int4
	}
	if name == "shobj_description" {
		kind = sharedDescription
	}
	arity := 2
	if name == "obj_description" && len(raw.Args) == 1 {
		kind, arity = legacyDescription, 1
	}
	if !visible || len(raw.Args) != arity || raw.Variadic {
		return nil, b.functionSignatureError(raw)
	}
	args := make([]BoundExpr, arity)
	properties := ExprProperties{Volatility: Stable}
	for i, child := range raw.Args {
		id := sqltype.Oid
		if i == 1 {
			id = second
		}
		arg, err := b.bind(child, expressionPolicy{context: functionArgumentContext, expected: b.typ(id).Usage(), mode: sqltype.Implicit})
		if err != nil {
			if d, ok := err.(*sqlstate.Error); ok && (d.Code == "42804" || d.Code == "42846") {
				return nil, b.functionSignatureError(raw)
			}
			return nil, err
		}
		args[i] = arg
		p := arg.Properties()
		properties.Volatility = max(properties.Volatility, p.Volatility)
		properties.RowDependent = properties.RowDependent || p.RowDependent
		properties.ParameterDependent = properties.ParameterDependent || p.ParameterDependent
		properties.StageDependent = properties.StageDependent || p.StageDependent
	}
	// Analyze every child before STRICT eliminates execution demand.
	for _, arg := range args {
		if literal, ok := arg.(literalExpression); ok && literal.value.IsNull() {
			typ := b.typ(sqltype.Text)
			return literalExpression{b.info(typ.Usage(), raw.Pos, ExprProperties{}), sqltype.Null(typ)}, nil
		}
	}
	return commentInquiryExpression{b.info(b.typ(sqltype.Text).Usage(), raw.Pos, properties), kind, args}, nil
}

func descriptorComment(d Descriptor, sub int32) string {
	switch d := d.(type) {
	case *namespaceDescriptor:
		if sub == 0 {
			return d.comment
		}
	case *typeDescriptor:
		if sub == 0 {
			return d.comment
		}
	case *relationDescriptor:
		if sub == 0 {
			return d.comment
		}
		return d.columnComments[sub]
	}
	return ""
}

func (e commentInquiryExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	var values [2]sqltype.Value
	null := false
	for i, arg := range e.args {
		v, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = v
		null = null || v.IsNull()
	}
	if null {
		return sqltype.Null(e.usage.Type), nil
	}
	if c.statementScope == nil || c.statementScope.commentCatalog.state == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing comment snapshot")
	}
	oid, err := values[0].OID()
	if err != nil {
		return sqltype.Value{}, err
	}
	scope := c.statementScope
	var text string
	switch e.kind {
	case legacyDescription:
		for a, d := range scope.commentCatalog.state.descriptors {
			if a.ObjectID == OID(oid) && a.SubID == 0 {
				if text = descriptorComment(d, 0); text != "" {
					break
				}
			}
		}
	case columnDescription:
		sub, err := values[1].Integer()
		if err != nil {
			return sqltype.Value{}, err
		}
		text = descriptorComment(scope.commentCatalog.state.descriptors[ObjectAddress{ClassID: RelationClassID, ObjectID: OID(oid)}], int32(sub))
	case objectDescription, sharedDescription:
		name, err := values[1].Text()
		if err != nil {
			return sqltype.Value{}, err
		}
		if e.kind == sharedDescription {
			if name == "pg_authid" {
				r := scope.commentRoles
				text = r.state.roles[PrincipalRef{owner: r.owner, id: uint64(oid)}].Comment
			}
		} else {
			var class OID
			switch name {
			case "pg_class":
				class = RelationClassID
			case "pg_namespace":
				class = NamespaceClassID
			case "pg_type":
				class = TypeClassID
			}
			if class != 0 {
				text = descriptorComment(scope.commentCatalog.state.descriptors[ObjectAddress{ClassID: class, ObjectID: OID(oid)}], 0)
			}
		}
	}
	if text == "" {
		return sqltype.Null(e.usage.Type), nil
	}
	return sqltype.String(e.usage.Type, text), nil
}
