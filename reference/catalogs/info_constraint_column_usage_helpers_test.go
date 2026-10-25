package reference_test

import (
	"database/sql"
	"reflect"
	"sort"
	"testing"

	"stuntgres/reference/fixture"
)

var ccuTypes = []string{"NAME", "NAME", "NAME", "NAME", "NAME", "NAME", "NAME"}

const ccuQuery = "SELECT * FROM information_schema.constraint_column_usage WHERE table_name LIKE $1 ORDER BY table_catalog,table_schema,table_name,column_name,constraint_catalog,constraint_schema,constraint_name"

type ccuCase struct {
	t        *testing.T
	p        *fixture.Pair
	b        fixture.Backend
	conn     *sql.Conn
	database string
}

func ccuScenario(t *testing.T, body func(*ccuCase)) {
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
			c := &ccuCase{t: t, p: p, b: b, conn: conn}
			if err := conn.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&c.database); err != nil {
				t.Fatal(err)
			}
			body(c)
		})
	}
}

func (c *ccuCase) run(e fixture.Executor, s fixture.Step) {
	c.t.Helper()
	fixture.RunStep(c.t, c.p.Ctx, e, c.b.Recorder, s)
}
func (c *ccuCase) exec(queries ...string) {
	c.t.Helper()
	for _, q := range queries {
		c.run(c.conn, fixture.Exec(q, 0))
	}
}
func (c *ccuCase) hold(e fixture.Executor, q string) fixture.HeldStatement {
	c.t.Helper()
	stmt, err := e.PrepareContext(c.p.Ctx, q)
	if err != nil {
		c.t.Fatal(err)
	}
	c.t.Cleanup(func() { stmt.Close() })
	return fixture.HeldStatement{Executor: e, Statement: stmt, SQL: q}
}
func (c *ccuCase) row(tableSchema, table, column, constraintSchema, constraint string) []any {
	return []any{c.database, tableSchema, table, column, c.database, constraintSchema, constraint}
}
func (c *ccuCase) pub(table, column, constraint string) []any {
	return c.row("public", table, column, "public", constraint)
}
func ccuSorted(rows [][]any) [][]any {
	result := append([][]any(nil), rows...)
	sort.SliceStable(result, func(i, j int) bool {
		for n := range fixture.CCUColumns {
			if result[i][n] != result[j][n] {
				return result[i][n].(string) < result[j][n].(string)
			}
		}
		return false
	})
	return result
}
func (c *ccuCase) step(pattern any, rows [][]any) fixture.Step {
	want := ccuSorted(rows)
	s := fixture.Select(ccuQuery, fixture.CCUColumns, ccuTypes, want, pattern)
	s.CheckMetadata = func(t *testing.T, r *sql.Rows) { ccuMetadata(t, r, c.b.PG) }
	s.CheckScan = func(t *testing.T, r *sql.Rows, index int) {
		values, targets := make([]any, 7), make([]any, 7)
		for i := range values {
			targets[i] = &values[i]
		}
		if err := r.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		for i, v := range values {
			typ := reflect.TypeOf("")
			if c.b.PG {
				typ = reflect.TypeOf([]byte(nil))
			}
			if v == nil || reflect.TypeOf(v) != typ || fixture.Normalize(v) != want[index][i] {
				t.Fatalf("raw cell row=%d column=%d: %T(%v), want %v(%v)", index, i, v, v, typ, want[index][i])
			}
		}
	}
	return s
}
func (c *ccuCase) check(e fixture.Executor, pattern any, rows ...[]any) {
	c.t.Helper()
	s := c.step(pattern, rows)
	c.run(e, s)
	if _, held := e.(fixture.HeldStatement); !held {
		c.run(e, fixture.Prepared(s))
	}
}
func ccuMetadata(t *testing.T, rows *sql.Rows, pg bool) []*sql.ColumnType {
	t.Helper()
	names, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(names, fixture.CCUColumns) {
		t.Fatal("columns", names, "want", fixture.CCUColumns)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	ccuColumnTypes(t, types, pg)
	return types
}
func ccuColumnTypes(t *testing.T, types []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(types) != 7 {
		t.Fatal("column types", types)
	}
	for i, typ := range types {
		scan := reflect.TypeOf("")
		if pg {
			scan = reflect.TypeOf((*any)(nil)).Elem()
		}
		nullable, known := typ.Nullable()
		length, hasLength := typ.Length()
		precision, scale, hasDecimal := typ.DecimalSize()
		if typ.Name() != fixture.CCUColumns[i] || typ.DatabaseTypeName() != "NAME" || typ.ScanType() != scan || nullable != !pg || known != !pg || length != 0 || hasLength || precision != 0 || scale != 0 || hasDecimal {
			t.Fatalf("metadata %d: %s %s scan=%v nullable=%v/%v length=%d/%v decimal=%d/%d/%v", i, typ.Name(), typ.DatabaseTypeName(), typ.ScanType(), nullable, known, length, hasLength, precision, scale, hasDecimal)
		}
	}
}
func ccuAssertRows(t *testing.T, rows *sql.Rows, want [][]any) {
	t.Helper()
	defer rows.Close()
	var got [][]any
	for rows.Next() {
		values, targets := make([]any, 7), make([]any, 7)
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
	if len(got) != len(want) || len(got) > 0 && !reflect.DeepEqual(got, ccuSorted(want)) {
		t.Fatal("rows", got, "want", ccuSorted(want))
	}
}
