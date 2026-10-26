package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

var udtColumns = []string{"user_defined_type_catalog", "user_defined_type_schema", "user_defined_type_name", "user_defined_type_category", "is_instantiable", "is_final", "ordering_form", "ordering_category", "ordering_routine_catalog", "ordering_routine_schema", "ordering_routine_name", "reference_type", "data_type", "character_maximum_length", "character_octet_length", "character_set_catalog", "character_set_schema", "character_set_name", "collation_catalog", "collation_schema", "collation_name", "numeric_precision", "numeric_precision_radix", "numeric_scale", "datetime_precision", "interval_type", "interval_precision", "source_dtd_identifier", "ref_dtd_identifier"}
var udtTypes = []string{"NAME", "NAME", "NAME", "VARCHAR", "VARCHAR", "VARCHAR", "VARCHAR", "VARCHAR", "NAME", "NAME", "NAME", "VARCHAR", "VARCHAR", "INT4", "INT4", "NAME", "NAME", "NAME", "NAME", "NAME", "NAME", "INT4", "INT4", "INT4", "INT4", "VARCHAR", "INT4", "NAME", "NAME"}
var udtRoles = []string{"udt_owner", "udt_reader", "udt_member", "udt_noinherit", "udt_set", "udt_group", "udt_usage_member"}

type udtCase struct {
	t        *testing.T
	p        *fixture.Pair
	b        fixture.Backend
	c        *sql.Conn
	r        *fixture.Recorder
	database string
}

func udtScenario(t *testing.T, body func(*udtCase)) {
	t.Helper()
	for _, schema := range []string{"udt_scope", "udt_moved", "udt_private"} {
		fixture.SchemaReferenceCleanup(t, schema)
	}
	fixture.RoleReferenceCleanup(t, udtRoles)
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, conn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			t.Cleanup(func() {
				defer conn.Close()
				for _, q := range []string{"ROLLBACK", "RESET ROLE", "RESET SESSION AUTHORIZATION", "DROP SCHEMA IF EXISTS udt_scope CASCADE", "DROP SCHEMA IF EXISTS udt_moved CASCADE", "DROP SCHEMA IF EXISTS udt_private CASCADE"} {
					if _, err := conn.ExecContext(p.Ctx, q); err != nil {
						t.Error(err)
					}
				}
			})
			var database string
			if err := conn.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil || database == "" {
				t.Fatalf("database=%q error=%v", database, err)
			}
			body(&udtCase{t, p, b, conn, recorder, database})
		})
	}
}

func (c *udtCase) run(s fixture.Step) {
	c.t.Helper()
	fixture.RunStep(c.t, c.p.Ctx, c.c, c.r, s)
}
func (c *udtCase) exec(queries ...string) {
	c.t.Helper()
	for _, q := range queries {
		c.run(fixture.Exec(q, 0))
	}
}
func (c *udtCase) hold(where string) fixture.HeldStatement {
	c.t.Helper()
	return dcHold(c.t, c.p, c.c, udtProjection(c.b.PG, where, nil).Query)
}
func (c *udtCase) check(where string, want ...[]any) {
	c.t.Helper()
	s := udtProjection(c.b.PG, where, want)
	c.run(s)
	c.run(fixture.Prepared(s))
}
func (c *udtCase) held(h fixture.HeldStatement, where string, args []any, want ...[]any) {
	c.t.Helper()
	s := udtProjection(c.b.PG, where, want)
	s.Args = args
	dcCheck(c.t, c.p, c.c, c.r, h, s)
}

// The released projection has five populated cells and twenty-four typed NULLs.
func udtRow(database, schema, name string) []any {
	r := make([]any, 29)
	r[0], r[1], r[2], r[3], r[4] = database, schema, name, "STRUCTURED", "YES"
	return r
}
func udtProjection(pg bool, where string, want [][]any) fixture.Step {
	s := fixture.Select("SELECT "+strings.Join(udtColumns, ",")+" FROM information_schema.user_defined_types WHERE "+where, udtColumns, udtTypes, want)
	s.Bag = true
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		udtMetadata(t, types, pg)
	}
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if value == nil {
			return true
		}
		var ok bool
		switch udtTypes[column] {
		case "INT4":
			_, ok = value.(int64)
		case "VARCHAR":
			_, ok = value.(string)
		case "NAME":
			if pg {
				_, ok = value.([]byte)
			} else {
				_, ok = value.(string)
			}
		}
		if !ok {
			t.Fatalf("raw column %d row %d: %T(%v)", column, row, value, value)
		}
		return true
	}
	return s
}
func udtMetadata(t *testing.T, types []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(types) != 29 {
		t.Fatalf("columns=%d want 29", len(types))
	}
	for i, typ := range types {
		scan := reflect.TypeFor[string]()
		var length int64
		lengthOK := false
		switch udtTypes[i] {
		case "NAME":
			if pg {
				scan = reflect.TypeFor[any]()
			}
		case "VARCHAR":
			length, lengthOK = math.MaxInt64, true
			if pg {
				length = -5
			}
		case "INT4":
			scan = reflect.TypeFor[int64]()
			if pg {
				scan = reflect.TypeFor[int32]()
			}
		}
		if i == 4 || i == 5 {
			length, lengthOK = 3, true
		}
		n, nk := typ.Nullable()
		l, lk := typ.Length()
		p, s, dk := typ.DecimalSize()
		if typ.Name() != udtColumns[i] || typ.DatabaseTypeName() != udtTypes[i] || typ.ScanType() != scan || n != !pg || nk != !pg || l != length || lk != lengthOK || p != 0 || s != 0 || dk {
			t.Fatalf("column %d name=%s type=%s scan=%v nullable=%t/%t length=%d/%t decimal=%d/%d/%t; want scan=%v length=%d/%t", i, typ.Name(), typ.DatabaseTypeName(), typ.ScanType(), n, nk, l, lk, p, s, dk, scan, length, lengthOK)
		}
	}
}
