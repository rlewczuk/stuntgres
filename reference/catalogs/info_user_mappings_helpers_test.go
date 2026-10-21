package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

var iumColumns = []string{"authorization_identifier", "foreign_server_catalog", "foreign_server_name"}
var iumTypes = []string{"NAME", "NAME", "NAME"}
var iumRoles = []string{"ium_owner", "ium_member", "ium_reader", "ium_usage_member", "ium_set", "ium_mapped", "ium_other", "IUM Mixed Role", "IUM Renamed Role"}

func iumScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *fixture.Recorder, *[]fixture.Diagnostic, string)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, iumRoles)
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			t.Cleanup(func() {
				defer c.Close()
				// Cluster-wide SQL/MED objects and roles survive public-schema reset.
				for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "DROP FOREIGN DATA WRAPPER ium_fdw CASCADE"} {
					if _, err := c.ExecContext(p.Ctx, q); err != nil {
						t.Error(err)
					}
				}
			})
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, recorder, s) }
			run(fixture.Exec("CREATE FOREIGN DATA WRAPPER ium_fdw", 0))
			for _, role := range iumRoles {
				if role != "IUM Renamed Role" {
					run(fixture.Exec("CREATE ROLE \""+role+"\"", 0))
				}
			}
			var database string
			if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil || database == "" {
				t.Fatalf("independent database identity=%q error=%v", database, err)
			}
			body(t, p, b, c, recorder, &notices, database)
		})
	}
}

func iumRows(database, server string, roles ...string) [][]any {
	var rows [][]any
	for _, role := range roles {
		rows = append(rows, []any{role, database, server})
	}
	return rows
}

func iumMetadata(t *testing.T, columns []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(columns) != 3 {
		t.Fatalf("column count=%d want 3", len(columns))
	}
	for i, c := range columns {
		length, lengthOK := c.Length()
		precision, scale, decimalOK := c.DecimalSize()
		nullable, nullableOK := c.Nullable()
		scan := reflect.TypeFor[string]()
		if pg {
			scan = reflect.TypeFor[any]()
		}
		if c.Name() != iumColumns[i] || c.DatabaseTypeName() != "NAME" || c.ScanType() != scan || length != 0 || lengthOK || precision != 0 || scale != 0 || decimalOK || nullable != !pg || nullableOK != !pg {
			t.Fatalf("column %d name=%s type=%s scan=%v length=%d/%t decimal=%d/%d/%t nullable=%t/%t", i, c.Name(), c.DatabaseTypeName(), c.ScanType(), length, lengthOK, precision, scale, decimalOK, nullable, nullableOK)
		}
	}
}

func iumProjection(pg bool, where string, want [][]any) fixture.Step {
	s := fixture.Select("SELECT * FROM information_schema.user_mappings WHERE "+where, iumColumns, iumTypes, want)
	s.Bag = true
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		iumMetadata(t, columns, pg)
	}
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		// Bag rows have no portable order. Check raw text against the literal
		// candidate bag; RunStep separately asserts exact multiplicities.
		text, ok := value.(string)
		if pg {
			bytes, bytesOK := value.([]byte)
			text, ok = string(bytes), bytesOK
		}
		if !ok {
			t.Fatalf("raw NAME=%T(%v)", value, value)
		}
		for _, candidate := range want {
			if candidate[column] == text {
				return true
			}
		}
		t.Fatalf("raw NAME column %d unexpected value %q", column, text)
		return true
	}
	return s
}

func iumHeld(t *testing.T, p *fixture.Pair, c *sql.Conn, recorder *fixture.Recorder, query string) func(fixture.Step) {
	t.Helper()
	stmt, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stmt.Close() })
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
	return func(s fixture.Step) { fixture.RunStep(t, p.Ctx, held, recorder, s) }
}

func iumCascadeNotice(t *testing.T, pg bool, got []fixture.Diagnostic, server, role string) {
	t.Helper()
	want := []fixture.Diagnostic{}
	if pg {
		want = append(want, fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to user mapping for " + role + " on server " + server})
	}
	fixture.RegtypeAssertNotices(t, got, want)
}
