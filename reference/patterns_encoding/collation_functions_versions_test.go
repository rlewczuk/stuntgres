package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func collationFunctionsScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func collationFunctionsError(t *testing.T, p *fixture.Pair, b fixture.Backend, q, code, message string, args ...any) {
	t.Helper()
	s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message}, args...)
	s.SelectRows, s.ErrorStage = true, "execute"
	fixture.CollationRoutes(t, p, b, s)
}

func collationFunctionsHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, q string) fixture.HeldStatement {
	t.Helper()
	stmt, err := x.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stmt.Close() })
	return fixture.HeldStatement{Executor: x, Statement: stmt, SQL: q}
}

func TestReferenceCollationFunctionsVersions(t *testing.T) {
	collationFunctionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		before := collationFunctionsBag(t, p, b, "pg_catalog")
		for _, suffix := range []string{"", " WHERE false"} {
			rows := [][]any{{nil, nil, nil}}
			if suffix != "" {
				rows = nil
			}
			s := fixture.Select(`SELECT pg_collation_actual_version(NULL::oid) AS c,pg_database_collation_actual_version(NULL::oid) AS d,pg_import_system_collations(NULL::regnamespace) AS n`+suffix, []string{"c", "d", "n"}, []string{"TEXT", "TEXT", "INT4"}, rows)
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
				cols, err := r.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				for _, col := range cols {
					if _, ok := col.Nullable(); ok && b.PG {
						t.Fatal("pq unexpectedly claims nullable metadata")
					}
				}
			}
			fixture.CollationRoutes(t, p, b, s)
		}
		fixture.CollationRoutes(t, p, b, fixture.Select(`SELECT pg_collation_actual_version(950::oid) AS c,pg_collation_actual_version(951::oid) AS p,pg_collation_actual_version(100::oid) AS d,pg_collation_actual_version('pg_catalog.pg_c_utf8'::regcollation::oid) AS b`, []string{"c", "p", "d", "b"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{nil, nil, nil, "1"}}))
		fixture.CollationRoutes(t, p, b, fixture.Select(`SELECT datlocprovider,datcollate,datctype,datcollversion,pg_database_collation_actual_version(oid) AS actual FROM pg_catalog.pg_database WHERE datname=current_database()`, []string{"datlocprovider", "datcollate", "datctype", "datcollversion", "actual"}, []string{"CHAR", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"c", "C", "C", nil, nil}}))
		var databaseOID int64
		if err := b.DB.QueryRowContext(p.Ctx, `SELECT oid FROM pg_database WHERE datname=current_database()`).Scan(&databaseOID); err != nil {
			t.Fatal(err)
		}
		if databaseOID <= 0 {
			t.Fatal("invalid current database identity", databaseOID)
		}
		if b.PG {
			fixture.CollationRoutes(t, p, b, fixture.Select(`SELECT pg_collation_actual_version('pg_catalog.unicode'::regcollation::oid) IS NOT NULL AS available`, []string{"available"}, []string{"BOOL"}, [][]any{{true}}))
		} else {
			fixture.CollationRoutes(t, p, b, fixture.Select(`SELECT pg_collation_actual_version('pg_catalog.unicode'::regcollation::oid) AS actual`, []string{"actual"}, []string{"TEXT"}, [][]any{{nil}}))
		}
		icuQuery := `SELECT pg_collation_actual_version($1) AS actual`
		icuHeld := collationFunctionsHeld(t, p, b, b.DB, icuQuery)
		for _, arg := range []any{int64(963), int64(950), int64(811), nil, int64(963)} {
			if b.PG && arg == int64(963) {
				rows, err := icuHeld.Statement.QueryContext(p.Ctx, arg)
				if err != nil {
					t.Fatal(err)
				}
				cols, err := rows.ColumnTypes()
				if err != nil {
					rows.Close()
					t.Fatal(err)
				}
				if len(cols) != 1 || cols[0].Name() != "actual" || cols[0].DatabaseTypeName() != "TEXT" {
					rows.Close()
					t.Fatal("held ICU version metadata", cols)
				}
				var version sql.NullString
				if !rows.Next() {
					rows.Close()
					t.Fatal("missing held ICU version row")
				}
				if err := rows.Scan(&version); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				if !version.Valid || version.String == "" {
					rows.Close()
					t.Fatal("installed ICU provider has no actual version")
				}
				extra := rows.Next()
				err = rows.Err()
				rows.Close()
				if extra || err != nil {
					t.Fatal("held ICU version row completion", extra, err)
				}
				continue
			}
			var want any
			if arg == int64(811) {
				want = "1"
			}
			fixture.RunStep(t, p.Ctx, icuHeld, b.Recorder, fixture.Select(icuQuery, []string{"actual"}, []string{"TEXT"}, [][]any{{want}}, arg))
		}
		for _, tc := range []struct {
			fn, typ string
			valid   any
			want    any
		}{
			{"pg_collation_actual_version", "TEXT", int64(950), nil},
			{"pg_database_collation_actual_version", "TEXT", databaseOID, nil},
			{"pg_import_system_collations", "INT4", nil, nil},
		} {
			q := `SELECT ` + tc.fn + `($1) AS actual`
			h := collationFunctionsHeld(t, p, b, b.DB, q)
			for _, arg := range []any{nil, tc.valid, nil} {
				fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.Select(q, []string{"actual"}, []string{tc.typ}, [][]any{{tc.want}}, arg))
			}
			if tc.fn == "pg_database_collation_actual_version" {
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42704", Message: "database with OID 0 does not exist"}, int64(0))
				s.SelectRows = true
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.Select(q, []string{"actual"}, []string{"TEXT"}, [][]any{{nil}}, databaseOID))
			}
		}
		if after := collationFunctionsBag(t, p, b, "pg_catalog"); !reflect.DeepEqual(before, after) {
			t.Fatal("version inspection changed recorded catalog definitions")
		}
	})
}

