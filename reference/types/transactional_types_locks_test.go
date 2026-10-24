package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalTypesLocks(t *testing.T) {
	for _, kind := range []string{"domain_validation", "composite_evolution", "domain_drop", "type_drop"} {
		t.Run(kind, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, writer := fixture.LockConnections(t, p.Ctx, b.DB)
					reader, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer reader.Close()
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					for _, q := range []string{"CREATE DOMAIN tt_d AS integer", "CREATE TYPE tt_pair AS(id integer)", "CREATE TABLE tt_data(v tt_d)", "CREATE TABLE tt_typed OF tt_pair", "CREATE TABLE tt_unrelated(v integer)"} {
						run(fixture.Exec(q, 0))
					}
					target, query, lockMode := "tt_data", "ALTER DOMAIN tt_d ADD CONSTRAINT positive CHECK(VALUE>0)", "ShareLock"
					if kind == "composite_evolution" {
						target, query, lockMode = "tt_typed", "ALTER TYPE tt_pair ADD ATTRIBUTE note text CASCADE", "AccessExclusiveLock"
					}
					if kind == "domain_drop" {
						query, lockMode = "DROP DOMAIN tt_d CASCADE", "AccessExclusiveLock"
					}
					if kind == "type_drop" {
						target, query, lockMode = "tt_typed", "DROP TYPE tt_pair CASCADE", "AccessExclusiveLock"
					}
					waiterPID, holderPID := 0, 0
					var oid int64
					if b.PG {
						waiterPID, holderPID = fixture.RowPIDs(t, p.Ctx, c, writer)
						oid = fixture.LockOID(t, p.Ctx, target)
					}
					tx, err := writer.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("INSERT INTO "+target+" VALUES(1)", 1))
					run(fixture.Exec("BEGIN", 0))
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					run(fixture.Exec("SAVEPOINT waiting", 0))
					fixture.BinaryConversionActiveCancelAfter(t, p, b, c, query, "(*relationRequest).wait", func(ctx context.Context) {
						if b.PG {
							fixture.LockWaitBehind(t, ctx, waiterPID, holderPID, oid, lockMode)
						}
						fixture.RunStep(t, ctx, reader, b.Recorder, fixture.Exec("INSERT INTO tt_unrelated VALUES(42)", 1))
						if kind == "domain_validation" {
							fixture.RunStep(t, ctx, reader, b.Recorder, fixture.Select("SELECT count(*) FROM tt_data", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
						}
					}, func(ctx context.Context) fixture.RelationOutcome { return fixture.RowExecute(ctx, c, query, false) })
					run(fixture.Exec("ROLLBACK TO waiting", 0))
					run(fixture.Exec("ROLLBACK", 0))
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					// The same physical owner and real follower must both remain usable.
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec(query, 0))
					run(fixture.Exec("ROLLBACK", 0))
					fixture.RunStep(t, p.Ctx, writer, b.Recorder, fixture.Exec("INSERT INTO "+target+" VALUES(2)", 1))
					run(fixture.Select("SELECT v FROM tt_unrelated", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
				})
			}
		})
	}
}
