package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

var iccColumns = []string{"constraint_catalog", "constraint_schema", "constraint_name", "check_clause"}
var iccTypes = []string{"NAME", "NAME", "NAME", "VARCHAR"}

const iccQuery = "SELECT * FROM information_schema.check_constraints WHERE constraint_name LIKE 'icc_%'"

func iccScenario(t *testing.T, body func(*iccCase)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"icc_owner", "icc_reader", "icc_inherit", "icc_set", "icc_routine_owner", "icc_power"})
	for _, schema := range []string{"icc_private", "icc_other", "icc_moved"} {
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
			c := &iccCase{t: t, p: p, b: b, conn: conn}
			if err := conn.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&c.database); err != nil {
				t.Fatal(err)
			}
			body(c)
		})
	}
}

type iccCase struct {
	t        *testing.T
	p        *fixture.Pair
	b        fixture.Backend
	conn     *sql.Conn
	database string
}

func (c *iccCase) run(s fixture.Step) {
	c.t.Helper()
	fixture.RunStep(c.t, c.p.Ctx, c.conn, c.b.Recorder, s)
}

func (c *iccCase) exec(queries ...string) {
	c.t.Helper()
	for _, q := range queries {
		c.run(fixture.Exec(q, 0))
	}
}

func (c *iccCase) row(schema, name, clause string) []any {
	return []any{c.database, schema, name, clause}
}

func (c *iccCase) step(query string, want [][]any, args ...any) fixture.Step {
	s := fixture.Select(query, iccColumns, iccTypes, want, args...)
	s.Bag = true
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		iccMetadata(t, columns, c.b.PG)
	}
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if value == nil {
			return false
		}
		if c.b.PG && column < 3 {
			if _, ok := value.([]byte); !ok {
				t.Fatalf("raw NAME column %d=%T", column, value)
			}
		} else if _, ok := value.(string); !ok {
			t.Fatalf("raw column %d=%T", column, value)
		}
		return false
	}
	return s
}

func (c *iccCase) read(want ...[]any) {
	c.t.Helper()
	s := c.step(iccQuery, want)
	c.run(s)
	c.run(fixture.Prepared(s))
}

func (c *iccCase) hold(query string) func(fixture.Step) {
	return rsuHeld(c.t, c.p, c.conn, c.b.Recorder, query)
}

func (c *iccCase) consumer() func(...[]any) {
	held := c.hold(iccQuery)
	return func(want ...[]any) {
		c.t.Helper()
		c.read(want...)
		held(c.step(iccQuery, want))
	}
}

func iccMetadata(t *testing.T, columns []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(columns) != 4 {
		t.Fatalf("column count=%d want 4", len(columns))
	}
	for i, column := range columns {
		length, lengthOK := column.Length()
		precision, scale, decimalOK := column.DecimalSize()
		nullable, nullableOK := column.Nullable()
		scan := reflect.TypeFor[string]()
		wantLength, wantLengthOK := int64(0), false
		if pg && i < 3 {
			scan = reflect.TypeFor[any]()
		}
		if i == 3 {
			wantLength, wantLengthOK = math.MaxInt64, true
			if pg {
				wantLength = -5
			}
		}
		if column.Name() != iccColumns[i] || column.DatabaseTypeName() != iccTypes[i] || column.ScanType() != scan || length != wantLength || lengthOK != wantLengthOK || precision != 0 || scale != 0 || decimalOK || nullable != !pg || nullableOK != !pg {
			t.Fatalf("column %d: name=%s type=%s scan=%v length=%d/%t decimal=%d/%d/%t nullable=%t/%t", i, column.Name(), column.DatabaseTypeName(), column.ScanType(), length, lengthOK, precision, scale, decimalOK, nullable, nullableOK)
		}
	}
}

func (c *iccCase) flags(name string, validated bool) {
	c.t.Helper()
	s := fixture.Select("SELECT conname,convalidated,conenforced FROM pg_constraint WHERE conname=$1", []string{"conname", "convalidated", "conenforced"}, []string{"NAME", "BOOL", "BOOL"}, [][]any{{name, validated, true}}, name)
	c.run(s)
	c.run(fixture.Prepared(s))
}

func (c *iccCase) constraintOID(name string) int64 {
	c.t.Helper()
	var oid int64
	if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT oid::bigint FROM pg_constraint WHERE conname=$1", name).Scan(&oid); err != nil || oid <= 0 {
		c.t.Fatalf("constraint %s OID=%d error=%v", name, oid, err)
	}
	return oid
}

func (c *iccCase) missingColumn() {
	c.t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ImplicitError("SELECT missing FROM information_schema.check_constraints", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "8"})
		s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
		if prepared {
			s.ErrorStage = "prepare"
		}
		c.run(s)
	}
}
