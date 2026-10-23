package reference_test

import (
	"database/sql"
	"math"
	"testing"

	"stuntgres/reference/fixture"
)

var cubeCoordinateNames = []string{"lower", "upper", "raw", "projection"}
var cubeCoordinateTypes = []string{"FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8"}

func cubeCoordinateProjection(c, n string) string {
	return "cube_ll_coord(" + c + "," + n + ") AS lower,cube_ur_coord(" + c + "," + n + ") AS upper," + c + " -> " + n + " AS raw," + c + " ~> " + n + " AS projection"
}

func cubeCoordinateSelect(b fixture.Backend, q string, want [][]any, args ...any) fixture.Step {
	return cubeSpatialSelect(b, q, cubeCoordinateNames, cubeCoordinateTypes, want, args...)
}

// Extractors are exact selections, not approximate floating-point calculations.
// CheckRaw distinguishes zero signs before the fixture's generic equality.
// NaN uses cubeCoordinateNaNBoth because reflect.DeepEqual(NaN,NaN) is false.
func cubeCoordinateScalar(b fixture.Backend, q string, want float64, args ...any) fixture.Step {
	s := cubeSpatialSelect(b, q, []string{"v"}, []string{"FLOAT8"}, [][]any{{want}}, args...)
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		got, ok := value.(float64)
		if !ok || math.Float64bits(got) != math.Float64bits(want) {
			t.Fatalf("coordinate = %v (%T, bits %016x), want %v (%016x)", value, value, math.Float64bits(got), want, math.Float64bits(want))
		}
		return true
	}
	return s
}

func cubeCoordinateHeld(t *testing.T, p *fixture.Pair, c *sql.Conn, q string) fixture.HeldStatement {
	t.Helper()
	s, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return fixture.HeldStatement{Executor: c, Statement: s, SQL: q}
}

func cubeCoordinateSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	cubeInstall(t, p, b, c)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE cube_coordinate_data(id integer NOT NULL,v cube)", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO cube_coordinate_data VALUES (1,'(3,1),(1,4)'),(2,'(42,137)'),(3,NULL)", 3))
}

func cubeCoordinateTable(b fixture.Backend, view bool) fixture.Step {
	q := "SELECT id," + cubeCoordinateProjection("v", "1") + " FROM cube_coordinate_data ORDER BY id"
	if view {
		q = "SELECT id,lower,upper,raw,projection FROM cube_coordinate_view ORDER BY id"
	}
	return cubeSpatialSelectNullable(b, q, append([]string{"id"}, cubeCoordinateNames...), append([]string{"INT4"}, cubeCoordinateTypes...), [][]any{{int64(1), float64(1), float64(3), float64(3), float64(1)}, {int64(2), float64(42), float64(42), float64(42), float64(42)}, {int64(3), nil, nil, nil, nil}}, view)
}

func cubeCoordinateNaNBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string) {
	t.Helper()
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, prepared := range []bool{false, true} {
		var rows *sql.Rows
		if prepared {
			rows, err = stmt.QueryContext(p.Ctx)
		} else {
			rows, err = c.QueryContext(p.Ctx, q)
		}
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer rows.Close()
			cols, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			if len(cols) != 1 || cols[0].Name() != "v" || cols[0].DatabaseTypeName() != "FLOAT8" {
				t.Fatal("NaN metadata", cols)
			}
			cubeSpatialSelect(b, q, []string{"v"}, []string{"FLOAT8"}, nil).CheckMetadata(t, rows)
			if !rows.Next() {
				t.Fatal("missing NaN row", rows.Err())
			}
			var raw any
			if err := rows.Scan(&raw); err != nil {
				t.Fatal(err)
			}
			value, ok := raw.(float64)
			if !ok || !math.IsNaN(value) {
				t.Fatalf("coordinate NaN = %v (%T)", raw, raw)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("NaN row count", rows.Err())
			}
		}()
	}
}
