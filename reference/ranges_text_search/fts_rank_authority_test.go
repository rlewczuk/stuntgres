package reference_test

import (
	"context"
	"database/sql"
	"math"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSRankAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"rank_actor"})
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE ROLE rank_actor", 0))
		for _, name := range ftsRankNames {
			for _, types := range ftsRankForms {
				query := "SELECT pg_catalog." + name + "(" + ftsRankParameters(types) + ") AS score"
				signature := "pg_catalog." + name + "(" + strings.Join(types, ",") + ")"
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
					args := ftsRankArguments(types, "cat:1", "cat")
					want := ftsRankExact(.06079271)
					if name == "ts_rank_cd" {
						want = ftsRankExact(.1)
					}
					rows, err := stmt.QueryContext(p.Ctx, args...)
					if err != nil {
						t.Fatal(err)
					}
					retained := ftsRankRead(t, p, b, rows, "score", "FLOAT4", want, false)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("REVOKE EXECUTE ON FUNCTION "+signature+" FROM PUBLIC", 0))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET ROLE rank_actor", 0))
					d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for function "+name)
					ftsRankDiagnosticRoutes(t, p, b, c, query, d, false, args...)
					for range 2 {
						s := fixture.ErrorDiagnostic(query, d, args...)
						s.SelectRows, s.ErrorStage = true, "execute"
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}, b.Recorder, s)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET ROLE", 0))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("GRANT EXECUTE ON FUNCTION "+signature+" TO PUBLIC", 0))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET ROLE rank_actor", 0))
					rows, err = stmt.QueryContext(p.Ctx, args...)
					if err != nil {
						t.Fatal(err)
					}
					ftsRankRead(t, p, b, rows, "score", "FLOAT4", want, false)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET ROLE", 0))
					if retained != want.value { // retained driver scalar owns its bits.
						if value, ok := retained.(float64); !ok || absRank(value-want.value) > want.tolerance {
							t.Fatal("retained REAL mutated", retained)
						}
					}
				}()
			}
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE VIEW public.rank_dependency AS SELECT pg_catalog.ts_rank('cat:1'::tsvector,'cat'::tsquery) AS score", 0))
		// PostgreSQL pinned builtin objects do not acquire ordinary pg_depend
		// edges. The view still executes the actual qualified routine identity.
		s := fixture.Select("SELECT count(*)::bigint AS edges FROM pg_catalog.pg_depend WHERE classid=2618 AND objid IN (SELECT oid FROM pg_catalog.pg_rewrite WHERE ev_class='public.rank_dependency'::regclass) AND refclassid=1255 AND refobjid IN (3703,3704,3705,3706)", []string{"edges"}, []string{"INT8"}, [][]any{{int64(0)}})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		ftsRankRoutes(t, p, b, c, "SELECT score FROM public.rank_dependency", "score", "FLOAT4", ftsRankViewWant{value: ftsRankExact(.06079271)})
		// Ownership after both source statement and borrowed connection close.
		owner, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		statement, err := owner.PrepareContext(p.Ctx, "SELECT ts_rank('cat:1A,2D'::tsvector,'cat'::tsquery)")
		if err != nil {
			owner.Close()
			t.Fatal(err)
		}
		rows, err := statement.QueryContext(p.Ctx)
		if err != nil {
			statement.Close()
			owner.Close()
			t.Fatal(err)
		}
		retained := ftsRankRead(t, p, b, rows, "ts_rank", "FLOAT4", ftsRankBits(0x3f1f8524), false)
		statement.Close()
		owner.Close()
		ftsRankRoutes(t, p, b, c, "SELECT ts_rank('rat:1'::tsvector,'cat'::tsquery)", "ts_rank", "FLOAT4", ftsRankBits(0))
		value, ok := retained.(float64)
		if !ok || math.Float32bits(float32(value)) != 0x3f1f8524 {
			t.Fatal("closed-owner REAL changed", retained)
		}
	})
}
