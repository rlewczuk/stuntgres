package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

const pgTableProbe = "SELECT * FROM pg_catalog.pg_tables WHERE schemaname='public' AND tablename=$1"

func pgTableCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, name string, row []any) {
	t.Helper()
	var rows [][]any
	if row != nil {
		rows = [][]any{row}
	}
	fixture.RunStep(t, p.Ctx, db, b.Recorder, fixture.PGTableViewSelect(b, pgTableProbe, fixture.PGTableColumns, fixture.PGTableTypes, rows, name))
}
func TestReferencePGTableViewsShape(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			for _, source := range []struct {
				name           string
				columns, types []string
			}{{"pg_tables", fixture.PGTableColumns, fixture.PGTableTypes}, {"pg_views", fixture.PGViewColumns, fixture.PGViewTypes}} {
				for _, suffix := range []string{" WHERE FALSE", " LIMIT 0"} {
					s := fixture.PGTableViewSelect(b, "SELECT * FROM pg_catalog."+source.name+suffix, source.columns, source.types, nil)
					run(s)
					run(fixture.Prepared(s))
				}
			}
			run(fixture.Exec("CREATE TABLE pgtv_shape(n INT)", 0))
			s := fixture.PGTableViewSelect(b, pgTableProbe, fixture.PGTableColumns, fixture.PGTableTypes, [][]any{fixture.PGTableRow("public", "pgtv_shape", fixture.PGTableViewsOwner(b), false)}, "pgtv_shape")
			run(s)
			run(fixture.Prepared(s))
			for _, arg := range []any{"absent", nil} {
				s := fixture.PGTableViewSelect(b, pgTableProbe, fixture.PGTableColumns, fixture.PGTableTypes, nil, arg)
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
			pgTableCheck(t, p, b, b.DB, "pgtv_history", fixture.PGTableRow("public", "pgtv_history", fixture.PGTableViewsOwner(b), false))
			for _, definition := range []string{"(n)", "USING hash(n)", "((n+1))"} {
				run("CREATE INDEX pgtv_index ON pgtv_history " + definition)
				pgTableCheck(t, p, b, b.DB, "pgtv_history", fixture.PGTableRow("public", "pgtv_history", fixture.PGTableViewsOwner(b), true))
				pgTableCheck(t, p, b, b.DB, "pgtv_index", nil)
				run("REINDEX INDEX pgtv_index")
				pgTableCheck(t, p, b, b.DB, "pgtv_history", fixture.PGTableRow("public", "pgtv_history", fixture.PGTableViewsOwner(b), true))
				run("DROP INDEX pgtv_index")
				pgTableCheck(t, p, b, b.DB, "pgtv_history", fixture.PGTableRow("public", "pgtv_history", fixture.PGTableViewsOwner(b), true))
			}
			run("CREATE INDEX pgtv_index ON pgtv_history(m)")
			run("ALTER TABLE pgtv_history DROP COLUMN m")
			for _, prepared := range []bool{false, true} {
				s := fixture.Exec("TRUNCATE pgtv_history", 0)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				pgTableCheck(t, p, b, b.DB, "pgtv_history", fixture.PGTableRow("public", "pgtv_history", fixture.PGTableViewsOwner(b), true))
			}
			pgTableCheck(t, p, b, b.DB, "pgtv_history", fixture.PGTableRow("public", "pgtv_history", fixture.PGTableViewsOwner(b), true))
			run("DROP TABLE pgtv_history")
			run("CREATE TABLE pgtv_history(n INT)")
			pgTableCheck(t, p, b, b.DB, "pgtv_history", fixture.PGTableRow("public", "pgtv_history", fixture.PGTableViewsOwner(b), false))
			run("CREATE TABLE pgtv_unique(n INT UNIQUE)")
			pgTableCheck(t, p, b, b.DB, "pgtv_unique", fixture.PGTableRow("public", "pgtv_unique", fixture.PGTableViewsOwner(b), true))
		})
	}
}
