package reference_test

import (
	"context"
	"database/sql"
	"math"
	"reflect"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

var etColumns = []string{
	"object_catalog", "object_schema", "object_name", "object_type", "collection_type_identifier", "data_type",
	"character_maximum_length", "character_octet_length", "character_set_catalog", "character_set_schema", "character_set_name",
	"collation_catalog", "collation_schema", "collation_name", "numeric_precision", "numeric_precision_radix", "numeric_scale",
	"datetime_precision", "interval_type", "interval_precision", "udt_catalog", "udt_schema", "udt_name",
	"scope_catalog", "scope_schema", "scope_name", "maximum_cardinality", "dtd_identifier",
}
var etDomains = []string{
	"sql_identifier", "sql_identifier", "sql_identifier", "character_data", "sql_identifier", "character_data",
	"cardinal_number", "cardinal_number", "sql_identifier", "sql_identifier", "sql_identifier",
	"sql_identifier", "sql_identifier", "sql_identifier", "cardinal_number", "cardinal_number", "cardinal_number",
	"cardinal_number", "character_data", "cardinal_number", "sql_identifier", "sql_identifier", "sql_identifier",
	"sql_identifier", "sql_identifier", "sql_identifier", "cardinal_number", "sql_identifier",
}
var etTypes = []string{
	"NAME", "NAME", "NAME", "VARCHAR", "NAME", "VARCHAR", "INT4", "INT4", "NAME", "NAME", "NAME",
	"NAME", "NAME", "NAME", "INT4", "INT4", "INT4", "INT4", "VARCHAR", "INT4", "NAME", "NAME", "NAME",
	"NAME", "NAME", "NAME", "INT4", "NAME",
}

func etScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, string)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"et_owner", "et_reader", "et_inherit", "et_noinherit", "et_set"})
	for _, schema := range []string{"et_private", "et_moved", "et_shadow"} {
		fixture.SchemaReferenceCleanup(t, schema)
	}
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := c.Close(); err != nil {
					t.Error(err)
				}
			})
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), fixture.ScenarioBudget)
				defer cancel()
				for _, q := range []string{"RESET ROLE", "RESET SESSION AUTHORIZATION", "RESET search_path", "RESET quote_all_identifiers", "DROP SERVER IF EXISTS et_server CASCADE", "DROP FOREIGN DATA WRAPPER IF EXISTS et_fdw CASCADE"} {
					if _, err := c.ExecContext(ctx, q); err != nil {
						t.Errorf("cleanup %s: %v", q, err)
					}
				}
			})
			var database string
			if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
				t.Fatal(err)
			}
			body(t, p, b, c, database)
		})
	}
}

func etMetadata(t *testing.T, types []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(types) != 28 {
		t.Fatalf("columns=%d want28", len(types))
	}
	for i, c := range types {
		scan := reflect.TypeFor[string]()
		var length int64
		lengthOK := false
		switch etTypes[i] {
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
		}
		n, nk := c.Nullable()
		l, lk := c.Length()
		precision, scale, dk := c.DecimalSize()
		if c.Name() != etColumns[i] || c.DatabaseTypeName() != etTypes[i] || c.ScanType() != scan || n != !pg || nk != !pg || l != length || lk != lengthOK || precision != 0 || scale != 0 || dk {
			t.Errorf("column%d name=%s type=%s scan=%v nullable=%t/%t length=%d/%t decimal=%d/%d/%t", i+1, c.Name(), c.DatabaseTypeName(), c.ScanType(), n, nk, l, lk, precision, scale, dk)
		}
	}
}

func etProjection(pg bool, where string, want [][]any) fixture.Step {
	s := fixture.Select("SELECT e.* FROM information_schema.element_types e WHERE "+where, etColumns, etTypes, want)
	s.Bag = true
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		etMetadata(t, types, pg)
	}
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if value == nil {
			return true
		}
		ok := false
		switch etTypes[column] {
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

// REL_18_6 specifies every unassigned cell as SQL NULL, including all modifiers
// and cardinality. These expectations never read the provider being tested.
func etRow(database, schema, name, kind string, ordinal int, dataType, udtSchema, udtName string) []any {
	row := make([]any, 28)
	id := strconv.Itoa(ordinal)
	for i, value := range map[int]any{0: database, 1: schema, 2: name, 3: kind, 4: id, 5: dataType, 20: database, 21: udtSchema, 22: udtName, 27: "a" + id} {
		row[i] = value
	}
	return row
}

func etCollated(row []any, database, collation string) []any {
	row[11], row[12], row[13] = database, "pg_catalog", collation
	return row
}

func etExec(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, queries ...string) {
	t.Helper()
	for _, q := range queries {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
	}
}

func etCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	irCheck(t, p, b, c, s)
}

func etHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, query string) func(fixture.Step) {
	t.Helper()
	return rsuHeld(t, p, c, b.Recorder, query)
}

func etConsume(t *testing.T, rows *sql.Rows, types []*sql.ColumnType, pg bool, want [][]any) {
	t.Helper()
	var got [][]any
	for rows.Next() {
		values, dest := make([]any, 28), make([]any, 28)
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		for i := range values {
			values[i] = fixture.Normalize(values[i])
		}
		got = append(got, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	etMetadata(t, types, pg)
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	etMetadata(t, types, pg)
	// Retention callers explicitly order their queries; ordinary projections are bags.
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("retained rows=%v want=%v", got, want)
	}
}
