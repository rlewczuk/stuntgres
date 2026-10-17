package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGTypeFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pt18_smoke_reader"})
	pgTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		var numeric, raw [][]any
		for _, id := range []int64{16, 20, 23, 25} {
			r := pgTypeGolden(id)
			numeric = append(numeric, r)
			raw = append(raw, pgTypeRawFacts(r))
		}
		fixture.PgDescriptionRows(t, p, b, fixture.Select("SELECT "+pgTypeNumericProjection+" FROM pg_type WHERE oid IN (16,20,23,25) ORDER BY oid", pgTypeColumns, pgTypeNumericTypes(), numeric))
		fixture.PgDescriptionRows(t, p, b, pgTypeStep(b, "SELECT * FROM pg_type WHERE oid IN (16,20,23,25) ORDER BY oid", raw))
		q := "SELECT * FROM pg_catalog.pg_type WHERE oid=$1::oid"
		held := fixture.ViewHold(t, p, b, q)
		for _, id := range []int64{23, 25, 0, 23} {
			var want [][]any
			if id != 0 {
				want = [][]any{pgTypeRawFacts(pgTypeGolden(id))}
			}
			fixture.PgDescriptionExecutorRows(t, p, b, held, pgTypeStep(b, q, want, id))
		}
		for _, prepared := range []bool{false, true} {
			stage := "execute"
			if prepared {
				stage = "prepare"
			}
			pgTypeError(t, p, b, b.DB, "SELECT missing FROM pg_catalog.pg_type", fixture.Diagnostic{Code: "42703", Message: "column \"missing\" does not exist", Position: "8"}, prepared, stage)
		}
		fixture.PgDescriptionExecutorRows(t, p, b, held, pgTypeStep(b, q, [][]any{pgTypeRawFacts(pgTypeGolden(23))}, int64(23)))
		func() {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("CREATE ROLE pt18_smoke_reader", 0))
			defer func() {
				for _, q := range []string{"RESET ROLE", "DROP ROLE pt18_smoke_reader"} {
					if _, err := conn.ExecContext(p.Ctx, q); err != nil {
						t.Error(err)
					}
				}
			}()
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("SET ROLE pt18_smoke_reader", 0))
			fixture.PgDescriptionExecutorRows(t, p, b, conn, fixture.Select("SELECT oid,typname FROM pg_type WHERE oid=23", []string{"oid", "typname"}, []string{"OID", "NAME"}, [][]any{{int64(23), "int4"}}))
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("RESET ROLE", 0))
		}()
		// Genuine enum/table SQL and native Dump/Reset/Restore, shared with lifecycle.
		pgTypeLogicalControls(t, p, b)
	})
}
