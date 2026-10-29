package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceExtensionOwnershipTransactions(t *testing.T) {
	ownershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := ownershipRun(t, p, b, c)
		ownershipSetup(run)
		run(fixture.Exec(`CREATE FUNCTION eo_old.private_member(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'`, 0))
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		peerRun := ownershipRun(t, p, b, peer)
		before := ownershipSnapshot(t, p, c, ownershipEdges)
		procs := ownershipSnapshot(t, p, c, ownershipProcedures)
		for _, end := range []string{"ROLLBACK", "COMMIT"} {
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec(`ALTER EXTENSION unaccent ADD FUNCTION eo_old.private_member(integer)`, 0))
			run(fixture.Exec(`SAVEPOINT membership`, 0))
			run(fixture.Exec(`ALTER EXTENSION unaccent SET SCHEMA eo_new`, 0))
			ownershipCallable(run, "eo_new")
			ownershipMemberCount(run, 7)
			ownershipMemberCount(peerRun, 6)
			peerRun(ownershipCount(`SELECT count(*) FROM pg_extension WHERE extname='unaccent' AND extnamespace='eo_old'::regnamespace`, 1))
			run(fixture.Exec(`ROLLBACK TO membership`, 0))
			ownershipCallable(run, "eo_old")
			ownershipMemberCount(run, 7)
			run(fixture.Exec(`ALTER EXTENSION unaccent DROP FUNCTION eo_old.private_member(integer)`, 0))
			run(fixture.Exec(end, 0))
			ownershipUnchanged(t, p, c, ownershipEdges, before)
			ownershipUnchanged(t, p, c, ownershipProcedures, procs)
		}
		// A committed move is visible to a previously borrowed peer, not just a
		// new session; the numerical procedure identities remain the same.
		run(fixture.Exec(`BEGIN`, 0))
		run(fixture.Exec(`ALTER EXTENSION unaccent SET SCHEMA eo_new`, 0))
		run(fixture.Exec(`COMMIT`, 0))
		ownershipCallable(peerRun, "eo_new")
		ownershipUnchanged(t, p, c, ownershipProcedures, procs)
		run(fixture.Exec(`BEGIN`, 0))
		run(fixture.Exec(`ALTER EXTENSION unaccent ADD FUNCTION eo_old.private_member(integer)`, 0))
		run(ownershipError(`ALTER EXTENSION unaccent ADD FUNCTION eo_old.private_member(integer)`, "55000", `function eo_old.private_member(integer) is already a member of extension "unaccent"`))
		run(ownershipError(`SELECT 1`, "25P02", `current transaction is aborted, commands ignored until end of transaction block`))
		run(fixture.Exec(`ROLLBACK`, 0))
		ownershipMemberCount(run, 6)
		for _, q := range []string{`ALTER EXTENSION unaccent SET SCHEMA eo_old`, `ALTER EXTENSION unaccent ADD FUNCTION eo_old.private_member(integer)`, `ALTER EXTENSION unaccent DROP FUNCTION eo_new.unaccent(text)`} {
			run(fixture.Exec(`BEGIN READ ONLY`, 0))
			run(ownershipError(q, "25006", `cannot execute ALTER EXTENSION in a read-only transaction`))
			run(ownershipError(`SELECT 1`, "25P02", `current transaction is aborted, commands ignored until end of transaction block`))
			run(fixture.Exec(`ROLLBACK`, 0))
		}
		// Only native's documented pre-canceled API promises context.Canceled;
		// PostgreSQL user cancellation admission belongs to shared lock fixtures.
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			if _, err := c.ExecContext(ctx, `ALTER EXTENSION unaccent SET SCHEMA eo_old`); !errors.Is(err, context.Canceled) {
				t.Fatalf("pre-canceled move: %v", err)
			}
			ownershipMemberCount(run, 6)
			ownershipCallable(run, "eo_new")
		}
	})
}
