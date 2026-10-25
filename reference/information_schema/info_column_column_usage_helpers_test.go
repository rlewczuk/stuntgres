package reference_test

import (
	"database/sql"
	"reflect"
	"sort"
	"testing"

	"stuntgres/reference/fixture"
)

var colcuColumns = []string{"table_catalog", "table_schema", "table_name", "column_name", "dependent_column"}
var colcuTypes = []string{"NAME", "NAME", "NAME", "NAME", "NAME"}

const colcuQuery = "SELECT * FROM information_schema.column_column_usage WHERE table_name LIKE $1 ORDER BY table_catalog,table_schema,table_name,column_name,dependent_column"
const colcuSeed = `CREATE TABLE colcu_usage(a integer,b integer,s integer GENERATED ALWAYS AS(a+a+b) STORED,v integer GENERATED ALWAYS AS(b+1) VIRTUAL,c integer GENERATED ALWAYS AS(7) STORED,n integer GENERATED ALWAYS AS(NULL::integer) STORED,z boolean GENERATED ALWAYS AS(CASE WHEN false THEN a>0 ELSE true END) STORED,o oid GENERATED ALWAYS AS(tableoid) VIRTUAL)`

type colcuCase struct {
	t        *testing.T
	p        *fixture.Pair
	b        fixture.Backend
	conn     *sql.Conn
	database string
}

func colcuScenario(t *testing.T, body func(*colcuCase)) {
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
			c := &colcuCase{t: t, p: p, b: b, conn: conn}
			if err := conn.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&c.database); err != nil {
				t.Fatal(err)
			}
			body(c)
		})
	}
}
func (c *colcuCase) run(e fixture.Executor, s fixture.Step) {
	c.t.Helper()
	fixture.RunStep(c.t, c.p.Ctx, e, c.b.Recorder, s)
}
func (c *colcuCase) exec(queries ...string) {
	c.t.Helper()
	for _, q := range queries {
		c.run(c.conn, fixture.Exec(q, 0))
	}
}
func (c *colcuCase) hold(e fixture.Executor, q string) fixture.HeldStatement {
	c.t.Helper()
	s, err := e.PrepareContext(c.p.Ctx, q)
	if err != nil {
		c.t.Fatal(err)
	}
	c.t.Cleanup(func() { s.Close() })
	return fixture.HeldStatement{Executor: e, Statement: s, SQL: q}
}
func (c *colcuCase) row(schema, table, base, dependent string) []any {
	return []any{c.database, schema, table, base, dependent}
}
func (c *colcuCase) pub(table, base, dependent string) []any {
	return c.row("public", table, base, dependent)
}
func (c *colcuCase) seedRows() [][]any {
	return [][]any{c.pub("colcu_usage", "a", "s"), c.pub("colcu_usage", "b", "s"), c.pub("colcu_usage", "b", "v"), c.pub("colcu_usage", "a", "z"), c.pub("colcu_usage", "tableoid", "o")}
}
func colcuSorted(rows [][]any) [][]any {
	result := append([][]any(nil), rows...)
	sort.SliceStable(result, func(i, j int) bool {
		for n := range colcuColumns {
			if result[i][n] != result[j][n] {
				return result[i][n].(string) < result[j][n].(string)
			}
		}
		return false
	})
	return result
}
func (c *colcuCase) step(pattern any, rows [][]any) fixture.Step {
	want := colcuSorted(rows)
	s := fixture.Select(colcuQuery, colcuColumns, colcuTypes, want, pattern)
	s.CheckMetadata = func(t *testing.T, r *sql.Rows) { colcuMetadata(t, r, c.b.PG) }
	s.CheckScan = func(t *testing.T, r *sql.Rows, index int) {
		values, targets := make([]any, 5), make([]any, 5)
		for i := range values {
			targets[i] = &values[i]
		}
		if err := r.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		typ := reflect.TypeOf("")
		if c.b.PG {
			typ = reflect.TypeOf([]byte(nil))
		}
		for i, v := range values {
			if v == nil || reflect.TypeOf(v) != typ || fixture.Normalize(v) != want[index][i] {
				t.Fatalf("raw cell %d/%d: %T(%v), want %v(%v)", index, i, v, v, typ, want[index][i])
			}
		}
	}
	return s
}
func (c *colcuCase) check(e fixture.Executor, pattern any, rows ...[]any) {
	c.t.Helper()
	s := c.step(pattern, rows)
	c.run(e, s)
	if _, held := e.(fixture.HeldStatement); !held {
		c.run(e, fixture.Prepared(s))
	}
}
func colcuMetadata(t *testing.T, r *sql.Rows, pg bool) []*sql.ColumnType {
	t.Helper()
	names, err := r.Columns()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(names, colcuColumns) {
		t.Fatal("columns", names)
	}
	types, err := r.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	colcuColumnTypes(t, types, pg)
	return types
}
func colcuColumnTypes(t *testing.T, types []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(types) != 5 {
		t.Fatal("types", types)
	}
	for i, typ := range types {
		scan := reflect.TypeOf("")
		if pg {
			scan = reflect.TypeOf((*any)(nil)).Elem()
		}
		nullable, known := typ.Nullable()
		length, hasLength := typ.Length()
		precision, scale, hasDecimal := typ.DecimalSize()
		if typ.Name() != colcuColumns[i] || typ.DatabaseTypeName() != "NAME" || typ.ScanType() != scan || nullable != !pg || known != !pg || length != 0 || hasLength || precision != 0 || scale != 0 || hasDecimal {
			t.Fatalf("metadata %d: %s %s scan=%v nullable=%v/%v length=%d/%v decimal=%d/%d/%v", i, typ.Name(), typ.DatabaseTypeName(), typ.ScanType(), nullable, known, length, hasLength, precision, scale, hasDecimal)
		}
	}
}
func colcuAssertRows(t *testing.T, r *sql.Rows, want [][]any) {
	t.Helper()
	defer r.Close()
	var got [][]any
	for r.Next() {
		values, targets := make([]any, 5), make([]any, 5)
		for i := range values {
			targets[i] = &values[i]
		}
		if err := r.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		for i := range values {
			values[i] = fixture.Normalize(values[i])
		}
		got = append(got, values)
	}
	if err := r.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) || len(got) > 0 && !reflect.DeepEqual(got, colcuSorted(want)) {
		t.Fatal("rows", got, "want", colcuSorted(want))
	}
}
