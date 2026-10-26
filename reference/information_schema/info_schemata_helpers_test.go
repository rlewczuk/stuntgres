package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

const iscQuery = "SELECT * FROM information_schema.schemata"

var iscColumns = []string{"catalog_name", "schema_name", "schema_owner", "default_character_set_catalog", "default_character_set_schema", "default_character_set_name", "sql_path"}
var iscTypes = []string{"NAME", "NAME", "NAME", "NAME", "NAME", "NAME", "VARCHAR"}

type iscCase struct {
	t        *testing.T
	p        *fixture.Pair
	b        fixture.Backend
	conn     *sql.Conn
	database string
}

func iscScenario(t *testing.T, body func(*iscCase)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"isc_owner", "isc_other_owner", "isc_reader", "isc_group", "isc_member", "isc_set"})
	fixture.SchemaReferenceCleanup(t, "isc_owned", "isc_renamed", "isc_hidden", "isc_smoke", "isc_staged")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				defer conn.Close()
				if _, err := conn.ExecContext(p.Ctx, "RESET ROLE; RESET SESSION AUTHORIZATION"); err != nil {
					t.Error(err)
				}
			})
			c := &iscCase{t: t, p: p, b: b, conn: conn}
			if err := conn.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&c.database); err != nil {
				t.Fatal(err)
			}
			body(c)
		})
	}
}
func (c *iscCase) run(e fixture.Executor, s fixture.Step) {
	c.t.Helper()
	fixture.RunStep(c.t, c.p.Ctx, e, c.b.Recorder, s)
}
func (c *iscCase) exec(queries ...string) {
	c.t.Helper()
	for _, q := range queries {
		c.run(c.conn, fixture.Exec(q, 0))
	}
}
func (c *iscCase) hold(q string) fixture.HeldStatement {
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
func (c *iscCase) row(schema, owner string) [][]any {
	return [][]any{{c.database, schema, owner, nil, nil, nil, nil}}
}
func iscMetadata(t *testing.T, columns []*sql.ColumnType, pg bool) {
	t.Helper()
	iscProjectionMetadata(t, columns, pg, 0)
}
func iscProjectionMetadata(t *testing.T, columns []*sql.ColumnType, pg bool, start int) {
	t.Helper()
	if len(columns) != 7-start {
		t.Fatalf("columns=%d want %d", len(columns), 7-start)
	}
	for offset, col := range columns {
		i := start + offset
		scan := reflect.TypeFor[string]()
		if pg && i < 6 {
			scan = reflect.TypeFor[any]()
		}
		length, lengthOK := col.Length()
		wantLength, wantLengthOK := int64(0), false
		if i == 6 {
			wantLength, wantLengthOK = math.MaxInt64, true
			if pg {
				wantLength = -5
			}
		}
		precision, scale, decimalOK := col.DecimalSize()
		nullable, nullableOK := col.Nullable()
		if col.Name() != iscColumns[i] || col.DatabaseTypeName() != iscTypes[i] || col.ScanType() != scan || length != wantLength || lengthOK != wantLengthOK || precision != 0 || scale != 0 || decimalOK || nullable != !pg || nullableOK != !pg {
			t.Fatalf("column %d name=%q type=%q scan=%v length=%d/%t decimal=%d/%d/%t nullable=%t/%t", i, col.Name(), col.DatabaseTypeName(), col.ScanType(), length, lengthOK, precision, scale, decimalOK, nullable, nullableOK)
		}
	}
}
func (c *iscCase) projection(q string, want [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, iscColumns, iscTypes, want, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		iscMetadata(t, columns, c.b.PG)
	}
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if want[row][column] == nil {
			if value != nil {
				t.Fatalf("raw NULL=%T(%v)", value, value)
			}
			return true
		}
		text, ok := value.(string)
		if c.b.PG && column < 6 {
			b, yes := value.([]byte)
			text, ok = string(b), yes
		}
		if !ok || text != want[row][column] {
			t.Fatalf("raw [%d,%d]=%T(%v) want %v", row, column, value, value, want[row][column])
		}
		return true
	}
	return s
}
func (c *iscCase) routes(s fixture.Step) {
	c.t.Helper()
	c.run(c.conn, s)
	c.run(c.conn, fixture.Prepared(s))
	c.run(c.hold(s.Query), s)
}
func (c *iscCase) check(held fixture.HeldStatement, s fixture.Step) {
	c.t.Helper()
	c.run(c.conn, s)
	c.run(c.conn, fixture.Prepared(s))
	c.run(held, s)
}
func (c *iscCase) consume(rows *sql.Rows, want [][]any) {
	c.t.Helper()
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil {
		c.t.Fatal(err)
	}
	iscMetadata(c.t, columns, c.b.PG)
	s := c.projection("", want)
	n := 0
	for rows.Next() {
		if n >= len(want) {
			c.t.Fatal("unexpected extra row")
		}
		var values [7]any
		if err := rows.Scan(&values[0], &values[1], &values[2], &values[3], &values[4], &values[5], &values[6]); err != nil {
			c.t.Fatal(err)
		}
		for i, v := range values {
			s.CheckRaw(c.t, i, n, v)
		}
		n++
	}
	if rows.Err() != nil || n != len(want) {
		c.t.Fatalf("rows=%d want=%d error=%v", n, len(want), rows.Err())
	}
	iscMetadata(c.t, columns, c.b.PG)
	if err := rows.Close(); err != nil {
		c.t.Fatal(err)
	}
	iscMetadata(c.t, columns, c.b.PG)
}
func (c *iscCase) identity() {
	var attributes [][]any
	for i, name := range iscColumns {
		domain, base := "sql_identifier", int64(19)
		if i == 6 {
			domain, base = "character_data", 1043
		}
		attributes = append(attributes, []any{int64(i + 1), name, "information_schema", domain, "d", base, int64(-1), false, int64(950), int64(0), false})
	}
	s := fixture.Select(`SELECT a.attnum,a.attname,n.nspname,t.typname,t.typtype,t.typbasetype,a.atttypmod,a.attnotnull,a.attcollation,a.attndims,t.typnotnull FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_type t ON t.oid=a.atttypid JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE a.attrelid='information_schema.schemata'::regclass AND a.attnum>0 ORDER BY a.attnum`, []string{"attnum", "attname", "nspname", "typname", "typtype", "typbasetype", "atttypmod", "attnotnull", "attcollation", "attndims", "typnotnull"}, []string{"INT2", "NAME", "NAME", "NAME", "CHAR", "OID", "INT4", "BOOL", "OID", "INT2", "BOOL"}, attributes)
	for _, route := range []fixture.Executor{c.conn, c.hold(s.Query)} {
		fixture.PgDescriptionExecutorRows(c.t, c.p, c.b, route, s)
	}
	fixture.PgDescriptionExecutorRows(c.t, c.p, c.b, c.conn, fixture.Prepared(s))
	c.routes(fixture.Select(`SELECT c.relkind,t.typname,t.typrelid=c.oid AS same_relation,c.reltype=t.oid AS same_type,a.typelem=t.oid AS same_element,a.typnamespace=t.typnamespace AS same_namespace,n.nspname,a.typname AS array_name FROM pg_catalog.pg_class c JOIN pg_catalog.pg_type t ON t.oid=c.reltype JOIN pg_catalog.pg_type a ON a.oid=t.typarray JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE c.oid='information_schema.schemata'::regclass`, []string{"relkind", "typname", "same_relation", "same_type", "same_element", "same_namespace", "nspname", "array_name"}, []string{"CHAR", "NAME", "BOOL", "BOOL", "BOOL", "BOOL", "NAME", "NAME"}, [][]any{{"v", "schemata", true, true, true, true, "information_schema", "_schemata"}}))
}
