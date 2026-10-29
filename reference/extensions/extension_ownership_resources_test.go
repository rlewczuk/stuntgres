package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceExtensionOwnershipResources(t *testing.T) {
	ownershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, peer *sql.Conn) {
		run := ownershipRun(t, p, b, peer)
		run(fixture.Exec(`CREATE SCHEMA eo_other`, 0))
		run(fixture.Exec(`CREATE TEMP TABLE eo_peer_temp(v integer)`, 0))
		run(fixture.Exec(`INSERT INTO eo_peer_temp VALUES(73)`, 1))
		owner := extensionTemporaryOwner(t, p, b.PG)
		defer owner.close(t)
		name := extensionTempSeed(t, p, owner)
		qualified := extensionTempIdentifier(name)
		// A different session installs into the genuine owner's TEMP namespace;
		// functions in one's own TEMP namespace are not SQL lookup candidates.
		run(fixture.Exec(`CREATE EXTENSION unaccent SCHEMA `+qualified, 0))
		run(fixture.Exec(`CREATE FUNCTION eo_other.survivor(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1+1'`, 0))
		run(fixture.Exec(`ALTER EXTENSION unaccent ADD FUNCTION eo_other.survivor(integer)`, 0))
		run(fixture.Exec(`ALTER EXTENSION unaccent DROP FUNCTION eo_other.survivor(integer)`, 0))
		ownerRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, owner.conn, owner.recorder, s) }
		ownerRun(fixture.Exec(`CREATE TEMP TABLE eo_detached(v integer)`, 0))
		run(fixture.Exec(`ALTER EXTENSION unaccent ADD TABLE `+qualified+`.eo_detached`, 0))
		run(fixture.Exec(`ALTER EXTENSION unaccent DROP TABLE `+qualified+`.eo_detached`, 0))
		ownerRun(fixture.Select(`SELECT pg_advisory_lock(30790018)`, []string{"pg_advisory_lock"}, []string{"VOID"}, [][]any{{""}}))
		if !b.PG {
			var attempted bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &attempted); err == nil || err.Error() != "stuntgres: temporary extension installation is not portable" || attempted.Len() != 0 {
				t.Fatalf("TEMP Dump output=%q error=%v", attempted.String(), err)
			}
		}
		if !owner.close(t) {
			t.FailNow()
		}
		fixture.ExtensionTempExitBarrier(t, p, peer, b.Recorder, 30790018)
		run(ownershipCount(`SELECT count(*) FROM pg_extension WHERE extname='unaccent'`, 0))
		run(ownershipCount(`SELECT count(*) FROM pg_class WHERE relname='eo_detached'`, 0))
		run(ownershipCount(`SELECT count(*) FROM pg_proc WHERE pronamespace=(SELECT oid FROM pg_namespace WHERE nspname=$1)`, 0, name))
		run(fixture.Select(`SELECT eo_other.survivor(4) AS answer`, []string{"answer"}, []string{"INT4"}, [][]any{{int64(5)}}))
		run(fixture.Select(`SELECT v FROM eo_peer_temp`, []string{"v"}, []string{"INT4"}, [][]any{{int64(73)}}))
		if !b.PG {
			var portable bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &portable); err != nil {
				t.Fatal("retired TEMP provenance prevents portable survivor dump", err)
			}
		}
	})
}
