package engine

import (
	"fmt"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type extensionPathsExpression struct {
	expr.Info
	arg expr.Expr
}

func (b *expressionBinder) bindExtensionPaths(raw parser.FunctionExpr, arg expr.Expr) (expr.Expr, error) {
	shape, err := sqltype.RecordShape([]sqltype.CompositeMember{
		{Attribute: 1, Name: "source", Usage: b.typ(sqltype.Text).Usage()},
		{Attribute: 2, Name: "target", Usage: b.typ(sqltype.Text).Usage()},
		{Attribute: 3, Name: "path", Usage: b.typ(sqltype.Text).Usage()},
	})
	if err != nil {
		return nil, err
	}
	properties := arg.Properties()
	properties.Volatility = max(expr.Stable, properties.Volatility)
	info, err := b.builtinRoutineInfo(b.info(shape.Usage(), raw.Pos, properties), functions.CatalogRoutineProvider, "pg_extension_update_paths", sqltype.Name)
	if err != nil {
		return nil, err
	}
	b.consumer.catalogSnapshot, b.prepared.regclassConcat = true, true
	return extensionPathsExpression{info, arg}, nil
}
func (n extensionPathsExpression) validate() error {
	if n.arg == nil || n.arg.ResultUsage().Type == nil || n.arg.ResultUsage().Type.ID() != sqltype.Name || n.Usage.Type == nil || n.Usage.Type.ID() != 2249 || !n.Usage.Type.IsComposite() || n.Usage.Type.CompositeLen() != 3 {
		return fmt.Errorf("stuntgres: invalid extension path signature")
	}
	for i, name := range [...]string{"source", "target", "path"} {
		member := n.Usage.Type.CompositeMember(i)
		if member.Attribute != int32(i+1) || member.Name != name || member.Usage.Type.ID() != sqltype.Text {
			return fmt.Errorf("stuntgres: invalid extension path record")
		}
	}
	return nil
}
func (n extensionPathsExpression) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: extension path set used as scalar")
}
func (n extensionPathsExpression) open(c *EvalContext) ([]sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return nil, err
	}
	if err := n.validate(); err != nil {
		return nil, err
	}
	value, err := n.arg.Eval(c)
	if err != nil {
		return nil, err
	}
	if value.IsNull() {
		return nil, nil
	}
	name, err := value.Text()
	if err != nil {
		return nil, err
	}
	if detail := catalog.ExtensionNameDetail(name, false); detail != "" {
		return nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: fmt.Sprintf(`invalid extension name: "%s"`, name), Detail: detail}
	}
	scope := c.liveScope()
	if scope == nil || scope.inquiry.commentCatalog.State() == nil {
		return nil, fmt.Errorf("stuntgres: missing extension paths snapshot")
	}
	implementation := scope.op.session.Engine().catalog.Extensions[name]
	if implementation == nil {
		return nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: fmt.Sprintf(`extension "%s" is not available`, name), Hint: "The extension must first be installed on the system where PostgreSQL is running."}
	}
	versions := implementation.Versions()
	vertices := versions.Vertices()
	rows := make([]sqltype.Value, 0, len(vertices)*(len(vertices)-1))
	shape := n.Usage.Type
	for _, from := range vertices {
		for _, to := range vertices {
			if from == to {
				continue
			}
			if err := c.Context.Err(); err != nil {
				return nil, err
			}
			fields := []sqltype.Value{sqltype.String(shape.CompositeMember(0).Usage.Type, from), sqltype.String(shape.CompositeMember(1).Usage.Type, to), sqltype.Null(shape.CompositeMember(2).Usage.Type)}
			if path := versions.Path(from, to); path != nil {
				fields[2] = sqltype.String(shape.CompositeMember(2).Usage.Type, strings.Join(path, "--"))
			}
			row, err := sqltype.OwnedCompositeValue(shape, fields)
			if err != nil {
				return nil, err
			}
			rows = append(rows, row)
		}
	}
	return rows, nil
}
