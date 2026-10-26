package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

var ccruColumns = []string{"constraint_catalog", "constraint_schema", "constraint_name", "specific_catalog", "specific_schema", "specific_name"}
var ccruTypes = []string{"NAME", "NAME", "NAME", "NAME", "NAME", "NAME"}

const ccruQuery = "SELECT * FROM information_schema.check_constraint_routine_usage WHERE constraint_name LIKE 'ccru_%'"

func ccruScenario(t *testing.T, body func(*ccruCase)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"ccru_o", "ccru_r", "ccru_i", "ccru_s", "ccru_t", "ccru_power"})
	for _, schema := range []string{"ccru_private", "ccru_moved"} {
		fixture.SchemaReferenceCleanup(t, schema)
	}
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				defer conn.Close()
				for _, q := range []string{"ROLLBACK", "RESET ROLE", "RESET SESSION AUTHORIZATION"} {
					if _, err := conn.ExecContext(p.Ctx, q); err != nil {
						t.Error(err)
					}
				}
			})
			c := &ccruCase{t: t, p: p, b: b, conn: conn}
			if err := conn.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&c.database); err != nil {
				t.Fatal(err)
			}
			body(c)
		})
	}
}

type ccruCase struct {
	t        *testing.T
	p        *fixture.Pair
	b        fixture.Backend
	conn     *sql.Conn
	database string
}

func (c *ccruCase) run(s fixture.Step) {
	c.t.Helper()
	fixture.RunStep(c.t, c.p.Ctx, c.conn, c.b.Recorder, s)
}
func (c *ccruCase) exec(queries ...string) {
	c.t.Helper()
	for _, q := range queries {
		c.run(fixture.Exec(q, 0))
	}
}
func (c *ccruCase) identity(schema, name, inputs string) string {
	c.t.Helper()
	return rsuIdentity(c.t, c.p, c.conn, schema, name, inputs)
}
func (c *ccruCase) row(schema, constraint, routineSchema, specific string) []any {
	return []any{c.database, schema, constraint, c.database, routineSchema, specific}
}
func (c *ccruCase) step(query string, want [][]any, args ...any) fixture.Step {
	s := fixture.Select(query, ccruColumns, ccruTypes, want, args...)
	s.Bag = true
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { ccruMetadata(t, rows, c.b.PG) }
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if value == nil {
			return false
		}
		if c.b.PG {
			if _, ok := value.([]byte); !ok {
				t.Fatalf("raw NAME=%T", value)
			}
		} else if _, ok := value.(string); !ok {
			t.Fatalf("raw NAME=%T", value)
		}
		return false
	}
	return s
}
func (c *ccruCase) read(want ...[]any) {
	c.t.Helper()
	s := c.step(ccruQuery, want)
	c.run(s)
	c.run(fixture.Prepared(s))
}
func (c *ccruCase) hold(query string) func(fixture.Step) {
	return rsuHeld(c.t, c.p, c.conn, c.b.Recorder, query)
}
func ccruMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	ccruColumnMetadata(t, columns, pg)
}

func ccruColumnMetadata(t *testing.T, columns []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(columns) != 6 {
		t.Fatalf("column count=%d", len(columns))
	}
	for i, column := range columns {
		length, lengthOK := column.Length()
		precision, scale, decimalOK := column.DecimalSize()
		nullable, nullableOK := column.Nullable()
		scan := reflect.TypeFor[string]()
		if pg {
			scan = reflect.TypeFor[any]()
		}
		if column.Name() != ccruColumns[i] || column.DatabaseTypeName() != "NAME" || column.ScanType() != scan || length != 0 || lengthOK || precision != 0 || scale != 0 || decimalOK || nullable != !pg || nullableOK != !pg {
			t.Fatalf("column %d: name=%s type=%s scan=%v length=%d/%t decimal=%d/%d/%t nullable=%t/%t", i, column.Name(), column.DatabaseTypeName(), column.ScanType(), length, lengthOK, precision, scale, decimalOK, nullable, nullableOK)
		}
	}
}
func (c *ccruCase) seed() (string, string) {
	c.exec("CREATE FUNCTION ccru_f(integer) RETURNS boolean RETURN $1 > 0", "CREATE FUNCTION ccru_g(integer) RETURNS boolean RETURN $1 < 100")
	return c.identity("public", "ccru_f", "integer"), c.identity("public", "ccru_g", "integer")
}
func (c *ccruCase) missingColumn() {
	for _, prepared := range []bool{false, true} {
		s := fixture.ImplicitError("SELECT missing FROM information_schema.check_constraint_routine_usage", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "8"})
		s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
		if prepared {
			s.ErrorStage = "prepare"
		}
		c.run(s)
	}
}
