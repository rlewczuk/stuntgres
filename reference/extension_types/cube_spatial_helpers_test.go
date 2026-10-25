package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func cubeSpatialScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET extra_float_digits=3", 0))
			body(t, p, b, c)
		})
	}
}

var cubeSpatialNames = []string{"overlap", "contains", "contained", "distance", "euclidean", "taxicab", "chebyshev"}
var cubeSpatialTypes = []string{"BOOL", "BOOL", "BOOL", "FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8"}
var cubeSpatialSurfaces = []string{"&&", "@>", "<@", "cube_distance", "<->", "<#>", "<=>"}

func cubeSpatialExpression(surface, a, b string) string {
	if surface == "cube_distance" {
		return "cube_distance(" + a + "," + b + ")"
	}
	return a + " " + surface + " " + b
}

func cubeSpatialProjection(a, b string) string {
	parts := make([]string, len(cubeSpatialNames))
	for i, surface := range cubeSpatialSurfaces {
		parts[i] = cubeSpatialExpression(surface, a, b) + " AS " + cubeSpatialNames[i]
	}
	return strings.Join(parts, ",")
}

func cubeSpatialResult(overlap, contains, contained bool, euclidean, taxicab, chebyshev float64) []any {
	return []any{overlap, contains, contained, euclidean, euclidean, taxicab, chebyshev}
}

func cubeSpatialSelect(b fixture.Backend, q string, names, types []string, want [][]any, args ...any) fixture.Step {
	return cubeSpatialSelectNullable(b, q, names, types, want, false, args...)
}

// Native stored-view columns retain the existing declared-column nullability
// policy; ordinary computed projections and PG expose unknown nullability.
func cubeSpatialSelectNullable(b fixture.Backend, q string, names, types []string, want [][]any, viewColumns bool, args ...any) fixture.Step {
	s := fixture.Select(q, names, types, want, args...)
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if value == nil {
			return false
		}
		switch types[column] {
		case "BOOL":
			if _, ok := value.(bool); !ok {
				t.Fatalf("raw BOOL %T", value)
			}
		case "FLOAT8":
			if _, ok := value.(float64); !ok {
				t.Fatalf("raw FLOAT8 %T", value)
			}
		}
		return false
	}
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		cols, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for i, col := range cols {
			var scan reflect.Type
			switch types[i] {
			case "BOOL":
				scan = reflect.TypeFor[bool]()
			case "FLOAT8":
				scan = reflect.TypeFor[float64]()
			default:
				continue
			}
			if col.ScanType() != scan {
				t.Fatalf("%s ScanType %v want %v", names[i], col.ScanType(), scan)
			}
			wantNullable := viewColumns && !b.PG
			if n, known := col.Nullable(); n != wantNullable || known != wantNullable {
				t.Fatalf("nullable %v/%v want %v/%v", n, known, wantNullable, wantNullable)
			}
			if n, known := col.Length(); n != 0 || known {
				t.Fatalf("%s Length %d/%v", names[i], n, known)
			}
			if p, s, known := col.DecimalSize(); p != 0 || s != 0 || known {
				t.Fatalf("%s DecimalSize %d/%d/%v", names[i], p, s, known)
			}
		}
	}
	return s
}

func cubeSpatialBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, step fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step))
}

type cubeSpatialCase struct {
	name, a, b                    string
	overlap, contains, contained  bool
	euclidean, taxicab, chebyshev float64
}

// Independently specified from the manual/released source and confirmed by
// root's pinned PostgreSQL18.6 oracle before native implementation.
// In particular, containment intentionally does not use padded overlap bounds.
var cubeSpatialFinite = []cubeSpatialCase{
	{"disjoint", "(0,0),(1,1)", "(4,5),(6,7)", false, false, false, 5, 7, 4},
	{"touching", "(0,0),(1,1)", "(1,1),(2,2)", true, false, false, 0, 0, 0},
	{"nested", "(0,0),(4,4)", "(1,1),(2,2)", true, true, false, 0, 0, 0},
	{"points", "(3,4)", "(0,0)", false, false, false, 5, 7, 4},
	{"equal", "(1,1)", "(1,1)", true, true, true, 0, 0, 0},
	{"flat_touch", "(0,1),(0,3)", "(0,3),(0,5)", true, false, false, 0, 0, 0},
	{"extra_nonzero", "(3)", "(3,4)", false, false, true, 4, 4, 4},
	{"extra_zero", "(3)", "(3,0)", true, true, true, 0, 0, 0},
	{"containment_asymmetry", "(0,5),(1,6)", "(0.5)", false, true, false, 5, 5, 5},
	{"empty_point", "()", "(3,4)", false, false, true, 5, 7, 4},
	{"empty_origin", "()", "(0,0)", true, true, true, 0, 0, 0},
	{"empty", "()", "()", true, true, true, 0, 0, 0},
	{"boundary_flat", "(0,0),(4,4)", "(0,1),(0,4)", true, true, false, 0, 0, 0},
	{"boundary_point", "(0,0),(4,4)", "(4,4)", true, true, false, 0, 0, 0},
	{"excursion", "(0,0),(4,4)", "(4,4),(5,4)", true, false, false, 0, 0, 0},
	{"reversed", "(1,1),(0,0)", "(6,7),(4,5)", false, false, false, 5, 7, 4},
	{"mixed_corners", "(1,0),(0,1)", "(4,7),(6,5)", false, false, false, 5, 7, 4},
}

func cubeSpatialFixtureRows() [][]any {
	return [][]any{
		append([]any{int64(1)}, cubeSpatialResult(false, false, false, 5, 7, 4)...),
		append([]any{int64(2)}, cubeSpatialResult(true, false, false, 0, 0, 0)...),
		append([]any{int64(3)}, cubeSpatialResult(false, true, false, 5, 5, 5)...),
		{int64(4), nil, nil, nil, nil, nil, nil, nil},
	}
}

func cubeSpatialTableQuery() string {
	return "SELECT id," + cubeSpatialProjection("a", "b") + " FROM cube_spatial_data ORDER BY id"
}

func cubeSpatialTableCheck(b fixture.Backend) fixture.Step {
	return cubeSpatialSelect(b, cubeSpatialTableQuery(), append([]string{"id"}, cubeSpatialNames...), append([]string{"INT4"}, cubeSpatialTypes...), cubeSpatialFixtureRows())
}

func cubeSpatialSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	cubeInstall(t, p, b, c)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE cube_spatial_data(id integer NOT NULL,a cube,b cube)", 0))
	q := "INSERT INTO cube_spatial_data VALUES ($1,$2,$3)"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	for i, pair := range [][2]any{{"(0,0),(1,1)", "(4,5),(6,7)"}, {"(0,0),(1,1)", "(1,1),(2,2)"}, {[]byte("(0,5),(1,6)"), []byte("(0.5)")}, {nil, "(0)"}} {
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, i+1, pair[0], pair[1]))
		for _, value := range pair {
			if owned, ok := value.([]byte); ok {
				for j := range owned {
					owned[j] = 'x'
				}
			}
		}
	}
}

func cubeSpatialNamedSelect(b fixture.Backend, q string, rows [][]any, args ...any) fixture.Step {
	return cubeSpatialSelect(b, q, cubeSpatialNames, cubeSpatialTypes, rows, args...)
}

func cubeSpatialCaseName(i int, name string) string { return fmt.Sprintf("%02d_%s", i, name) }
