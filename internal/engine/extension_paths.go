package engine

import (
	"fmt"
	"strings"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

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
	return plan.ExtensionPathsExpr{Info: info, Arg: arg}, nil
}

func (h *statementHost) OpenExtensionPaths(c *exec.Context, n plan.ExtensionPathsExpr, name string) ([]sqltype.Value, error) {
	scope := h.scope()
	if scope.inquiry.commentCatalog.State() == nil {
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
