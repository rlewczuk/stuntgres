package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceVersionInformationStoredControls(t *testing.T) {
	versionInformationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		run(fixture.Exec("CREATE VIEW public.version_information_view AS SELECT version() AS v, unicode_version() AS u, icu_unicode_version() AS i", 0))
		run(fixture.Exec("CREATE TABLE version_information_defaults(id INT, v TEXT DEFAULT version(), u TEXT DEFAULT unicode_version(), i TEXT DEFAULT icu_unicode_version())", 0))
		run(fixture.Exec("INSERT INTO version_information_defaults(id) VALUES(1)", 1))
		bannerPredicate := "v = 'PostgreSQL 18.0 compatible; embedded stuntgres'"
		var icu any
		if b.PG {
			bannerPredicate = "v LIKE 'PostgreSQL 18.6 %'"
			icu = "15.0"
		}
		viewQuery := "SELECT " + bannerPredicate + " AS compatible, u, i FROM version_information_view"
		tableQuery := "SELECT id, " + bannerPredicate + " AS compatible, u, i FROM version_information_defaults ORDER BY id"
		for _, prepared := range []bool{false, true} {
			s := fixture.Select(viewQuery, []string{"compatible", "u", "i"}, []string{"BOOL", "TEXT", "TEXT"}, [][]any{{true, "16.0", icu}})
			s.Prepared = prepared
			run(s)
			s = fixture.Select(tableQuery, []string{"id", "compatible", "u", "i"}, []string{"INT4", "BOOL", "TEXT", "TEXT"}, [][]any{{int64(1), true, "16.0", icu}})
			s.Prepared = prepared
			run(s)
		}
		run(fixture.Exec("CREATE TABLE version_information_group_rows(id INT)", 0))
		run(fixture.Exec("INSERT INTO version_information_group_rows VALUES(1),(2)", 2))
		bannerTest := "version() = 'PostgreSQL 18.0 compatible; embedded stuntgres'"
		if b.PG {
			bannerTest = "version() LIKE 'PostgreSQL 18.6 %'"
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT "+bannerTest+" AS compatible, unicode_version(), icu_unicode_version(), count(*) FROM version_information_group_rows",
				[]string{"compatible", "unicode_version", "icu_unicode_version", "count"}, []string{"BOOL", "TEXT", "TEXT", "INT8"}, [][]any{{true, "16.0", icu, int64(2)}})
			s.Prepared = prepared
			run(s)
		}
		if b.PG {
			return // PostgreSQL has no native catalog-replacement control API.
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		checkRestored := func(t *testing.T, target *fixture.Pair, db *sql.DB, recorder *fixture.Recorder) {
			t.Helper()
			for _, prepared := range []bool{false, true} {
				s := fixture.Select(viewQuery, []string{"compatible", "u", "i"}, []string{"BOOL", "TEXT", "TEXT"}, [][]any{{true, "16.0", nil}})
				s.Prepared = prepared
				fixture.RunStep(t, target.Ctx, db, recorder, s)
			}
			fixture.RunStep(t, target.Ctx, db, recorder, fixture.Exec("INSERT INTO version_information_defaults(id) VALUES(2)", 1))
			fixture.RunStep(t, target.Ctx, db, recorder, fixture.Select(tableQuery,
				[]string{"id", "compatible", "u", "i"}, []string{"INT4", "BOOL", "TEXT", "TEXT"},
				[][]any{{int64(1), true, "16.0", nil}, {int64(2), true, "16.0", nil}}))
		}
		checkRestored(t, p, b.DB, b.Recorder)
		destination := fixture.NewFixture(t, false, true)
		if err := stuntgres.Restore(destination.Ctx, destination.Native, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		for _, target := range destination.Backends() {
			checkRestored(t, destination, target.DB, target.Recorder)
		}
	})
}

func TestReferenceVersionInformationCancellation(t *testing.T) {
	versionInformationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn) {
		var icu any
		if b.PG {
			icu = "15.0"
		}
		query := "SELECT unicode_version(), icu_unicode_version()"
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select(query,
			[]string{"unicode_version", "icu_unicode_version"}, []string{"TEXT", "TEXT"}, [][]any{{"16.0", icu}}))
		if b.PG {
			return // Fast pq precancellation has no deterministic admission window.
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		const inquiries = "SELECT version(), unicode_version(), icu_unicode_version()"
		rows, err := conn.QueryContext(ctx, inquiries)
		if rows != nil {
			rows.Close()
			t.Error("precanceled QueryContext published rows")
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("precanceled QueryContext = %v, want context.Canceled", err)
		}
		stmt, err := conn.PrepareContext(ctx, inquiries)
		if stmt != nil {
			stmt.Close()
			t.Error("precanceled PrepareContext published a statement")
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("precanceled PrepareContext = %v, want context.Canceled", err)
		}
	})
}

func TestReferenceVersionInformationSmoke(t *testing.T) {
	t.Run("values", TestReferenceVersionInformationValues)
	t.Run("provider", TestReferenceVersionInformationUnicodeProvider)
	t.Run("stored", TestReferenceVersionInformationStoredControls)
	t.Run("diagnostic", func(t *testing.T) {
		versionInformationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn) {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("SELECT version(NULL)", fixture.Diagnostic{
					Code: "42883", Message: "function version(unknown) does not exist", Position: "8",
					Hint: "No function matches the given name and argument types. You might need to add explicit type casts.",
				})
				s.Prepared, s.SelectRows, s.ErrorStage = prepared, true, "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
			}
		})
	})
}
