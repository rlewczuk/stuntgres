package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"sort"
	"testing"

	"stuntgres/reference/fixture"
)

var isqColumns = []string{"sequence_catalog", "sequence_schema", "sequence_name", "data_type", "numeric_precision", "numeric_precision_radix", "numeric_scale", "start_value", "minimum_value", "maximum_value", "increment", "cycle_option"}
var isqTypes = []string{"NAME", "NAME", "NAME", "VARCHAR", "INT4", "INT4", "INT4", "VARCHAR", "VARCHAR", "VARCHAR", "VARCHAR", "VARCHAR"}
var isqRoles = []string{"isq_actor", "isq_owner", "isq_grant", "isq_middle", "isq_set", "isq_admin"}

const isqQuery = "SELECT * FROM information_schema.sequences WHERE sequence_name LIKE $1 ORDER BY sequence_schema,sequence_name"

type isqCase struct {
	t        *testing.T
	p        *fixture.Pair
	b        fixture.Backend
	conn     *sql.Conn
	recorder *fixture.Recorder
	database string
}

func isqScenario(t *testing.T, body func(*isqCase)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, isqRoles)
	fixture.SchemaReferenceCleanup(t, "isq_private")
	fixture.SchemaReferenceCleanup(t, "isq_moved")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, conn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			t.Cleanup(func() {
				for _, q := range []string{"ROLLBACK", "RESET ROLE", "RESET SESSION AUTHORIZATION"} {
					if _, err := conn.ExecContext(p.Ctx, q); err != nil {
						t.Error(err)
					}
				}
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			})
			c := &isqCase{t: t, p: p, b: b, conn: conn, recorder: recorder}
			if err := conn.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&c.database); err != nil {
				t.Fatal(err)
			}
			body(c)
			if len(notices) != 0 {
				t.Fatalf("unexpected notices: %+v", notices)
			}
		})
	}
}

func (c *isqCase) run(e fixture.Executor, s fixture.Step) {
	c.t.Helper()
	fixture.RunStep(c.t, c.p.Ctx, e, c.recorder, s)
}

func (c *isqCase) exec(queries ...string) {
	c.t.Helper()
	for _, q := range queries {
		c.run(c.conn, fixture.Exec(q, 0))
	}
}

func (c *isqCase) hold(e fixture.Executor, query string) fixture.HeldStatement {
	c.t.Helper()
	stmt, err := e.PrepareContext(c.p.Ctx, query)
	if err != nil {
		c.t.Fatal(err)
	}
	c.t.Cleanup(func() {
		if err := stmt.Close(); err != nil {
			c.t.Error(err)
		}
	})
	return fixture.HeldStatement{Executor: e, Statement: stmt, SQL: query}
}

func (c *isqCase) peer() *sql.Conn {
	c.t.Helper()
	conn, err := c.b.DB.Conn(c.p.Ctx)
	if err != nil {
		c.t.Fatal(err)
	}
	c.t.Cleanup(func() {
		conn.ExecContext(c.p.Ctx, "ROLLBACK")
		conn.ExecContext(c.p.Ctx, "RESET ROLE")
		if err := conn.Close(); err != nil {
			c.t.Error(err)
		}
	})
	return conn
}

func (c *isqCase) row(schema, name, dataType string, precision int64, start, min, max, increment, cycle string) []any {
	return []any{c.database, schema, name, dataType, precision, int64(2), int64(0), start, min, max, increment, cycle}
}
func (c *isqCase) def(schema, name string) []any {
	return c.row(schema, name, "bigint", 64, "1", "1", "9223372036854775807", "1", "NO")
}
func (c *isqCase) custom(schema, name string) []any {
	return c.row(schema, name, "integer", 32, "15", "-20", "200", "7", "YES")
}
func isqSorted(rows [][]any) [][]any {
	out := append([][]any(nil), rows...)
	sort.Slice(out, func(i, j int) bool {
		if out[i][1] != out[j][1] {
			return out[i][1].(string) < out[j][1].(string)
		}
		return out[i][2].(string) < out[j][2].(string)
	})
	return out
}
func (c *isqCase) step(pattern any, want [][]any) fixture.Step {
	s := fixture.Select(isqQuery, isqColumns, isqTypes, isqSorted(want), pattern)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { isqMetadata(t, rows, c.b.PG) }
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		expected := reflect.TypeFor[string]()
		if column >= 4 && column <= 6 {
			expected = reflect.TypeFor[int64]()
		}
		if c.b.PG && column < 3 {
			expected = reflect.TypeFor[[]byte]()
		}
		if reflect.TypeOf(value) != expected {
			t.Fatalf("raw [%d,%d]=%T(%v), want %v", row, column, value, value, expected)
		}
		return false
	}
	return s
}
func (c *isqCase) check(e fixture.Executor, pattern any, want ...[]any) {
	c.t.Helper()
	s := c.step(pattern, want)
	c.run(e, s)
	if _, held := e.(fixture.HeldStatement); !held {
		c.run(e, fixture.Prepared(s))
	}
}
func (c *isqCase) all(held fixture.HeldStatement, pattern any, want ...[]any) {
	c.t.Helper()
	c.check(c.conn, pattern, want...)
	c.check(held, pattern, want...)
}
func isqMetadata(t *testing.T, rows *sql.Rows, pg bool) []*sql.ColumnType {
	t.Helper()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(columns, isqColumns) {
		t.Fatal("columns", columns)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	isqColumnTypes(t, types, pg)
	return types
}
func isqColumnTypes(t *testing.T, types []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(types) != 12 {
		t.Fatal("column count", len(types))
	}
	for i, typ := range types {
		scan := reflect.TypeFor[string]()
		var length int64
		hasLength := false
		if i >= 4 && i <= 6 {
			scan = reflect.TypeFor[int64]()
			if pg {
				scan = reflect.TypeFor[int32]()
			}
		} else if i < 3 {
			if pg {
				scan = reflect.TypeFor[any]()
			}
		} else {
			length, hasLength = math.MaxInt64, true
			if pg {
				length = -5
			}
			if i == 11 {
				length = 3
			}
		}
		nullable, nullableOK := typ.Nullable()
		gotLength, lengthOK := typ.Length()
		precision, scale, decimalOK := typ.DecimalSize()
		if typ.Name() != isqColumns[i] || typ.DatabaseTypeName() != isqTypes[i] || typ.ScanType() != scan || nullable != !pg || nullableOK != !pg || gotLength != length || lengthOK != hasLength || precision != 0 || scale != 0 || decimalOK {
			t.Fatalf("metadata %d: name=%s type=%s scan=%v nullable=%t/%t length=%d/%t decimal=%d/%d/%t; want scan=%v length=%d/%t nullable=%t/%t", i, typ.Name(), typ.DatabaseTypeName(), typ.ScanType(), nullable, nullableOK, gotLength, lengthOK, precision, scale, decimalOK, scan, length, hasLength, !pg, !pg)
		}
	}
}

func isqConsume(t *testing.T, rows *sql.Rows, pg bool, want [][]any) {
	t.Helper()
	defer rows.Close()
	saved := isqMetadata(t, rows, pg)
	var got [][]any
	for rows.Next() {
		values, targets := make([]any, 12), make([]any, 12)
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
	if len(got) != len(want) || len(got) != 0 && !reflect.DeepEqual(got, isqSorted(want)) {
		t.Fatalf("rows=%v want=%v", got, isqSorted(want))
	}
	isqColumnTypes(t, saved, pg)
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	isqColumnTypes(t, saved, pg)
}
