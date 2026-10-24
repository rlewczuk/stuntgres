package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

const szTable = "information_schema.sql_sizing"
const szQuery = "SELECT sizing_id,sizing_name,supported_value,comments FROM " + szTable
const szComment = "Might be less, depending on character set."

var szColumns = []string{"sizing_id", "sizing_name", "supported_value", "comments"}
var szTypes = []string{"INT4", "VARCHAR", "INT4", "VARCHAR"}

// Independently prescribed from REL_18_6 information_schema.sql and the native
// enforceable-limit decisions. Neither inventory reads the producer or SQL output.
func szExpected(pg bool) [][]any {
	if pg {
		return [][]any{
			{int64(0), "MAXIMUM DRIVER CONNECTIONS", nil, nil},
			{int64(1), "MAXIMUM CONCURRENT ACTIVITIES", int64(0), nil},
			{int64(30), "MAXIMUM COLUMN NAME LENGTH", int64(63), szComment},
			{int64(31), "MAXIMUM CURSOR NAME LENGTH", int64(63), szComment},
			{int64(32), "MAXIMUM SCHEMA NAME LENGTH", int64(63), szComment},
			{int64(34), "MAXIMUM CATALOG NAME LENGTH", int64(63), szComment},
			{int64(35), "MAXIMUM TABLE NAME LENGTH", int64(63), szComment},
			{int64(97), "MAXIMUM COLUMNS IN GROUP BY", int64(0), nil},
			{int64(99), "MAXIMUM COLUMNS IN ORDER BY", int64(0), nil},
			{int64(100), "MAXIMUM COLUMNS IN SELECT", int64(1664), nil},
			{int64(101), "MAXIMUM COLUMNS IN TABLE", int64(1600), nil},
			{int64(106), "MAXIMUM TABLES IN SELECT", int64(0), nil},
			{int64(107), "MAXIMUM USER NAME LENGTH", int64(63), szComment},
			{int64(10005), "MAXIMUM IDENTIFIER LENGTH", int64(63), szComment},
			{int64(20000), "MAXIMUM STATEMENT OCTETS", int64(0), nil},
			{int64(20001), "MAXIMUM STATEMENT OCTETS DATA", int64(0), nil},
			{int64(20002), "MAXIMUM STATEMENT OCTETS SCHEMA", int64(0), nil},
			{int64(25000), "MAXIMUM CURRENT DEFAULT TRANSFORM GROUP LENGTH", nil, nil},
			{int64(25001), "MAXIMUM CURRENT TRANSFORM GROUP LENGTH", nil, nil},
			{int64(25002), "MAXIMUM CURRENT PATH LENGTH", int64(0), nil},
			{int64(25003), "MAXIMUM CURRENT ROLE LENGTH", nil, nil},
			{int64(25004), "MAXIMUM SESSION USER LENGTH", int64(63), szComment},
			{int64(25005), "MAXIMUM SYSTEM USER LENGTH", int64(63), szComment},
		}
	}
	return [][]any{
		{int64(0), "MAXIMUM DRIVER CONNECTIONS", nil, nil},
		{int64(1), "MAXIMUM CONCURRENT ACTIVITIES", int64(0), nil},
		{int64(30), "MAXIMUM COLUMN NAME LENGTH", int64(63), szComment},
		{int64(31), "MAXIMUM CURSOR NAME LENGTH", int64(0), nil},
		{int64(32), "MAXIMUM SCHEMA NAME LENGTH", int64(63), szComment},
		{int64(34), "MAXIMUM CATALOG NAME LENGTH", int64(63), szComment},
		{int64(35), "MAXIMUM TABLE NAME LENGTH", int64(63), szComment},
		{int64(97), "MAXIMUM COLUMNS IN GROUP BY", int64(0), nil},
		{int64(99), "MAXIMUM COLUMNS IN ORDER BY", int64(0), nil},
		{int64(100), "MAXIMUM COLUMNS IN SELECT", int64(0), nil},
		{int64(101), "MAXIMUM COLUMNS IN TABLE", int64(1600), nil},
		{int64(106), "MAXIMUM TABLES IN SELECT", int64(0), nil},
		{int64(107), "MAXIMUM USER NAME LENGTH", int64(63), szComment},
		{int64(10005), "MAXIMUM IDENTIFIER LENGTH", int64(63), szComment},
		{int64(20000), "MAXIMUM STATEMENT OCTETS", int64(0), nil},
		{int64(20001), "MAXIMUM STATEMENT OCTETS DATA", int64(0), nil},
		{int64(20002), "MAXIMUM STATEMENT OCTETS SCHEMA", int64(0), nil},
		{int64(25000), "MAXIMUM CURRENT DEFAULT TRANSFORM GROUP LENGTH", nil, nil},
		{int64(25001), "MAXIMUM CURRENT TRANSFORM GROUP LENGTH", nil, nil},
		{int64(25002), "MAXIMUM CURRENT PATH LENGTH", int64(0), nil},
		{int64(25003), "MAXIMUM CURRENT ROLE LENGTH", nil, nil},
		{int64(25004), "MAXIMUM SESSION USER LENGTH", int64(63), szComment},
		{int64(25005), "MAXIMUM SYSTEM USER LENGTH", int64(0), nil},
	}
}