func TestReferenceCollationFunctionsDiagnostics(t *testing.T) {
	collationFunctionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, n := range []int64{0, 4294967295} {
			for _, kind := range []string{"collation", "database"} {
				fn := "pg_" + kind + "_actual_version"
				if kind == "database" {
					fn = "pg_database_collation_actual_version"
				}
				collationFunctionsError(t, p, b, `SELECT `+fn+`($1::oid) AS actual`, "42704", fmt.Sprintf("%s with OID %d does not exist", kind, n), n)
			}
			collationFunctionsError(t, p, b, `SELECT pg_import_system_collations($1::regnamespace) AS n`, "3F000", fmt.Sprintf("schema with OID %d does not exist", n), n)
		}
		for _, tc := range []struct{ q, code, message, hint, position string }{
			{`SELECT 'é',pg_import_system_collations('cf_missing_namespace')`, "3F000", `schema "cf_missing_namespace" does not exist`, "", "40"},
			{`SELECT 'é',pg_database_collation_actual_version(true)`, "42883", "function pg_database_collation_actual_version(boolean) does not exist", "No function matches the given name and argument types. You might need to add explicit type casts.", "12"},
			{`SELECT pg_import_system_collations()`, "42883", "function pg_import_system_collations() does not exist", "No function matches the given name and argument types. You might need to add explicit type casts.", "8"},
		} {
			s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Hint: tc.hint, Position: tc.position})
			s.SelectRows = true
			fixture.CollationRoutes(t, p, b, s)
		}
		const signatureHint = "No function matches the given name and argument types. You might need to add explicit type casts."
		for _, fn := range []string{"pg_collation_actual_version", "pg_database_collation_actual_version", "pg_import_system_collations"} {
			for _, tc := range []struct{ call, signature string }{
				{fn + "()", fn + "()"},
				{fn + "(NULL,NULL)", fn + "(unknown, unknown)"},
				{fn + "(true)", fn + "(boolean)"},
				{fn + "('x'::text)", fn + "(text)"},
				{"public." + fn + "(NULL)", "public." + fn + "(unknown)"},
			} {
				fixture.CollationError(t, p, b, "SELECT "+tc.call, fixture.Diagnostic{Code: "42883", Message: "function " + tc.signature + " does not exist", Hint: signatureHint, Position: "8"})
			}
		}
		fixture.CollationError(t, p, b, `SELECT pg_collation_actual_version_missing(NULL)`, fixture.Diagnostic{Code: "42883", Message: "function pg_collation_actual_version_missing(unknown) does not exist", Hint: signatureHint, Position: "8"})
		for _, tc := range []struct{ fn, position string }{
			{"pg_collation_actual_version", "36"},
			{"pg_database_collation_actual_version", "45"},
		} {
			fixture.CollationError(t, p, b, "SELECT "+tc.fn+"('cf_bad_oid')", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type oid: "cf_bad_oid"`, Position: tc.position})
			q := "SELECT " + tc.fn + "($1::oid) AS actual"
			h := collationFunctionsHeld(t, p, b, b.DB, q)
			d := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type oid: "cf_bad_oid"`)
			d.Context = "unnamed portal parameter $1 = '...'"
			s := fixture.ImplicitError(q, d, "cf_bad_oid")
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.Select(q, []string{"actual"}, []string{"TEXT"}, [][]any{{nil}}, nil))
		}
		for _, q := range []string{`SELECT pg_import_system_collations($1::regnamespace)`, `SELECT pg_import_system_collations($1::text::regnamespace)`} {
			t.Run(q, func(t *testing.T) {
				h := collationFunctionsHeld(t, p, b, b.DB, q)
				var value any
				err := h.Statement.QueryRowContext(p.Ctx, "cf_missing_namespace").Scan(&value)
				want := fixture.ControlsReferenceDiagnostic("3F000", `schema "cf_missing_namespace" does not exist`)
				if q == `SELECT pg_import_system_collations($1::regnamespace)` {
					want.Context = "unnamed portal parameter $1 = '...'"
				}
				fixture.AssertDiagnostic(t, err, &want)
				fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.Select(q, []string{"pg_import_system_collations"}, []string{"INT4"}, [][]any{{nil}}, nil))
			})
		}
	})
}
