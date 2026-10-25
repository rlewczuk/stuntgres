package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

var dcColumns = []string{"constraint_catalog", "constraint_schema", "constraint_name", "domain_catalog", "domain_schema", "domain_name", "is_deferrable", "initially_deferred"}
var dcTypes = []string{"NAME", "NAME", "NAME", "NAME", "NAME", "NAME", "VARCHAR", "VARCHAR"}
var dcRoles = []string{"dc_owner", "dc_reader", "dc_owner_member", "dc_usage_member", "dc_usage_group", "dc_noinherit", "dc_set"}

func dcScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *fixture.Recorder, string)) {
	t.Helper()
	for _, schema := range []string{"dc_scope", "dc_moved", "dc_private"} {
		fixture.SchemaReferenceCleanup(t, schema)
	}
	fixture.RoleReferenceCleanup(t, dcRoles)
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, c, r := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			t.Cleanup(func() {
				defer c.Close()
				for _, q := range []string{"ROLLBACK", "RESET ROLE", "RESET SESSION AUTHORIZATION", "DROP SCHEMA IF EXISTS dc_scope CASCADE", "DROP SCHEMA IF EXISTS dc_moved CASCADE", "DROP SCHEMA IF EXISTS dc_private CASCADE"} {
					if _, err := c.ExecContext(p.Ctx, q); err != nil {
						t.Error(err)
					}
				}
			})
			var database string
			if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil || database == "" {
				t.Fatalf("database=%q error=%v", database, err)
			}
			body(t, p, b, c, r, database)
		})
	}
}

func dcMetadata(t *testing.T, columns []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(columns) != 8 {
		t.Fatalf("columns=%d want 8", len(columns))
	}
	for i, c := range columns {
		length, lengthOK := c.Length()
		precision, scale, decimalOK := c.DecimalSize()
		nullable, nullableOK := c.Nullable()
		scan := reflect.TypeFor[string]()
		if pg && i < 6 {
			scan = reflect.TypeFor[any]()
		}
		wantLength := int64(0)
		if i >= 6 {
			wantLength = 3
		}
		if c.Name() != dcColumns[i] || c.DatabaseTypeName() != dcTypes[i] || c.ScanType() != scan || length != wantLength || lengthOK != (i >= 6) || precision != 0 || scale != 0 || decimalOK || nullable != !pg || nullableOK != !pg {
			t.Fatalf("column %d name=%s type=%s scan=%v length=%d/%t decimal=%d/%d/%t nullable=%t/%t", i, c.Name(), c.DatabaseTypeName(), c.ScanType(), length, lengthOK, precision, scale, decimalOK, nullable, nullableOK)
		}
	}
}

func dcProjection(pg bool, where string, want [][]any) fixture.Step {
	s := fixture.Select("SELECT "+strings.Join(dcColumns, ",")+" FROM information_schema.domain_constraints WHERE "+where, dcColumns, dcTypes, want)
	s.Bag = true
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		dcMetadata(t, columns, pg)
	}
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		text, ok := value.(string)
		if pg && column < 6 {
			bytes, bytesOK := value.([]byte)
			text, ok = string(bytes), bytesOK
		}
		if !ok {
			t.Fatalf("raw column %d=%T(%v)", column, value, value)
		}
		for _, candidate := range want {
			if candidate[column] == text {
				return true
			}
		}
		t.Fatalf("unexpected raw column %d=%q", column, text)
		return true
	}
	return s
}

func dcRow(database, schema, constraint, domain string) []any {
	return []any{database, schema, constraint, database, schema, domain, "NO", "NO"}
}

func dcSeed(run func(fixture.Step)) {
	for _, q := range []string{
		"CREATE SCHEMA dc_scope",
		"CREATE DOMAIN dc_scope.a AS integer CONSTRAINT same CHECK(VALUE>0)",
		"CREATE DOMAIN dc_scope.b AS integer CONSTRAINT same CHECK(VALUE<10)",
		"CREATE DOMAIN dc_scope.child AS dc_scope.a",
		"CREATE DOMAIN dc_scope.plain AS integer",
		"CREATE TABLE dc_scope.t(v integer CONSTRAINT same CHECK(v>0))",
		"CREATE DOMAIN dc_scope.nn AS integer CONSTRAINT named_nn NOT NULL",
		"CREATE DOMAIN dc_scope.nn_child AS dc_scope.nn",
	} {
		run(fixture.Exec(q, 0))
	}
}

func dcSeedRows(database string) [][]any {
	return [][]any{dcRow(database, "dc_scope", "same", "a"), dcRow(database, "dc_scope", "same", "b"), dcRow(database, "dc_scope", "named_nn", "nn")}
}

func dcHold(t *testing.T, p *fixture.Pair, c *sql.Conn, query string) fixture.HeldStatement {
	t.Helper()
	stmt, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stmt.Close() })
	return fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
}

func dcCheck(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, held fixture.HeldStatement, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, r, s)
	fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(s))
	fixture.RunStep(t, p.Ctx, held, r, s)
}

func dcFlags(domain, name string, valid bool) fixture.Step {
	return fixture.Select("SELECT convalidated FROM pg_catalog.pg_constraint WHERE contypid='dc_scope."+domain+"'::regtype::oid AND conname='"+name+"'", []string{"convalidated"}, []string{"BOOL"}, [][]any{{valid}})
}

func dcMissing(run func(fixture.Step)) {
	d := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
	d.Position = "8"
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic("SELECT missing FROM information_schema.domain_constraints", d)
		s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
		if prepared {
			s.ErrorStage = "prepare"
		}
		run(s)
	}
}
