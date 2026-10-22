package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"stuntgres/reference/fixture"
)

var kcuColumns = []string{"constraint_catalog", "constraint_schema", "constraint_name", "table_catalog", "table_schema", "table_name", "column_name", "ordinal_position", "position_in_unique_constraint"}
var kcuTypes = []string{"NAME", "NAME", "NAME", "NAME", "NAME", "NAME", "NAME", "INT4", "INT4"}

const kcuQuery = "SELECT * FROM information_schema.key_column_usage WHERE table_name LIKE $1 ORDER BY table_schema,table_name,constraint_name,ordinal_position"

type kcuCase struct {
	t        *testing.T
	p        *fixture.Pair
	b        fixture.Backend
	conn     *sql.Conn
	database string
}

func kcuScenario(t *testing.T, body func(*kcuCase)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			defer conn.ExecContext(p.Ctx, "RESET ROLE")
			defer conn.ExecContext(p.Ctx, "ROLLBACK")
			c := &kcuCase{t: t, p: p, b: b, conn: conn}
			if err := conn.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&c.database); err != nil {
				t.Fatal(err)
			}
			body(c)
		})
	}
}

func (c *kcuCase) run(e fixture.Executor, s fixture.Step) {
	c.t.Helper()
	fixture.RunStep(c.t, c.p.Ctx, e, c.b.Recorder, s)
}
func (c *kcuCase) exec(queries ...string) {
	c.t.Helper()
	for _, q := range queries {
		c.run(c.conn, fixture.Exec(q, 0))
	}
}
func (c *kcuCase) hold(e fixture.Executor, q string) fixture.HeldStatement {
	c.t.Helper()
	stmt, err := e.PrepareContext(c.p.Ctx, q)
	if err != nil {
		c.t.Fatal(err)
	}
	c.t.Cleanup(func() { stmt.Close() })
	return fixture.HeldStatement{Executor: e, Statement: stmt, SQL: q}
}
func (c *kcuCase) row(schema, constraint, table, column string, ordinal int64, position any) []any {
	return []any{c.database, schema, constraint, c.database, schema, table, column, ordinal, position}
}
func (c *kcuCase) pub(constraint, table, column string, ordinal int64, position any) []any {
	return c.row("public", constraint, table, column, ordinal, position)
}
func kcuSorted(rows [][]any) [][]any {
	result := append([][]any(nil), rows...)
	sort.SliceStable(result, func(i, j int) bool {
		for _, n := range []int{4, 5, 2} {
			if result[i][n] != result[j][n] {
				return result[i][n].(string) < result[j][n].(string)
			}
		}
		return result[i][7].(int64) < result[j][7].(int64)
	})
	return result
}
func (c *kcuCase) step(pattern any, rows [][]any) fixture.Step {
	s := fixture.Select(kcuQuery, kcuColumns, kcuTypes, kcuSorted(rows), pattern)
	s.CheckMetadata = func(t *testing.T, r *sql.Rows) { kcuMetadata(t, r, c.b.PG) }
	s.CheckScan = func(t *testing.T, r *sql.Rows, index int) {
		values, targets := make([]any, 9), make([]any, 9)
		for i := range values {
			targets[i] = &values[i]
		}
		if err := r.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		for i, v := range values {
			if v == nil {
				if i != 8 {
					t.Fatalf("NULL in column %d", i)
				}
				continue
			}
			want := reflect.TypeOf(int64(0))
			if i < 7 {
				want = reflect.TypeOf("")
				if c.b.PG {
					want = reflect.TypeOf([]byte(nil))
				}
			}
			if reflect.TypeOf(v) != want {
				t.Fatalf("raw column %d: %T, want %v", i, v, want)
			}
		}
		var nullable sql.NullInt64
		targets[8] = &nullable
		if err := r.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		expected := kcuSorted(rows)[index][8]
		if nullable.Valid != (expected != nil) || nullable.Valid && nullable.Int64 != expected.(int64) {
			t.Fatalf("nullable ordinal: %+v want %v", nullable, expected)
		}
	}
	return s
}
func (c *kcuCase) check(e fixture.Executor, pattern any, rows ...[]any) {
	c.t.Helper()
	s := c.step(pattern, rows)
	c.run(e, s)
	if _, held := e.(fixture.HeldStatement); !held {
		c.run(e, fixture.Prepared(s))
	}
}
func kcuMetadata(t *testing.T, rows *sql.Rows, pg bool) []*sql.ColumnType {
	t.Helper()
	names, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(names, kcuColumns) {
		t.Fatal("columns", names)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	kcuColumnTypes(t, types, pg)
	return types
}
func kcuColumnTypes(t *testing.T, types []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(types) != 9 {
		t.Fatal("column types", types)
	}
	for i, typ := range types {
		scan := reflect.TypeOf(int64(0))
		if i < 7 {
			scan = reflect.TypeOf("")
		}
		if pg {
			scan = reflect.TypeOf(int32(0))
			if i < 7 {
				scan = reflect.TypeOf((*any)(nil)).Elem()
			}
		}
		nullable, known := typ.Nullable()
		length, hasLength := typ.Length()
		precision, scale, hasDecimal := typ.DecimalSize()
		if typ.Name() != kcuColumns[i] || typ.DatabaseTypeName() != kcuTypes[i] || typ.ScanType() != scan || nullable != !pg || known != !pg || length != 0 || hasLength || precision != 0 || scale != 0 || hasDecimal {
			t.Fatalf("metadata %d: %s %s scan=%v nullable=%v/%v length=%d/%v decimal=%d/%d/%v", i, typ.Name(), typ.DatabaseTypeName(), typ.ScanType(), nullable, known, length, hasLength, precision, scale, hasDecimal)
		}
	}
}
func kcuAssertRows(t *testing.T, rows *sql.Rows, want [][]any) {
	t.Helper()
	defer rows.Close()
	var got [][]any
	for rows.Next() {
		values, targets := make([]any, 9), make([]any, 9)
		for i := range values {
			targets[i] = &values[i]
		}
		if err := rows.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		for i := range values {
			values[i] = fixture.Normalize(values[i])
		}
		got = append(got, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) || len(got) > 0 && !reflect.DeepEqual(got, kcuSorted(want)) {
		t.Fatal("retained rows", fmt.Sprint(got), "want", fmt.Sprint(kcuSorted(want)))
	}
}
