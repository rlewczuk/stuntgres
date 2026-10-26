package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

const icoQuery = "SELECT * FROM information_schema.collations"
const icoKnown = "collation_schema='pg_catalog' AND collation_name IN ('C','POSIX','default','pg_c_utf8','pg_unicode_fast','ucs_basic','unicode')"

var icoColumns = []string{"collation_catalog", "collation_schema", "collation_name", "pad_attribute"}
var icoTypes = []string{"NAME", "NAME", "NAME", "VARCHAR"}
var icoNames = []string{"C", "POSIX", "default", "pg_c_utf8", "pg_unicode_fast", "ucs_basic", "unicode"}

type icoCase struct {
	t *testing.T
	p *fixture.Pair
	b fixture.Backend
	conn *sql.Conn
	database string
}

func icoScenario(t *testing.T, roles bool, body func(*icoCase)) {
	t.Helper()
	if roles {
		fixture.RoleReferenceCleanup(t, []string{"icoll_owner", "icoll_reader"})
	}
	fixture.SchemaReferenceCleanup(t, "icoll_lifecycle", "icoll_moved", "icoll_hidden")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil { t.Fatal(err) }
			t.Cleanup(func() {
				defer conn.Close()
				if _, err := conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil { t.Error(err) }
			})
			c := &icoCase{t:t, p:p, b:b, conn:conn}
			if err := conn.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&c.database); err != nil { t.Fatal(err) }
			body(c)
		})
	}
}

func (c *icoCase) run(e fixture.Executor, s fixture.Step) {
	c.t.Helper()
	fixture.RunStep(c.t, c.p.Ctx, e, c.b.Recorder, s)
}

func (c *icoCase) exec(queries ...string) {
	c.t.Helper()
	for _, q := range queries { c.run(c.conn, fixture.Exec(q, 0)) }
}

func (c *icoCase) hold(q string) fixture.HeldStatement {
	c.t.Helper()
	stmt, err := c.conn.PrepareContext(c.p.Ctx, q)
	if err != nil { c.t.Fatal(err) }
	c.t.Cleanup(func() { if err := stmt.Close(); err != nil { c.t.Error(err) } })
	return fixture.HeldStatement{Executor:c.conn, Statement:stmt, SQL:q}
}

func (c *icoCase) row(schema, name string) [][]any {
	return [][]any{{c.database, schema, name, "NO PAD"}}
}

func (c *icoCase) seeds() [][]any {
	var rows [][]any
	for _, name := range icoNames { rows = append(rows, c.row("pg_catalog", name)[0]) }
	return rows
}

func icoMetadata(t *testing.T, columns []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(columns) != 4 { t.Fatalf("columns=%d want 4", len(columns)) }
	for i, col := range columns {
		scan := reflect.TypeFor[string]()
		if pg && i < 3 { scan = reflect.TypeFor[any]() }
		length, lengthOK := col.Length()
		wantLength, wantLengthOK := int64(0), false
		if i == 3 {
			wantLength, wantLengthOK = math.MaxInt64, true
			if pg { wantLength = -5 }
		}
		precision, scale, decimalOK := col.DecimalSize()
		nullable, nullableOK := col.Nullable()
		if col.Name() != icoColumns[i] || col.DatabaseTypeName() != icoTypes[i] || col.ScanType() != scan || length != wantLength || lengthOK != wantLengthOK || precision != 0 || scale != 0 || decimalOK || nullable != !pg || nullableOK != !pg {
			t.Fatalf("column %d name=%q type=%q scan=%v length=%d/%t decimal=%d/%d/%t nullable=%t/%t", i, col.Name(), col.DatabaseTypeName(), col.ScanType(), length, lengthOK, precision, scale, decimalOK, nullable, nullableOK)
		}
	}
}

func (c *icoCase) projection(q string, want [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, icoColumns, icoTypes, want, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil { t.Fatal(err) }
		icoMetadata(t, columns, c.b.PG)
	}
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if want[row][column] == nil {
			if value != nil { t.Fatalf("raw NULL=%T(%v)", value, value) }
			return true
		}
		text, ok := value.(string)
		if c.b.PG && column < 3 {
			bytes, bytesOK := value.([]byte)
			text, ok = string(bytes), bytesOK
		}
		if !ok || text != want[row][column] { t.Fatalf("raw [%d,%d]=%T(%v) want %v", row, column, value, value, want[row][column]) }
		return true
	}
	return s
}

func (c *icoCase) routes(s fixture.Step) {
	c.t.Helper()
	c.run(c.conn, s)
	c.run(c.conn, fixture.Prepared(s))
	c.run(c.hold(s.Query), s)
}

// consume checks retained descriptors after EOF and explicit Close, including
// zero-row and null-extended results; raw cells are checked before normalization.
func (c *icoCase) consume(rows *sql.Rows, want [][]any) {
	c.t.Helper()
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil { c.t.Fatal(err) }
	icoMetadata(c.t, columns, c.b.PG)
	s := c.projection("", want)
	n := 0
	for rows.Next() {
		if n >= len(want) { c.t.Fatal("unexpected extra row") }
		values := make([]any, 4)
		dest := []any{&values[0], &values[1], &values[2], &values[3]}
		if err := rows.Scan(dest...); err != nil { c.t.Fatal(err) }
		for i, v := range values { s.CheckRaw(c.t, i, n, v) }
		n++
	}
	if rows.Err() != nil || n != len(want) { c.t.Fatalf("rows=%d want=%d error=%v", n, len(want), rows.Err()) }
	icoMetadata(c.t, columns, c.b.PG)
	if err := rows.Close(); err != nil { c.t.Fatal(err) }
	icoMetadata(c.t, columns, c.b.PG)
}
