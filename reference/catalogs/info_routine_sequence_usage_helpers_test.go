package reference_test

import (
	"database/sql"
	"reflect"
	"strconv"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

var rsuColumns = []string{"specific_catalog", "specific_schema", "specific_name", "routine_catalog", "routine_schema", "routine_name", "sequence_catalog", "sequence_schema", "sequence_name"}
var rsuTypes = []string{"NAME", "NAME", "NAME", "NAME", "NAME", "NAME", "NAME", "NAME", "NAME"}
var rsuRoles = []string{"rsu_owner", "rsu_reader", "rsu_set", "rsu_noinherit", "rsu_middle", "rsu_leaf", "rsu_broken"}

func rsuScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *fixture.Recorder, *[]fixture.Diagnostic, string)) {
	t.Helper()
	fixture.SchemaReferenceCleanup(t, "rsu_private")
	fixture.SchemaReferenceCleanup(t, "rsu_moved")
	fixture.RoleReferenceCleanup(t, rsuRoles)
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			t.Cleanup(func() {
				defer c.Close()
				for _, q := range []string{"ROLLBACK", "RESET ROLE", "RESET SESSION AUTHORIZATION", "DROP FUNCTION IF EXISTS rsu_private.r()", "DROP SCHEMA IF EXISTS rsu_private CASCADE", "DROP SCHEMA IF EXISTS rsu_moved CASCADE"} {
					if _, err := c.ExecContext(p.Ctx, q); err != nil {
						t.Error(err)
					}
				}
			})
			var database string
			if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil || database == "" {
				t.Fatalf("database=%q error=%v", database, err)
			}
			body(t, p, b, c, recorder, &notices, database)
		})
	}
}

func rsuMetadata(t *testing.T, columns []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(columns) != len(rsuColumns) {
		t.Fatalf("column count=%d want %d", len(columns), len(rsuColumns))
	}
	for i, c := range columns {
		length, lengthOK := c.Length()
		precision, scale, decimalOK := c.DecimalSize()
		nullable, nullableOK := c.Nullable()
		scan := reflect.TypeFor[string]()
		if pg {
			scan = reflect.TypeFor[any]()
		}
		if c.Name() != rsuColumns[i] || c.DatabaseTypeName() != "NAME" || c.ScanType() != scan || length != 0 || lengthOK || precision != 0 || scale != 0 || decimalOK || nullable != !pg || nullableOK != !pg {
			t.Fatalf("column %d name=%s type=%s scan=%v length=%d/%t decimal=%d/%d/%t nullable=%t/%t", i, c.Name(), c.DatabaseTypeName(), c.ScanType(), length, lengthOK, precision, scale, decimalOK, nullable, nullableOK)
		}
	}
}

func rsuProjection(pg bool, where string, want [][]any) fixture.Step {
	s := fixture.Select("SELECT * FROM information_schema.routine_sequence_usage WHERE "+where, rsuColumns, rsuTypes, want)
	s.Bag = true
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		rsuMetadata(t, columns, pg)
	}
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		text, ok := value.(string)
		if pg {
			bytes, bytesOK := value.([]byte)
			text, ok = string(bytes), bytesOK
		}
		if !ok {
			t.Fatalf("raw NAME column %d=%T(%v)", column, value, value)
		}
		for _, candidate := range want {
			if candidate[column] == text {
				return true
			}
		}
		t.Fatalf("unexpected raw NAME column %d=%q", column, text)
		return true
	}
	return s
}

// Identity comes from pg_proc, never from the usage view or pg_depend.
func rsuIdentity(t *testing.T, p *fixture.Pair, c *sql.Conn, schema, name, inputs string) string {
	t.Helper()
	var oid int64
	// Resolve overloads through the supported callable-identity input path.
	identitySQL := `"` + schema + `"."` + name + `"(` + inputs + `)`
	q := "SELECT $1::regprocedure::oid"
	if err := c.QueryRowContext(p.Ctx, q, identitySQL).Scan(&oid); err != nil || oid <= 0 {
		t.Fatalf("routine %s.%s(%s) oid=%d error=%v", schema, name, inputs, oid, err)
	}
	suffix := "_" + strconv.FormatInt(oid, 10)
	prefix := []rune(name)
	for len(string(prefix))+len(suffix) > 63 {
		prefix = prefix[:len(prefix)-1]
	}
	identity := string(prefix) + suffix
	if !utf8.ValidString(identity) {
		t.Fatal("invalid independent specific identity", identity)
	}
	return identity
}

func rsuRow(database, schema, name, identity, sequenceSchema, sequence string) []any {
	return []any{database, schema, identity, database, schema, name, database, sequenceSchema, sequence}
}

func rsuHeld(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, query string) func(fixture.Step) {
	t.Helper()
	stmt, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stmt.Close() })
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
	return func(s fixture.Step) { fixture.RunStep(t, p.Ctx, held, r, s) }
}
