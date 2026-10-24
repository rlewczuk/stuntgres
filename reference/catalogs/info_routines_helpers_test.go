package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

var irColumns = []string{
	"specific_catalog",
	"specific_schema",
	"specific_name",
	"routine_catalog",
	"routine_schema",
	"routine_name",
	"routine_type",
	"module_catalog",
	"module_schema",
	"module_name",
	"udt_catalog",
	"udt_schema",
	"udt_name",
	"data_type",
	"character_maximum_length",
	"character_octet_length",
	"character_set_catalog",
	"character_set_schema",
	"character_set_name",
	"collation_catalog",
	"collation_schema",
	"collation_name",
	"numeric_precision",
	"numeric_precision_radix",
	"numeric_scale",
	"datetime_precision",
	"interval_type",
	"interval_precision",
	"type_udt_catalog",
	"type_udt_schema",
	"type_udt_name",
	"scope_catalog",
	"scope_schema",
	"scope_name",
	"maximum_cardinality",
	"dtd_identifier",
	"routine_body",
	"routine_definition",
	"external_name",
	"external_language",
	"parameter_style",
	"is_deterministic",
	"sql_data_access",
	"is_null_call",
	"sql_path",
	"schema_level_routine",
	"max_dynamic_result_sets",
	"is_user_defined_cast",
	"is_implicitly_invocable",
	"security_type",
	"to_sql_specific_catalog",
	"to_sql_specific_schema",
	"to_sql_specific_name",
	"as_locator",
	"created",
	"last_altered",
	"new_savepoint_level",
	"is_udt_dependent",
	"result_cast_from_data_type",
	"result_cast_as_locator",
	"result_cast_char_max_length",
	"result_cast_char_octet_length",
	"result_cast_char_set_catalog",
	"result_cast_char_set_schema",
	"result_cast_char_set_name",
	"result_cast_collation_catalog",
	"result_cast_collation_schema",
	"result_cast_collation_name",
	"result_cast_numeric_precision",
	"result_cast_numeric_precision_radix",
	"result_cast_numeric_scale",
	"result_cast_datetime_precision",
	"result_cast_interval_type",
	"result_cast_interval_precision",
	"result_cast_type_udt_catalog",
	"result_cast_type_udt_schema",
	"result_cast_type_udt_name",
	"result_cast_scope_catalog",
	"result_cast_scope_schema",
	"result_cast_scope_name",
	"result_cast_maximum_cardinality",
	"result_cast_dtd_identifier",
}
var irDomains = []string{
	"sql_identifier",
	"sql_identifier",
	"sql_identifier",
	"sql_identifier",
	"sql_identifier",
	"sql_identifier",
	"character_data",
	"sql_identifier",
	"sql_identifier",
	"sql_identifier",
	"sql_identifier",
	"sql_identifier",
	"sql_identifier",
	"character_data",
	"cardinal_number",
	"cardinal_number",
	"sql_identifier",
	"sql_identifier",
	"sql_identifier",
	"sql_identifier",
	"sql_identifier",
	"sql_identifier",
	"cardinal_number",
	"cardinal_number",
	"cardinal_number",
	"cardinal_number",
	"character_data",
	"cardinal_number",
	"sql_identifier",
	"sql_identifier",
	"sql_identifier",
	"sql_identifier",
	"sql_identifier",
	"sql_identifier",
	"cardinal_number",
	"sql_identifier",
	"character_data",
	"character_data",
	"character_data",
	"character_data",
	"character_data",
	"yes_or_no",
	"character_data",
	"yes_or_no",
	"character_data",
	"yes_or_no",
	"cardinal_number",
	"yes_or_no",
	"yes_or_no",
	"character_data",
	"sql_identifier",
	"sql_identifier",
	"sql_identifier",
	"yes_or_no",
	"time_stamp",
	"time_stamp",
	"yes_or_no",
	"yes_or_no",
	"character_data",
	"yes_or_no",
	"cardinal_number",
	"cardinal_number",
	"sql_identifier",
	"sql_identifier",
	"sql_identifier",
	"sql_identifier",
	"sql_identifier",
	"sql_identifier",
	"cardinal_number",
	"cardinal_number",
	"cardinal_number",
	"cardinal_number",
	"character_data",
	"cardinal_number",
	"sql_identifier",
	"sql_identifier",
	"sql_identifier",
	"sql_identifier",
	"sql_identifier",
	"sql_identifier",
	"cardinal_number",
	"sql_identifier",
}

