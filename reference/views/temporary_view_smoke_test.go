package reference_test

import (
	"bytes"
	"context"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTemporaryViewCancellation(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, holder := fixture.LockConnections(t, p.Ctx, b.DB)
			fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("CREATE TABLE cancellation_source(v INT)", 0))
			for _, prepared := range []bool{false, true} {
				t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
					tx := fixture.QueryLockBegin(t, p.Ctx, holder)
					defer tx.Rollback()
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("LOCK TABLE cancellation_source IN ACCESS EXCLUSIVE MODE", 0))
					q := "CREATE TEMP VIEW cancellation_v AS SELECT v FROM cancellation_source"
					fixture.BinaryConversionActiveCancel(t, p, b, c, q, "relationRequest", func(ctx context.Context) fixture.RelationOutcome {
						if !prepared {
							r, err := c.ExecContext(ctx, q)
							return fixture.RelationOutcome{Result: r, Err: err}
						}
						stmt, err := c.PrepareContext(ctx, q)
						if err != nil {
							return fixture.RelationOutcome{Err: err}
						}
						defer stmt.Close()
						r, err := stmt.ExecContext(ctx)
						return fixture.RelationOutcome{Result: r, Err: err}
					})
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT viewname FROM pg_catalog.pg_views WHERE viewname='cancellation_v'", []string{"viewname"}, []string{"NAME"}, [][]any{}))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP VIEW cancellation_v", 0))
				})
			}
		})
	}
}

func TestReferenceTemporaryViewFeatureSmoke(t *testing.T) {
	temporaryViewScenario(t, func(s *fixture.TempReferenceSession, n *[]fixture.Diagnostic) {
		t := s.T
		s.Utility("CREATE TABLE smoke_public(v INT)", nil)
		s.Exec(s.C, "INSERT INTO smoke_public VALUES(7)", 1)
		s.Utility("CREATE VIEW smoke_same AS SELECT v FROM smoke_public", nil)
		s.Utility("CREATE TEMP TABLE smoke_base(v INT)", nil)
		s.Exec(s.C, "INSERT INTO smoke_base VALUES(11)", 1)
		s.Utility("CREATE TEMP VIEW smoke_same AS SELECT v FROM smoke_base", nil)
		fixture.TemporaryTableRows(s, s.Peer, "smoke_same", 7)
		s.Exec(s.Peer, "CREATE TEMPORARY VIEW smoke_same AS SELECT 22 AS v", 0)
		s.Utility("CREATE VIEW smoke_nested AS SELECT v FROM smoke_same", nil)
		temporaryViewNotices(t, n, "smoke_nested")
		fixture.TemporaryTableRows(s, s.C, "smoke_same", 11)
		fixture.TemporaryTableRows(s, s.Peer, "smoke_same", 22)
		fixture.TemporaryTableRows(s, s.C, "public.smoke_same", 7)
		s.Utility("CREATE TEMP VIEW smoke_unique AS SELECT 33 AS v", nil)
		s.Missing(s.Peer, "smoke_unique", "v", "15")
		if s.Prepared {
			missing := fixture.ImplicitError("SELECT v FROM smoke_unique", fixture.Diagnostic{Code: "42P01", Message: `relation "smoke_unique" does not exist`, Position: "15"})
			missing.Prepared, missing.SelectRows, missing.ErrorStage = true, true, "prepare"
			fixture.RunStep(t, s.P.Ctx, s.Peer, s.Record, missing)
		}
		s.Exec(s.C, "INSERT INTO smoke_base VALUES(12)", 1)
		fixture.TemporaryTableRows(s, s.C, "smoke_nested", 11, 12)
		s.Utility("CREATE VIEW public.smoke_forbidden AS SELECT v FROM smoke_base", fixture.TempReferenceDiagnostic("42P16", "cannot create temporary relation in non-temporary schema", "", ""))
		temporaryViewNotices(t, n, "smoke_forbidden")
		if !s.Postgres {
			dump := fixture.ViewDump(t, s.P)
			fresh := fixture.NewFixture(t, false, true)
			if err := stuntgres.Restore(s.P.Ctx, fresh.Native, bytes.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, s.P.Ctx, fresh.Memory, fresh.MemoryRecord, fixture.Select("SELECT v FROM smoke_same", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
			fixture.RunStep(t, s.P.Ctx, fresh.Memory, fresh.MemoryRecord, fixture.Select("SELECT viewname FROM pg_catalog.pg_views WHERE viewname='smoke_nested' OR viewname='smoke_unique'", []string{"viewname"}, []string{"NAME"}, [][]any{}))
			if err := stuntgres.Reset(s.P.Ctx, s.P.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(s.P.Ctx, s.P.Native, bytes.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			fixture.TemporaryTableRows(s, s.C, "smoke_nested", 11, 12)
			fixture.TemporaryTableRows(s, s.Peer, "smoke_same", 22)
		}
		s.Retire()
		fixture.TemporaryTableRows(s, s.Peer, "smoke_same", 22)
		fixture.TemporaryTableRows(s, s.Peer, "public.smoke_same", 7)
		temporaryViewSelect(s, s.Peer, "SELECT viewname FROM pg_catalog.pg_views WHERE viewname='smoke_nested' OR viewname='smoke_unique'", []string{"viewname"}, []string{"NAME"}, [][]any{})
	})
}
