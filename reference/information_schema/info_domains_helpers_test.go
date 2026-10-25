package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

var idmColumns = []string{"domain_catalog", "domain_schema", "domain_name", "data_type", "character_maximum_length", "character_octet_length", "character_set_catalog", "character_set_schema", "character_set_name", "collation_catalog", "collation_schema", "collation_name", "numeric_precision", "numeric_precision_radix", "numeric_scale", "datetime_precision", "interval_type", "interval_precision", "domain_default", "udt_catalog", "udt_schema", "udt_name", "scope_catalog", "scope_schema", "scope_name", "maximum_cardinality", "dtd_identifier"}
var idmTypes = []string{"NAME", "NAME", "NAME", "VARCHAR", "INT4", "INT4", "NAME", "NAME", "NAME", "NAME", "NAME", "NAME", "INT4", "INT4", "INT4", "INT4", "VARCHAR", "INT4", "VARCHAR", "NAME", "NAME", "NAME", "NAME", "NAME", "NAME", "INT4", "NAME"}
var idmRoles = []string{"idm_owner", "idm_reader", "idm_member", "idm_noinherit", "idm_set", "idm_group", "idm_usage_member"}

type idmCase struct {
	t        *testing.T
	p        *fixture.Pair
	b        fixture.Backend
	c        *sql.Conn
	r        *fixture.Recorder
	database string
}

func idmScenario(t *testing.T, body func(*idmCase)) {
	t.Helper()
	for _, schema := range []string{"idm_scope", "idm_moved", "idm_private"} {
		fixture.SchemaReferenceCleanup(t, schema)
	}
	fixture.RoleReferenceCleanup(t, idmRoles)
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, conn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			t.Cleanup(func() {
				defer conn.Close()
				for _, q := range []string{"ROLLBACK", "RESET ROLE", "RESET SESSION AUTHORIZATION", "DROP SCHEMA IF EXISTS idm_scope CASCADE", "DROP SCHEMA IF EXISTS idm_moved CASCADE", "DROP SCHEMA IF EXISTS idm_private CASCADE"} {
					if _, err := conn.ExecContext(p.Ctx, q); err != nil {
						t.Error(err)
					}
				}
			})
			var database string
			if err := conn.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil || database == "" {
				t.Fatalf("database=%q error=%v", database, err)
			}
			body(&idmCase{t, p, b, conn, recorder, database})
		})
	}
}

func (c *idmCase) run(s fixture.Step) {
	c.t.Helper()
	fixture.RunStep(c.t, c.p.Ctx, c.c, c.r, s)
}
func (c *idmCase) exec(queries ...string) {
	c.t.Helper()
	for _, q := range queries {
		c.run(fixture.Exec(q, 0))
	}
}
func (c *idmCase) hold(where string) fixture.HeldStatement {
	c.t.Helper()
	return dcHold(c.t, c.p, c.c, idmProjection(c.b.PG, where, nil).Query)
}
func (c *idmCase) check(where string, want ...[]any) {
	c.t.Helper()
	s := idmProjection(c.b.PG, where, want)
	c.run(s)
	c.run(fixture.Prepared(s))
}
func (c *idmCase) held(h fixture.HeldStatement, where string, args []any, want ...[]any) {
	c.t.Helper()
	s := idmProjection(c.b.PG, where, want)
	s.Args = args
	dcCheck(c.t, c.p, c.c, c.r, h, s)
}

// Each row starts from the released always-NULL fields; scenarios specify every
// non-NULL helper/default/collation cell independently of the queried backend.
func idmRow(database, schema, name, dataType, baseSchema, baseName string, cells map[int]any) []any {
	r := make([]any, 27)
	r[0], r[1], r[2], r[3] = database, schema, name, dataType
	r[19], r[20], r[21], r[26] = database, baseSchema, baseName, "1"
	for i, v := range cells {
		r[i] = v
	}
	return r
}
func idmInteger(database, schema, name string, defaultText any) []any {
	return idmRow(database, schema, name, "integer", "pg_catalog", "int4", map[int]any{12: int64(32), 13: int64(2), 14: int64(0), 18: defaultText})
}
func idmVarchar(database, schema, name, defaultText string) []any {
	return idmRow(database, schema, name, "character varying", "pg_catalog", "varchar", map[int]any{4: int64(5), 5: int64(20), 9: database, 10: "pg_catalog", 11: "C", 18: defaultText})
}
func idmProjection(pg bool, where string, want [][]any) fixture.Step {
	s := fixture.Select("SELECT "+strings.Join(idmColumns, ",")+" FROM information_schema.domains WHERE "+where, idmColumns, idmTypes, want)
	s.Bag = true
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		idmMetadata(t, types, pg)
	}
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if value == nil {
			return true
		}
		var ok bool
		switch idmTypes[column] {
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
func idmMetadata(t *testing.T, types []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(types) != 27 {
		t.Fatalf("columns=%d want 27", len(types))
	}
	for i, typ := range types {
		scan := reflect.TypeFor[string]()
		var length int64
		lengthOK := false
		switch idmTypes[i] {
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
		n, nk := typ.Nullable()
		l, lk := typ.Length()
		p, s, dk := typ.DecimalSize()
		if typ.Name() != idmColumns[i] || typ.DatabaseTypeName() != idmTypes[i] || typ.ScanType() != scan || n != !pg || nk != !pg || l != length || lk != lengthOK || p != 0 || s != 0 || dk {
			t.Fatalf("column %d name=%s type=%s scan=%v nullable=%t/%t length=%d/%t decimal=%d/%d/%t; want scan=%v length=%d/%t", i, typ.Name(), typ.DatabaseTypeName(), typ.ScanType(), n, nk, l, lk, p, s, dk, scan, length, lengthOK)
		}
	}
}
