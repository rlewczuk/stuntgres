package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSHeadlineAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"headline_actor"})
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer ftsConvertSetting(t, p, c)()
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE ROLE headline_actor", 0))
		for _, types := range ftsHeadlineForms {
			query := "SELECT pg_catalog.ts_headline(" + ftsRankParameters(types) + ") AS excerpt"
			signature := "pg_catalog.ts_headline(" + strings.Join(types, ",") + ")"
			func() {
				stmt, err := c.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				defer func() {
					ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
					defer cancel()
					if _, err := c.ExecContext(ctx, "RESET ROLE; GRANT EXECUTE ON FUNCTION "+signature+" TO PUBLIC"); err != nil {
						t.Error(err)
					}
				}()
				args := ftsHeadlineArguments(types)
				rows, err := stmt.QueryContext(p.Ctx, args...)
				if err != nil {
					t.Fatal(err)
				}
				retained := ftsRankRead(t, p, b, rows, "excerpt", "TEXT", "<b>Cat</b> rat", false)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("REVOKE EXECUTE ON FUNCTION "+signature+" FROM PUBLIC", 0))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET ROLE headline_actor", 0))
				d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for function ts_headline")
				ftsRankDiagnosticRoutes(t, p, b, c, query, d, false, args...)
				for range 2 {
					s := fixture.ErrorDiagnostic(query, d, args...)
					s.SelectRows, s.ErrorStage = true, "execute"
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}, b.Recorder, s)
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET ROLE", 0))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("GRANT EXECUTE ON FUNCTION "+signature+" TO PUBLIC", 0))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET ROLE headline_actor", 0))
				rows, err = stmt.QueryContext(p.Ctx, args...)
				if err != nil {
					t.Fatal(err)
				}
				ftsRankRead(t, p, b, rows, "excerpt", "TEXT", "<b>Cat</b> rat", false)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET ROLE", 0))
				if retained != "<b>Cat</b> rat" {
					t.Fatal("retained headline mutated", retained)
				}
			}()
		}
		// Public visibility honors a user-owned shadow, but a qualified call
		// continues selecting the released text/config signature.
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE FUNCTION public.ts_headline(text,tsquery) RETURNS text LANGUAGE SQL IMMUTABLE AS 'SELECT ''shadow'''", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET search_path=public,pg_catalog", 0))
		defer func() {
			if _, err := c.ExecContext(p.Ctx, "RESET search_path"); err != nil {
				t.Error(err)
			}
		}()
		ftsRankRoutes(t, p, b, c, "SELECT ts_headline('Cat','cat'::tsquery)", "ts_headline", "TEXT", "shadow")
		ftsRankRoutes(t, p, b, c, "SELECT pg_catalog.ts_headline('Cat','cat'::tsquery)", "ts_headline", "TEXT", "<b>Cat</b>")
		owner, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		query := "SELECT pg_catalog.ts_headline('english'::regconfig,$1::text,$2::tsquery,$3::text)"
		stmt, err := owner.PrepareContext(p.Ctx, query)
		if err != nil {
			owner.Close()
			t.Fatal(err)
		}
		rows, err := stmt.QueryContext(p.Ctx, "Rats & cats", "rat", "HighlightAll=true,StartSel=<<,StopSel=>>")
		if err != nil {
			stmt.Close()
			owner.Close()
			t.Fatal(err)
		}
		retained := ftsRankRead(t, p, b, rows, "ts_headline", "TEXT", "<<Rats>> & cats", false)
		rows, err = stmt.QueryContext(p.Ctx, "Cats & rats", "cat", "HighlightAll=true,StartSel=[,StopSel=]")
		if err != nil {
			stmt.Close()
			owner.Close()
			t.Fatal(err)
		}
		ftsRankRead(t, p, b, rows, "ts_headline", "TEXT", "[Cats] & rats", false)
		stmt.Close()
		owner.Close()
		if retained != "<<Rats>> & cats" {
			t.Fatal("closed-owner headline changed", retained)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE VIEW public.headline_dependency AS SELECT pg_catalog.ts_headline('english'::regconfig,'Cat','cat'::tsquery) AS excerpt", 0))
		s := fixture.Select("SELECT count(*)::bigint AS edges FROM pg_catalog.pg_depend WHERE classid=2618 AND objid IN (SELECT oid FROM pg_catalog.pg_rewrite WHERE ev_class='public.headline_dependency'::regclass) AND refclassid=1255 AND refobjid IN (3743,3744,3754,3755)", []string{"edges"}, []string{"INT8"}, [][]any{{int64(0)}})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		ftsRankRoutes(t, p, b, c, "SELECT excerpt FROM public.headline_dependency", "excerpt", "TEXT", ftsRankViewWant{value: "<b>Cat</b>"})
	})
}
