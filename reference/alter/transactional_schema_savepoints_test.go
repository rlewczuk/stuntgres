package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalSchemaSavepoints(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "tx_sp_early", "tx_sp_late", "tx_sp_broken")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, peer := fixture.LockConnections(t, p.Ctx, b.DB)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("BEGIN", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			run(fixture.Exec("CREATE SCHEMA tx_sp_early CREATE TABLE kept(v INT)", 0))
			run(fixture.Exec("INSERT INTO tx_sp_early.kept VALUES(1)", 1))
			run(fixture.Exec("SAVEPOINT earlier", 0))
			for range 2 {
				run(fixture.Exec("CREATE SCHEMA tx_sp_late CREATE TABLE gone(id SERIAL PRIMARY KEY, v INT CHECK(v>0))", 0))
				run(fixture.Exec("INSERT INTO tx_sp_late.gone(v) VALUES(2)", 1))
				run(fixture.Exec("INSERT INTO tx_sp_early.kept VALUES(2)", 1))
				var namespace int64
				if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_namespace WHERE nspname='tx_sp_late'").Scan(&namespace); err != nil {
					t.Fatal(err)
				}
				run(fixture.Exec("ROLLBACK TO earlier", 0))
				run(fixture.Select("SELECT v FROM tx_sp_early.kept", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
				run(fixture.Select("SELECT count(*) FROM pg_namespace WHERE nspname='tx_sp_late'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				run(fixture.Select("SELECT count(*) FROM pg_class WHERE relnamespace=$1", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, namespace))
				run(fixture.Select("SELECT count(*) FROM pg_type WHERE typnamespace=$1", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, namespace))
				run(fixture.Select("SELECT count(*) FROM pg_constraint WHERE connamespace=$1", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, namespace))
				run(fixture.Select("SELECT count(*) FROM pg_depend WHERE refclassid='pg_namespace'::regclass AND refobjid=$1", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, namespace))
			}
			run(fixture.ErrorDiagnostic("ALTER TABLE tx_sp_early.kept ADD COLUMN added INT, ADD COLUMN v INT", fixture.ControlsReferenceDiagnostic("42701", `column "v" of relation "kept" already exists`)))
			run(fixture.ErrorDiagnostic("SELECT 1", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
			run(fixture.Exec("ROLLBACK TO earlier", 0))
			run(fixture.Select("SELECT * FROM tx_sp_early.kept", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
			run(fixture.ErrorDiagnostic("CREATE SCHEMA tx_sp_broken CREATE TABLE first(v INT) CREATE TABLE first(v INT)", fixture.ControlsReferenceDiagnostic("42P07", `relation "first" already exists`)))
			run(fixture.Exec("ROLLBACK TO earlier", 0))
			run(fixture.Select("SELECT count(*) FROM pg_namespace WHERE nspname='tx_sp_broken'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			run(fixture.Exec("RELEASE earlier", 0))
			run(fixture.Exec("ROLLBACK", 0))
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT count(*) FROM pg_namespace WHERE nspname IN ('tx_sp_early','tx_sp_late','tx_sp_broken')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("CREATE TABLE tx_sp_committed(v INT)", 0))
			run(fixture.Exec("INSERT INTO tx_sp_committed VALUES(1)", 1))
			run(fixture.Exec("SAVEPOINT survives", 0))
			run(fixture.Exec("INSERT INTO tx_sp_committed VALUES(2)", 1))
			run(fixture.Exec("ROLLBACK TO survives", 0))
			run(fixture.Exec("COMMIT", 0))
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT v FROM tx_sp_committed", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
		})
	}
}
