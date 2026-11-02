package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// The released view projection is specified independently of the native registry.
var attrColumns = []string{"udt_catalog", "udt_schema", "udt_name", "attribute_name", "ordinal_position", "attribute_default", "is_nullable", "data_type", "character_maximum_length", "character_octet_length", "character_set_catalog", "character_set_schema", "character_set_name", "collation_catalog", "collation_schema", "collation_name", "numeric_precision", "numeric_precision_radix", "numeric_scale", "datetime_precision", "interval_type", "interval_precision", "attribute_udt_catalog", "attribute_udt_schema", "attribute_udt_name", "scope_catalog", "scope_schema", "scope_name", "maximum_cardinality", "dtd_identifier", "is_derived_reference_attribute"}
var attrTypes = []string{"NAME", "NAME", "NAME", "NAME", "INT4", "VARCHAR", "VARCHAR", "VARCHAR", "INT4", "INT4", "NAME", "NAME", "NAME", "NAME", "NAME", "NAME", "INT4", "INT4", "INT4", "INT4", "VARCHAR", "INT4", "NAME", "NAME", "NAME", "NAME", "NAME", "NAME", "INT4", "NAME", "VARCHAR"}
var attrRoles = []string{"attr_owner", "attr_reader", "attr_member", "attr_noinherit", "attr_set", "attr_group", "attr_usage_member"}

type attrCase struct {
	t        *testing.T
	p        *fixture.Pair
	b        fixture.Backend
	c        *sql.Conn
	r        *fixture.Recorder
	database string
}

func attrScenario(t *testing.T, body func(*attrCase)) {
	t.Helper()
	for _, schema := range []string{"attr_scope", "attr_private", "attr_moved"} {
		fixture.SchemaReferenceCleanup(t, schema)
	}
	fixture.RoleReferenceCleanup(t, attrRoles)
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, conn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			t.Cleanup(func() {
				defer conn.Close()
				for _, q := range []string{"ROLLBACK", "RESET ROLE", "RESET SESSION AUTHORIZATION", "DROP SCHEMA IF EXISTS attr_scope CASCADE", "DROP SCHEMA IF EXISTS attr_private CASCADE", "DROP SCHEMA IF EXISTS attr_moved CASCADE"} {
					if _, err := conn.ExecContext(p.Ctx, q); err != nil {
						t.Error(err)
					}
				}
			})
			var database string
			if err := conn.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil || database == "" {
				t.Fatalf("database=%q error=%v", database, err)
			}
			body(&attrCase{t, p, b, conn, recorder, database})
		})
	}
}

func (c *attrCase) run(s fixture.Step) {
	c.t.Helper()
	fixture.RunStep(c.t, c.p.Ctx, c.c, c.r, s)
}

func (c *attrCase) exec(queries ...string) {
	c.t.Helper()
	for _, q := range queries {
		c.run(fixture.Exec(q, 0))
	}
}

func (c *attrCase) check(where string, want ...[]any) {
	c.t.Helper()
	s := attrProjection(c.b.PG, where, want)
	c.run(s)
	c.run(fixture.Prepared(s))
}

func (c *attrCase) hold(where string) fixture.HeldStatement {
	c.t.Helper()
	return dcHold(c.t, c.p, c.c, attrProjection(c.b.PG, where, nil).Query)
}

func (c *attrCase) held(h fixture.HeldStatement, where string, args []any, want ...[]any) {
	c.t.Helper()
	s := attrProjection(c.b.PG, where, want)
	s.Args = args
	dcCheck(c.t, c.p, c.c, c.r, h, s)
}

// Every unspecified field is SQL NULL, including defaults and cardinalities.
func attrRow(database, schema, owner, member string, ordinal int64, dataType, typeSchema, typeName string) []any {
	r := make([]any, 31)
	r[0], r[1], r[2], r[3], r[4] = database, schema, owner, member, ordinal
	r[6], r[7] = "YES", dataType
	r[22], r[23], r[24] = database, typeSchema, typeName
	r[29], r[30] = strconv.FormatInt(ordinal, 10), "NO"
	return r
}

func attrPatch(row []any, values map[string]any) []any {
	for name, value := range values {
		found := false
		for i, column := range attrColumns {
			if column == name {
				row[i], found = value, true
				break
			}
		}
		if !found {
			panic("unknown expected attributes column: " + name)
		}
	}
	return row
}

func (c *attrCase) integer(owner, member string, ordinal int64) []any {
	return attrPatch(attrRow(c.database, "attr_scope", owner, member, ordinal, "integer", "pg_catalog", "int4"), map[string]any{"numeric_precision": int64(32), "numeric_precision_radix": int64(2), "numeric_scale": int64(0)})
}

func attrProjection(pg bool, where string, want [][]any) fixture.Step {
	s := fixture.Select("SELECT "+strings.Join(attrColumns, ",")+" FROM information_schema.attributes WHERE "+where+" ORDER BY udt_schema,udt_name,ordinal_position", attrColumns, attrTypes, want)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		attrMetadata(t, types, pg, attrColumns, attrTypes)
	}
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if value == nil {
			return true
		}
		var ok bool
		switch attrTypes[column] {
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

func attrMetadata(t *testing.T, types []*sql.ColumnType, pg bool, names, bases []string) {
	t.Helper()
	if len(types) != len(names) {
		t.Fatalf("columns=%d want %d", len(types), len(names))
	}
	for i, typ := range types {
		scan := reflect.TypeFor[string]()
		var length int64
		lengthOK := false
		switch bases[i] {
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
		if names[i] == "is_nullable" || names[i] == "is_derived_reference_attribute" {
			length, lengthOK = 3, true
		}
		n, nk := typ.Nullable()
		l, lk := typ.Length()
		p, s, dk := typ.DecimalSize()
		if typ.Name() != names[i] || typ.DatabaseTypeName() != bases[i] || typ.ScanType() != scan || n != !pg || nk != !pg || l != length || lk != lengthOK || p != 0 || s != 0 || dk {
			t.Fatalf("column %d name=%s type=%s scan=%v nullable=%t/%t length=%d/%t decimal=%d/%d/%t; want scan=%v length=%d/%t", i, typ.Name(), typ.DatabaseTypeName(), typ.ScanType(), n, nk, l, lk, p, s, dk, scan, length, lengthOK)
		}
	}
}
