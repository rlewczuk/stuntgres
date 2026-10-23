package extension

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// UpdatePathRows builds the pg_extension_update_paths rows of the registered
// extension name: one (source, target, path) row of shape for every ordered
// pair of distinct versions, with a NULL path when target is unreachable. It
// checks ctx before each row.
func UpdatePathRows(ctx context.Context, registry map[string]catalog.ExtensionImplementation, name string, shape *sqltype.Descriptor) ([]sqltype.Value, error) {
	implementation := registry[name]
	if implementation == nil {
		return nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: fmt.Sprintf(`extension "%s" is not available`, name), Hint: "The extension must first be installed on the system where PostgreSQL is running."}
	}
	versions := implementation.Versions()
	vertices := versions.Vertices()
	rows := make([]sqltype.Value, 0, len(vertices)*(len(vertices)-1))
	for _, from := range vertices {
		for _, to := range vertices {
			if from == to {
				continue
			}
			if err := ctx.Err(); err != nil {
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
