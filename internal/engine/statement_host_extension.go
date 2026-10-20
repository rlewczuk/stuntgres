package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (h *statementHost) OpenExtensionPaths(c *exec.Context, n plan.ExtensionPathsExpr, name string) ([]sqltype.Value, error) {
	scope := h.scope()
	if scope.inquiry.commentCatalog.State() == nil {
		return nil, fmt.Errorf("stuntgres: missing extension paths snapshot")
	}
	implementation := scope.engine().catalog.Extensions[name]
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
