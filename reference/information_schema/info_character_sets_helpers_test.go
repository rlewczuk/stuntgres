package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

const csQuery = "SELECT * FROM information_schema.character_sets"

var csColumns = []string{"character_set_catalog", "character_set_schema", "character_set_name", "character_repertoire", "form_of_use", "default_collate_catalog", "default_collate_schema", "default_collate_name"}
var csTypes = []string{"NAME", "NAME", "NAME", "NAME", "NAME", "NAME", "NAME", "NAME"}

type csCase struct {
	t        *testing.T
	p        *fixture.Pair
	b        fixture.Backend
	conn     *sql.Conn
	database string
}

func csScenario(t *testing.T, roles bool, body func(*csCase)) {
	t.Helper()
	if roles {
		fixture.RoleReferenceCleanup(t, []string{"cs_owner", "cs_reader"})
	}
	fixture.SchemaReferenceCleanup(t, "cs_lifecycle", "cs_visibility")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				defer conn.Close()
				for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION"} {
					if _, err := conn.ExecContext(p.Ctx, q); err != nil {
						t.Error(err)
					}
				}
			})
			c := &csCase{t: t, p: p, b: b, conn: conn}
			if err := conn.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&c.database); err != nil {
				t.Fatal(err)
			}
			if b.PG && c.database != "stuntgres_reference" {
				t.Fatalf("oracle database=%q", c.database)
			}
			body(c)
		})
	}
}

func (c *csCase) run(e fixture.Executor, s fixture.Step) {
	c.t.Helper()
	fixture.RunStep(c.t, c.p.Ctx, e, c.b.Recorder, s)
}

func (c *csCase) exec(queries ...string) {
	c.t.Helper()
	for _, q := range queries {
		c.run(c.conn, fixture.Exec(q, 0))
	}
}

func (c *csCase) hold(q string) fixture.HeldStatement {
	c.t.Helper()
	stmt, err := c.conn.PrepareContext(c.p.Ctx, q)
	if err != nil {
		c.t.Fatal(err)
	}
	c.t.Cleanup(func() { _ = stmt.Close() })
	return fixture.HeldStatement{Executor: c.conn, Statement: stmt, SQL: q}
}

func (c *csCase) rows(schema, name string) [][]any {
	return [][]any{{nil, nil, "UTF8", "UCS", "UTF8", c.database, schema, name}}
}

func csMetadata(t *testing.T, columns []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(columns) != len(csColumns) {
		t.Fatalf("columns=%d want %d", len(columns), len(csColumns))
	}
	for i, col := range columns {
		scan := reflect.TypeFor[string]()
		if pg {
			scan = reflect.TypeFor[any]()
		}
		length, lengthOK := col.Length()
		precision, scale, decimalOK := col.DecimalSize()
		nullable, nullableOK := col.Nullable()
		if col.Name() != csColumns[i] || col.DatabaseTypeName() != "NAME" || col.ScanType() != scan || length != 0 || lengthOK || precision != 0 || scale != 0 || decimalOK || nullable != !pg || nullableOK != !pg {
			t.Fatalf("column %d name=%q type=%q scan=%v length=%d/%t decimal=%d/%d/%t nullable=%t/%t", i, col.Name(), col.DatabaseTypeName(), col.ScanType(), length, lengthOK, precision, scale, decimalOK, nullable, nullableOK)
		}
	}
}

func (c *csCase) projection(q string, want [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, csColumns, csTypes, want, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		csMetadata(t, columns, c.b.PG)
	}
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if want[row][column] == nil {
			if value != nil {
				t.Fatalf("raw NULL=%T(%v)", value, value)
			}
			return true
		}
		text, ok := value.(string)
		if c.b.PG {
			bytes, bytesOK := value.([]byte)
			text, ok = string(bytes), bytesOK
		}
		if !ok || text != want[row][column] {
			t.Fatalf("raw NAME[%d,%d]=%T(%v) want %v", row, column, value, value, want[row][column])
		}
		return true
	}
	return s
}

func (c *csCase) check(held fixture.HeldStatement, schema, name string) {
	c.t.Helper()
	s := c.projection(csQuery, c.rows(schema, name))
	c.run(c.conn, s)
	c.run(c.conn, fixture.Prepared(s))
	c.run(held, s)
}

func (c *csCase) denied() {
	c.t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic("SELECT * FROM pg_catalog.pg_user_mapping", fixture.ControlsReferenceDiagnostic("42501", "permission denied for table pg_user_mapping"))
		s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
		c.run(c.conn, s)
	}
}
