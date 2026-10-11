package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

var pgTableColumns = []string{"schemaname", "tablename", "tableowner", "tablespace", "hasindexes", "hasrules", "hastriggers", "rowsecurity"}
var pgTableTypes = []string{"NAME", "NAME", "NAME", "NAME", "BOOL", "BOOL", "BOOL", "BOOL"}
var pgViewColumns = []string{"schemaname", "viewname", "viewowner", "definition"}
var pgViewTypes = []string{"NAME", "NAME", "NAME", "TEXT"}

func pgTableViewsOwner(b fixture.Backend) string {
	if b.PG {
		return "stuntgres"
	}
	return "postgres"
}

func pgTableViewMetadata(t *testing.T, types []*sql.ColumnType, columns, names []string, pg bool) {
	t.Helper()
	if len(types) != len(columns) {
		t.Fatalf("descriptor width: %v", types)
	}
	for i, typ := range types {
		scan := reflect.TypeFor[string]()
		if names[i] == "NAME" && pg {
			scan = reflect.TypeFor[any]()
		}
		if names[i] == "BOOL" {
			scan = reflect.TypeFor[bool]()
		}
		length, sized := typ.Length()
		nullable, known := typ.Nullable()
		precision, scale, decimal := typ.DecimalSize()
		wantLength := int64(0)
		if names[i] == "TEXT" {
			wantLength = math.MaxInt64
		}
		if typ.Name() != columns[i] || typ.DatabaseTypeName() != names[i] || typ.ScanType() != scan || length != wantLength || sized != (names[i] == "TEXT") || nullable != !pg || known != !pg || precision != 0 || scale != 0 || decimal {
			t.Fatalf("column %d: %s %s scan=%v length=%d/%v nullable=%v/%v decimal=%d/%d/%v", i, typ.Name(), typ.DatabaseTypeName(), typ.ScanType(), length, sized, nullable, known, precision, scale, decimal)
		}
	}
}
func pgTableViewSelect(b fixture.Backend, query string, columns, types []string, rows [][]any, args ...any) fixture.Step {
	s := fixture.Select(query, columns, types, rows, args...)
	s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
		descriptions, err := r.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		pgTableViewMetadata(t, descriptions, columns, types, b.PG)
	}
	return s
}
func pgTableRow(schema, name, owner string, indexed bool) []any {
	return []any{schema, name, owner, nil, indexed, false, false, false}
}

const pgTableProbe = "SELECT * FROM pg_catalog.pg_tables WHERE schemaname='public' AND tablename=$1"

func pgTableCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, name string, row []any) {
	t.Helper()
	var rows [][]any
	if row != nil {
		rows = [][]any{row}
	}
	fixture.RunStep(t, p.Ctx, db, b.Recorder, pgTableViewSelect(b, pgTableProbe, pgTableColumns, pgTableTypes, rows, name))
}
func TestReferencePGTableViewsShape(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			for _, source := range []struct {
				name           string
				columns, types []string
			}{{"pg_tables", pgTableColumns, pgTableTypes}, {"pg_views", pgViewColumns, pgViewTypes}} {
				for _, suffix := range []string{" WHERE FALSE", " LIMIT 0"} {
					s := pgTableViewSelect(b, "SELECT * FROM pg_catalog."+source.name+suffix, source.columns, source.types, nil)
					run(s)
					run(fixture.Prepared(s))
				}
			}
			run(fixture.Exec("CREATE TABLE pgtv_shape(n INT)", 0))
			s := pgTableViewSelect(b, pgTableProbe, pgTableColumns, pgTableTypes, [][]any{pgTableRow("public", "pgtv_shape", pgTableViewsOwner(b), false)}, "pgtv_shape")
			run(s)
			run(fixture.Prepared(s))
			for _, arg := range []any{"absent", nil} {
				s := pgTableViewSelect(b, pgTableProbe, pgTableColumns, pgTableTypes, nil, arg)
				run(s)
				run(fixture.Prepared(s))
			}
		})
	}
}

func TestReferencePGTableViewsDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			cases := []struct{ query, code, message, position string }{
				{"SELECT oid FROM pg_catalog.pg_tables", "42703", `column "oid" does not exist`, "8"},
				{"SELECT relpersistence FROM pg_catalog.pg_tables", "42703", `column "relpersistence" does not exist`, "8"},
				{"SELECT relam FROM pg_catalog.pg_tables", "42703", `column "relam" does not exist`, "8"},
				{"SELECT size FROM pg_catalog.pg_tables WHERE FALSE", "42703", `column "size" does not exist`, "8"},
				{"SELECT tablespace FROM pg_catalog.pg_views LIMIT 0", "42703", `column "tablespace" does not exist`, "8"},
				{"SELECT hasindexes FROM pg_catalog.pg_views", "42703", `column "hasindexes" does not exist`, "8"},
				{"SELECT /*é*/ p.missing FROM pg_catalog.pg_tables p", "42703", "column p.missing does not exist", "14"},
				{"SELECT schemaname FROM pg_catalog.pg_tables a, pg_catalog.pg_views b", "42702", `column reference "schemaname" is ambiguous`, "8"},
				{"SELECT * FROM pg_catalog.pg_tables_missing", "42P01", `relation "pg_catalog.pg_tables_missing" does not exist`, "15"},
			}
			for _, c := range cases {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(c.query, fixture.Diagnostic{Code: c.code, Message: c.message, Position: c.position})
					s.SelectRows = true
					s.Prepared = prepared
					if prepared {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
			}
		})
	}
}

func TestReferencePGTableViewsIndexHistory(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(q string) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0)) }
			run("CREATE TABLE pgtv_history(n INT, m INT)")
			pgTableCheck(t, p, b, b.DB, "pgtv_history", pgTableRow("public", "pgtv_history", pgTableViewsOwner(b), false))
			for _, definition := range []string{"(n)", "USING hash(n)", "((n+1))"} {
				run("CREATE INDEX pgtv_index ON pgtv_history " + definition)
				pgTableCheck(t, p, b, b.DB, "pgtv_history", pgTableRow("public", "pgtv_history", pgTableViewsOwner(b), true))
				pgTableCheck(t, p, b, b.DB, "pgtv_index", nil)
				run("REINDEX INDEX pgtv_index")
				pgTableCheck(t, p, b, b.DB, "pgtv_history", pgTableRow("public", "pgtv_history", pgTableViewsOwner(b), true))
				run("DROP INDEX pgtv_index")
				pgTableCheck(t, p, b, b.DB, "pgtv_history", pgTableRow("public", "pgtv_history", pgTableViewsOwner(b), true))
			}
			run("CREATE INDEX pgtv_index ON pgtv_history(m)")
			run("ALTER TABLE pgtv_history DROP COLUMN m")
			if b.PG {
				run("TRUNCATE pgtv_history")
			} else {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError("TRUNCATE pgtv_history", fixture.Diagnostic{Code: "0A000", Message: "statement is unsupported", Position: "1"})
					s.Prepared = prepared
					if prepared {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
			}
			pgTableCheck(t, p, b, b.DB, "pgtv_history", pgTableRow("public", "pgtv_history", pgTableViewsOwner(b), true))
			run("DROP TABLE pgtv_history")
			run("CREATE TABLE pgtv_history(n INT)")
			pgTableCheck(t, p, b, b.DB, "pgtv_history", pgTableRow("public", "pgtv_history", pgTableViewsOwner(b), false))
			run("CREATE TABLE pgtv_unique(n INT UNIQUE)")
			pgTableCheck(t, p, b, b.DB, "pgtv_unique", pgTableRow("public", "pgtv_unique", pgTableViewsOwner(b), true))
		})
	}
}
