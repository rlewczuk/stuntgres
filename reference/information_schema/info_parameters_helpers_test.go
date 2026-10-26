package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

var ipColumns = []string{"specific_catalog", "specific_schema", "specific_name", "ordinal_position", "parameter_mode", "is_result", "as_locator", "parameter_name", "data_type", "character_maximum_length", "character_octet_length", "character_set_catalog", "character_set_schema", "character_set_name", "collation_catalog", "collation_schema", "collation_name", "numeric_precision", "numeric_precision_radix", "numeric_scale", "datetime_precision", "interval_type", "interval_precision", "udt_catalog", "udt_schema", "udt_name", "scope_catalog", "scope_schema", "scope_name", "maximum_cardinality", "dtd_identifier", "parameter_default"}
var ipDomains = []string{"sql_identifier", "sql_identifier", "sql_identifier", "cardinal_number", "character_data", "yes_or_no", "yes_or_no", "sql_identifier", "character_data", "cardinal_number", "cardinal_number", "sql_identifier", "sql_identifier", "sql_identifier", "sql_identifier", "sql_identifier", "sql_identifier", "cardinal_number", "cardinal_number", "cardinal_number", "cardinal_number", "character_data", "cardinal_number", "sql_identifier", "sql_identifier", "sql_identifier", "sql_identifier", "sql_identifier", "sql_identifier", "cardinal_number", "sql_identifier", "character_data"}
var ipTypes = func() []string {
	out := make([]string, len(ipDomains))
	for i, d := range ipDomains {
		out[i] = map[string]string{"sql_identifier": "NAME", "cardinal_number": "INT4", "character_data": "VARCHAR", "yes_or_no": "VARCHAR"}[d]
	}
	return out
}()

func ipScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, string)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"ip_owner", "ip_reader", "ip_inherit", "ip_noinherit", "ip_set"})
	fixture.SchemaReferenceCleanup(t, "ip_private")
	fixture.SchemaReferenceCleanup(t, "ip_moved")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer func() {
				_, _ = c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
				_, _ = c.ExecContext(p.Ctx, "RESET ROLE")
			}()
			var db string
			if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&db); err != nil {
				t.Fatal(err)
			}
			body(t, p, b, c, db)
		})
	}
}

func ipMetadata(t *testing.T, types []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(types) != 32 {
		t.Fatalf("columns=%d want32", len(types))
	}
	for i, c := range types {
		scan := reflect.TypeFor[string]()
		var length int64
		lengthOK := false
		switch ipTypes[i] {
		case "NAME":
			if pg {
				scan = reflect.TypeFor[any]()
			}
		case "INT4":
			scan = reflect.TypeFor[int64]()
			if pg {
				scan = reflect.TypeFor[int32]()
			}
		case "VARCHAR":
			length, lengthOK = math.MaxInt64, true
			if pg {
				length = -5
			}
			if ipDomains[i] == "yes_or_no" {
				length = 3
			}
		}
		n, nk := c.Nullable()
		l, lk := c.Length()
		precision, scale, dk := c.DecimalSize()
		if c.Name() != ipColumns[i] || c.DatabaseTypeName() != ipTypes[i] || c.ScanType() != scan || n != !pg || nk != !pg || l != length || lk != lengthOK || precision != 0 || scale != 0 || dk {
			t.Errorf("column%d name=%s type=%s scan=%v nullable=%t/%t length=%d/%t decimal=%d/%d/%t", i+1, c.Name(), c.DatabaseTypeName(), c.ScanType(), n, nk, l, lk, precision, scale, dk)
		}
	}
}

func ipProjection(pg bool, where string, want [][]any) fixture.Step {
	s := fixture.Select("SELECT * FROM information_schema.parameters WHERE "+where, ipColumns, ipTypes, want)
	s.Bag = true
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		ipMetadata(t, types, pg)
	}
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if value == nil {
			return true
		}
		ok := false
		switch ipTypes[column] {
		case "NAME":
			if pg {
				_, ok = value.([]byte)
			} else {
				_, ok = value.(string)
			}
		case "VARCHAR":
			_, ok = value.(string)
		case "INT4":
			_, ok = value.(int64)
		}
		if !ok {
			t.Fatalf("raw column%d row%d %T(%v)", column+1, row, value, value)
		}
		return true
	}
	return s
}

// Released view constants and all always-NULL cells are specified independently.
func ipRow(db, schema, id string, ordinal int, name any, mode, dataType, udtSchema, udtName string, def any) []any {
	row := make([]any, 32)
	for i, v := range map[int]any{0: db, 1: schema, 2: id, 3: int64(ordinal), 4: mode, 5: "NO", 6: "NO", 7: name, 8: dataType, 23: db, 24: udtSchema, 25: udtName, 30: strconv.Itoa(ordinal), 31: def} {
		row[i] = v
	}
	return row
}
func ipMix(db, schema, id string, def any) [][]any {
	return [][]any{ipRow(db, schema, id, 1, "a", "IN", "integer", "pg_catalog", "int4", nil), ipRow(db, schema, id, 2, "b", "OUT", "text", "pg_catalog", "text", nil), ipRow(db, schema, id, 3, "c", "INOUT", "integer", "pg_catalog", "int4", def)}
}
func ipIdentityWhere(id string) string { return "specific_name='" + id + "'" }
func ipCreateMix(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) string {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE FUNCTION public.ip_mix(IN a integer, OUT b text, INOUT c integer DEFAULT 9) LANGUAGE SQL AS 'SELECT a::text,c'", 0))
	return rsuIdentity(t, p, c, "public", "ip_mix", "integer, integer")
}