var irTypes = func() []string {
	out := make([]string, len(irDomains))
	for i, d := range irDomains {
		out[i] = map[string]string{"sql_identifier": "NAME", "character_data": "VARCHAR", "cardinal_number": "INT4", "yes_or_no": "VARCHAR", "time_stamp": "TIMESTAMPTZ"}[d]
	}
	return out
}()

func irScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, string)) {
	t.Helper()
	// Cleanup is LIFO: drop private/moved objects and their ACL dependencies
	// before cluster roles, after the scenario has returned all handles.
	fixture.RoleReferenceCleanup(t, []string{"ir_owner", "ir_reader", "ir_inherit", "ir_noinherit", "ir_set"})
	fixture.SchemaReferenceCleanup(t, "ir_private")
	fixture.SchemaReferenceCleanup(t, "ir_moved")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = c.Close() })
			defer func() { _, _ = c.ExecContext(p.Ctx, "RESET ROLE") }()
			var db string
			if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&db); err != nil {
				t.Fatal(err)
			}
			body(t, p, b, c, db)
		})
	}
}

// All 82 pq descriptors were observed on the pinned PostgreSQL 18.6 oracle:
// NAME interface{}, VARCHAR string (-5 unlimited, 3 yes/no), INT4 int32,
// TIMESTAMPTZ time.Time; unknown nullability and decimal size throughout.
func irMetadata(t *testing.T, types []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(types) != 82 {
		t.Fatalf("columns=%d want82", len(types))
	}
	for i, c := range types {
		scan := reflect.TypeFor[string]()
		var length int64
		lengthOK := false
		switch irTypes[i] {
		case "NAME":
			if pg {
				scan = reflect.TypeFor[any]()
			}
		case "VARCHAR":
			length, lengthOK = math.MaxInt64, true
			if pg {
				length = -5
			}
			if irDomains[i] == "yes_or_no" {
				length = 3
			}
		case "INT4":
			scan = reflect.TypeFor[int64]()
			if pg {
				scan = reflect.TypeFor[int32]()
			}
		case "TIMESTAMPTZ":
			scan = reflect.TypeFor[time.Time]()
		}
		n, nk := c.Nullable()
		l, lk := c.Length()
		precision, scale, dk := c.DecimalSize()
		if c.Name() != irColumns[i] || c.DatabaseTypeName() != irTypes[i] || c.ScanType() != scan || n != !pg || nk != !pg || l != length || lk != lengthOK || precision != 0 || scale != 0 || dk {
			t.Errorf("column%d name=%s type=%s scan=%v nullable=%t/%t length=%d/%t decimal=%d/%d/%t; want name=%s type=%s scan=%v length=%d/%t", i+1, c.Name(), c.DatabaseTypeName(), c.ScanType(), n, nk, l, lk, precision, scale, dk, irColumns[i], irTypes[i], scan, length, lengthOK)
		}
	}
}

func irProjection(pg bool, where string, want [][]any) fixture.Step {
	s := fixture.Select("SELECT * FROM information_schema.routines WHERE "+where, irColumns, irTypes, want)
	s.Bag = true
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		irMetadata(t, types, pg)
	}
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if value == nil {
			return true
		}
		ok := false
		switch irTypes[column] {
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
			t.Fatalf("raw column%d row%d: %T(%v)", column+1, row, value, value)
		}
		return true
	}
	return s
}

// All unlisted cells are explicitly NULL under REL_18_6 information_schema.sql.
func irRow(db, schema, name, id, kind, dataType, udtSchema, udtName, language string, source any, external any, immutable, strict bool) []any {
	row := make([]any, 82)
	for i, v := range map[int]any{1: db, 2: schema, 3: id, 4: db, 5: schema, 6: name, 7: kind, 14: dataType, 29: db, 30: udtSchema, 31: udtName, 36: "0", 37: "EXTERNAL", 38: source, 39: external, 40: language, 41: "GENERAL", 42: "NO", 43: "MODIFIES", 44: "NO", 46: "YES", 47: int64(0), 50: "INVOKER", 54: "NO", 58: "NO"} {
		row[i-1] = v
	}
	if language == "SQL" {
		row[36] = "SQL"
	}
	if immutable {
		row[41] = "YES"
	}
	if strict {
		row[43] = "YES"
	}
	if kind == "PROCEDURE" {
		for _, i := range []int{14, 29, 30, 31, 36, 44} {
			row[i-1] = nil
		}
	}
	if kind == "" {
		row[6] = nil
	}
	return row
}

func irCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
	statement, err := c.PrepareContext(p.Ctx, s.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer statement.Close()
	held := fixture.HeldStatement{Executor: c, Statement: statement, SQL: s.Query}
	fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
}
