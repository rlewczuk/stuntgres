package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
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
	expr.Info
	kind commentInquiryKind
	args []expr.Expr
}

func (b *expressionBinder) bindCommentInquiry(raw parser.FunctionExpr, visible bool) (expr.Expr, error) {
	inquiry, err := b.bindCommentInquiryStructure(raw, visible)
	if err != nil {
		return nil, err
	}
	// Analyze every child before STRICT eliminates execution demand.
	for _, arg := range inquiry.args {
		if literal, ok := arg.(literalExpression); ok && literal.value.IsNull() {
			typ := b.typ(sqltype.Text)
			return literalExpression{b.info(typ.Usage(), raw.Pos, expr.Properties{}), sqltype.Null(typ)}, nil
		}
	}
	return inquiry, nil
}

// bindCommentInquiryStructure retains typed children for capture without
// evaluating them or changing the executable STRICT fold.
func (b *expressionBinder) bindCommentInquiryStructure(raw parser.FunctionExpr, visible bool) (commentInquiryExpression, error) {
	if b.defaultConsumer {
		return commentInquiryExpression{}, expr.ErrorAt("0A000", "functions are unsupported", raw.Pos)
	}
	if len(raw.Args) > 100 {
		// Argument transformation precedes PostgreSQL's global limit check.
		// Discard analyzed children; this call cannot produce an executable node.
		for _, child := range raw.Args {
			if _, err := b.bind(child, expressionPolicy{context: projectionContext}); err != nil {
				return commentInquiryExpression{}, err
			}
		}
		return commentInquiryExpression{}, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
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
	// PostgreSQL ignores VARIADIC decoration on a matching fixed signature.
	if !visible || len(raw.Args) != arity {
		return commentInquiryExpression{}, b.functionSignatureError(raw)
	}
	args := make([]expr.Expr, arity)
	properties := expr.Properties{Volatility: expr.Stable}
	for i, child := range raw.Args {
		id := sqltype.Oid
		if i == 1 {
			id = second
		}
		arg, err := b.bind(child, expressionPolicy{context: functionArgumentContext, expected: b.typ(id).Usage(), mode: sqltype.Implicit})
		if err != nil {
			if d, ok := err.(*sqlstate.Error); ok && (d.Code == "42804" || d.Code == "42846") {
				return commentInquiryExpression{}, b.functionSignatureError(raw)
			}
			return commentInquiryExpression{}, err
		}
		args[i] = arg
		p := arg.Properties()
		properties.Volatility = max(properties.Volatility, p.Volatility)
		properties.RowDependent = properties.RowDependent || p.RowDependent
		properties.ParameterDependent = properties.ParameterDependent || p.ParameterDependent
		properties.StageDependent = properties.StageDependent || p.StageDependent
		properties.DomainDependent = properties.DomainDependent || p.DomainDependent
		properties.RuntimeDomainConversion = properties.RuntimeDomainConversion || p.RuntimeDomainConversion
	}
	formal := [2]sqltype.ID{sqltype.Oid, second}
	info, err := b.builtinRoutineInfo(b.info(b.typ(sqltype.Text).Usage(), raw.Pos, properties), catalogRoutineProvider, name, formal[:arity]...)
	if err != nil {
		return commentInquiryExpression{}, err
	}
	return commentInquiryExpression{info, kind, args}, nil
}

func descriptorComment(d catalog.Descriptor, sub int32) string {
	switch d := d.(type) {
	case *catalog.Namespace:
		if sub == 0 {
			return d.Comment
		}
	case *catalog.Type:
		if sub == 0 {
			return d.Comment
		}
	case *catalog.Relation:
		if sub == 0 {
			return d.Comment
		}
		return d.ColumnComments[sub]
	}
	return ""
}

func (e commentInquiryExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	c := evalContext(ctx)
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
		return sqltype.Null(e.Usage.Type), nil
	}
	if err := admitInquiryBody(c, e); err != nil {
		return sqltype.Value{}, err
	}
	if c.liveScope() == nil || c.liveScope().commentCatalog.State() == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing comment snapshot")
	}
	oid, err := values[0].OID()
	if err != nil {
		return sqltype.Value{}, err
	}
	scope := c.liveScope()
	var text string
	switch e.kind {
	case legacyDescription:
		var class ids.OID
		for a, d := range scope.commentCatalog.Descriptors() {
			if a.ObjectID == ids.OID(oid) && a.SubID == 0 && (text == "" || a.ClassID < class) {
				if candidate := descriptorComment(d, 0); candidate != "" {
					// Immutable address ordering keeps equal calls STABLE; it
					// does not establish portable catalog precedence.
					text, class = candidate, a.ClassID
				}
			}
		}
	case columnDescription:
		sub, err := values[1].Integer()
		if err != nil {
			return sqltype.Value{}, err
		}
		text = descriptorComment(scope.commentCatalog.Get(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(oid)}), int32(sub))
	case objectDescription, sharedDescription:
		name, err := values[1].Text()
		if err != nil {
			return sqltype.Value{}, err
		}
		if e.kind == sharedDescription {
			if name == "pg_authid" {
				r := scope.commentRoles
				role, _ := r.Find(scope.commentCatalog.Owner().Principals.Principal(uint64(oid)))
				text = role.Comment
			}
		} else {
			var class ids.OID
			switch name {
			case "pg_class":
				class = ids.RelationClassID
			case "pg_namespace":
				class = ids.NamespaceClassID
			case "pg_type":
				class = ids.TypeClassID
			}
			if class != 0 {
				text = descriptorComment(scope.commentCatalog.Get(ids.ObjectAddress{ClassID: class, ObjectID: ids.OID(oid)}), 0)
			}
		}
	}
	if text == "" {
		return sqltype.Null(e.Usage.Type), nil
	}
	return sqltype.String(e.Usage.Type, text), nil
}
