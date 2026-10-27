package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

const trQuery = "SELECT * FROM information_schema.transforms"
const trProjection = "udt_catalog,udt_schema,udt_name,specific_catalog,specific_schema,specific_name,group_name,transform_type"
const trNullQuery = "SELECT t.* FROM (SELECT 1 AS n) s LEFT JOIN information_schema.transforms t ON false"

var trColumns = []string{"udt_catalog", "udt_schema", "udt_name", "specific_catalog", "specific_schema", "specific_name", "group_name", "transform_type"}
var trTypes = []string{"NAME", "NAME", "NAME", "NAME", "NAME", "NAME", "NAME", "VARCHAR"}
var trNullRow = [][]any{{nil, nil, nil, nil, nil, nil, nil, nil}}

type trCase struct {
	t        *testing.T
	p        *fixture.Pair
	b        fixture.Backend
	conn     *sql.Conn
	database string
}

func trScenario(t *testing.T, body func(*trCase)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"tr_reader"})
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			defer func() {
				if _, err := conn.ExecContext(p.Ctx, "SET ROLE NONE"); err != nil {
					t.Error(err)
				}
			}()
			c := &trCase{t: t, p: p, b: backend, conn: conn}
			if err := conn.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&c.database); err != nil {
				t.Fatal(err)
			}
			body(c)
		})
	}
}

func (c *trCase) run(e fixture.Executor, s fixture.Step) {
	c.t.Helper()
	fixture.RunStep(c.t, c.p.Ctx, e, c.b.Recorder, s)
}
func (c *trCase) exec(queries ...string) {
	c.t.Helper()
	for _, q := range queries {
		c.run(c.conn, fixture.Exec(q, 0))
	}
}
func (c *trCase) hold(q string) fixture.HeldStatement {
	c.t.Helper()
	s, err := c.conn.PrepareContext(c.p.Ctx, q)
	if err != nil {
		c.t.Fatal(err)
	}
	c.t.Cleanup(func() {
		if err := s.Close(); err != nil {
			c.t.Error(err)
		}
	})
	return fixture.HeldStatement{Executor: c.conn, Statement: s, SQL: q}
}
func trMetadata(t *testing.T, columns []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(columns) != 8 {
		t.Fatalf("width=%d want 8", len(columns))
	}
	for i, col := range columns {
		scan := reflect.TypeFor[string]()
		if pg && i < 7 {
			scan = reflect.TypeFor[any]()
		}
		length, lengthOK := col.Length()
		wantLength, wantLengthOK := int64(0), false
		if i == 7 {
			wantLength, wantLengthOK = math.MaxInt64, true
			if pg {
				wantLength = -5
			}
		}
		precision, scale, decimalOK := col.DecimalSize()
		nullable, nullableOK := col.Nullable()
		if col.Name() != trColumns[i] || col.DatabaseTypeName() != trTypes[i] || col.ScanType() != scan || length != wantLength || lengthOK != wantLengthOK || precision != 0 || scale != 0 || decimalOK || nullable != !pg || nullableOK != !pg {
			t.Fatalf("column %d: %s/%s scan=%v length=%d/%t decimal=%d/%d/%t nullable=%t/%t", i, col.Name(), col.DatabaseTypeName(), col.ScanType(), length, lengthOK, precision, scale, decimalOK, nullable, nullableOK)
		}
	}
}
func (c *trCase) step(q string, want [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, trColumns, trTypes, want, args...)
	s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
		columns, err := r.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		trMetadata(t, columns, c.b.PG)
	}
	s.CheckRaw = func(t *testing.T, column, row int, v any) bool {
		if v != nil {
			t.Fatalf("NULL extension cell [%d,%d]=%T(%v)", row, column, v, v)
		}
		return true
	}
	return s
}
func (c *trCase) routes(q string, want [][]any, args ...any) {
	c.t.Helper()
	s := c.step(q, want, args...)
	c.run(c.conn, s)
	c.run(c.conn, fixture.Prepared(s))
	held := c.hold(q)
	c.run(held, s)
	if err := held.Statement.Close(); err != nil {
		c.t.Fatal(err)
	}
}
func (c *trCase) consume(rows *sql.Rows, want int) []*sql.ColumnType {
	c.t.Helper()
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil {
		c.t.Fatal(err)
	}
	trMetadata(c.t, columns, c.b.PG)
	n := 0
	for rows.Next() {
		var values [8]any
		var destinations [8]any
		for i := range values {
			destinations[i] = &values[i]
		}
		if err := rows.Scan(destinations[:]...); err != nil {
			c.t.Fatal(err)
		}
		for i, v := range values {
			if v != nil {
				c.t.Fatalf("cell %d=%T(%v), want NULL", i, v, v)
			}
		}
		n++
	}
	if rows.Err() != nil || n != want {
		c.t.Fatalf("rows=%d want=%d error=%v", n, want, rows.Err())
	}
	trMetadata(c.t, columns, c.b.PG)
	if err := rows.Close(); err != nil {
		c.t.Fatal(err)
	}
	trMetadata(c.t, columns, c.b.PG)
	return columns
}
func (c *trCase) inventory(e fixture.Executor) {
	c.t.Helper()
	c.run(e, c.step(trQuery, nil))
	c.run(e, fixture.Select("SELECT count(*)::bigint AS n FROM information_schema.transforms", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	s := fixture.Select("SELECT oid::bigint AS oid,trftype::bigint AS trftype,trflang::bigint AS trflang,trffromsql::oid::bigint AS trffromsql,trftosql::oid::bigint AS trftosql FROM pg_catalog.pg_transform", []string{"oid", "trftype", "trflang", "trffromsql", "trftosql"}, []string{"INT8", "INT8", "INT8", "INT8", "INT8"}, nil)
	s.Bag = true
	c.run(e, s)
}
func trDiagnostic(code, message string) fixture.Diagnostic {
	return fixture.ControlsReferenceDiagnostic(code, message)
}
