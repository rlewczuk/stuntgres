package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatUserTablesEventsAndRollback(t *testing.T) {
	f := tableStatFamily{user: true}
	t.Run("settlement", func(t *testing.T) { tableStatEventsAndRollback(t, f) })
	// RETURNING executes a real accepted prefix on both backends, unlike
	// ordinary native VALUES, which validates its entire input before staging.
	t.Run("accepted_prefix", func(t *testing.T) {
	f.scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.StatisticsExec(t, p, b, c, "CREATE TABLE sut_atomic(n integer NOT NULL)", 0)
		fixture.StatisticsExec(t, p, b, c, "ALTER TABLE sut_atomic SET(autovacuum_enabled=false)", 0)
		w, retire := fixture.StatisticsReportingConn(t, p, b)
		fixture.StatisticsExec(t, p, b, w, "BEGIN", 0)
		for _, prepared := range []bool{false, true} {
			fixture.StatisticsExec(t, p, b, w, "SAVEPOINT streamed", 0)
			step := fixture.ImplicitError("INSERT INTO public.sut_atomic VALUES(2),(NULL) RETURNING n", fixture.Diagnostic{Code: "23502", Message: `null value in column "n" of relation "sut_atomic" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "sut_atomic", Column: "n"})
			step.Prepared = prepared
			step.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, w, b.Recorder, step)
			fixture.StatisticsExec(t, p, b, w, "ROLLBACK TO streamed", 0)
			want := int64(1)
			if prepared { want = 2 }
			xactTableCounts(t, xactUserGet(t, p, w, "sut_atomic"), want, 0, 0)
			fixture.RunStep(t, p.Ctx, w, b.Recorder, fixture.Select("SELECT count(*) FROM sut_atomic", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		}
		xactTableEnd(t, p, b, w, "ROLLBACK")
		retire()
		r := f.await(t, p, b, "sut_atomic", "executed failed prefixes2", func(r tableStatRow) bool { return r.Inserts == 2 })
		if r.Mods != 0 || r.Updates != 0 || r.Deletes != 0 || r.Since != 2 { t.Fatal("failed statement settlement", r) }
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) FROM sut_atomic", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	})
	})
}

func TestReferencePGStatUserTablesMaintenance(t *testing.T) {
	tableStatMaintenance(t, tableStatFamily{user: true})
}