func szFilter(rows [][]any, keep func([]any) bool) [][]any {
	var result [][]any
	for _, row := range rows {
		if keep(row) {
			result = append(result, row)
		}
	}
	return result
}

func szItem(rows [][]any, id any) [][]any {
	return szFilter(rows, func(row []any) bool { return row[0] == id })
}

type szCase struct {
	t       *testing.T
	p       *fixture.Pair
	b       fixture.Backend
	conn    *sql.Conn
	want    [][]any
	notices *[]fixture.Diagnostic
}

func szScenario(t *testing.T, roles bool, body func(*szCase)) {
	t.Helper()
	if roles {
		fixture.RoleReferenceCleanup(t, []string{"sz_owner", "sz_reader"})
	}
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, conn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			b.Recorder = recorder
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "RESET ROLE"} {
					if _, err := conn.ExecContext(ctx, q); err != nil {
						t.Error(err)
					}
				}
			})
			body(&szCase{t: t, p: p, b: b, conn: conn, want: szExpected(b.PG), notices: &notices})
		})
	}
}

func (c *szCase) run(e fixture.Executor, s fixture.Step) {
	c.t.Helper()
	fixture.RunStep(c.t, c.p.Ctx, e, c.b.Recorder, s)
}

func (c *szCase) exec(queries ...string) {
	c.t.Helper()
	for _, q := range queries {
		c.run(c.conn, fixture.Exec(q, 0))
	}
}

func (c *szCase) hold(q string) fixture.HeldStatement {
	c.t.Helper()
	stmt, err := c.conn.PrepareContext(c.p.Ctx, q)
	if err != nil {
		c.t.Fatal(err)
	}
	c.t.Cleanup(func() {
		if err := stmt.Close(); err != nil {
			c.t.Error(err)
		}
	})
	return fixture.HeldStatement{Executor: c.conn, Statement: stmt, SQL: q}
}

func szMetadata(t *testing.T, columns []*sql.ColumnType, names, types []string, pg bool) {
	t.Helper()
	if len(columns) != len(names) {
		t.Fatalf("columns=%d want %d", len(columns), len(names))
	}
	for i, col := range columns {
		var wantLength int64
		wantLengthOK := types[i] == "VARCHAR"
		scan := reflect.TypeFor[int64]()
		if pg {
			scan = reflect.TypeFor[int32]()
		}
		if wantLengthOK {
			scan = reflect.TypeFor[string]()
			wantLength = 9223372036854775807
			if pg {
				wantLength = -5
			}
		}
		length, lengthOK := col.Length()
		nullable, nullableOK := col.Nullable()
		precision, scale, decimalOK := col.DecimalSize()
		if col.Name() != names[i] || col.DatabaseTypeName() != types[i] || col.ScanType() != scan || length != wantLength || lengthOK != wantLengthOK || nullable != !pg || nullableOK != !pg || precision != 0 || scale != 0 || decimalOK {
			t.Fatalf("column %d: name=%q type=%q scan=%v length=%d/%t nullable=%t/%t decimal=%d/%d/%t", i, col.Name(), col.DatabaseTypeName(), col.ScanType(), length, lengthOK, nullable, nullableOK, precision, scale, decimalOK)
		}
	}
}

func szRaw(t *testing.T, typ string, value any) {
	t.Helper()
	if value == nil {
		return
	}
	if typ == "INT4" {
		if _, ok := value.(int64); !ok {
			t.Fatalf("raw=%T(%v), want int64", value, value)
		}
	} else if _, ok := value.(string); !ok {
		t.Fatalf("raw=%T(%v), want string", value, value)
	}
}

func (c *szCase) projection(q string, want [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, szColumns, szTypes, want, args...)
	s.Bag = true
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		szMetadata(t, columns, szColumns, szTypes, c.b.PG)
	}
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		szRaw(t, szTypes[column], value)
		return true
	}
	return s
}

func (c *szCase) check(e fixture.Executor, q string, want [][]any, args ...any) {
	c.t.Helper()
	s := c.projection(q, want, args...)
	c.run(e, s)
	c.run(c.conn, fixture.Prepared(s))
}
