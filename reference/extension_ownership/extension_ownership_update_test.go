package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func ownershipUpdateMembership(t *testing.T) {
	ownershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := ownershipRun(t, p, b, c)
		for _, q := range []string{`CREATE EXTENSION fuzzystrmatch VERSION '1.1'`, `CREATE FUNCTION eo_added(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1+1'`, `ALTER EXTENSION fuzzystrmatch DROP FUNCTION levenshtein(text,text)`, `ALTER EXTENSION fuzzystrmatch ADD FUNCTION eo_added(integer)`} {
			run(fixture.Exec(q, 0))
		}
		q := `SELECT oid::bigint,proowner::bigint,proargtypes::text,prosrc FROM pg_proc WHERE oid IN ('levenshtein(text,text)'::regprocedure,'eo_added(integer)'::regprocedure) ORDER BY oid`
		before := ownershipSnapshot(t, p, c, q)
		run(fixture.Exec(`ALTER EXTENSION fuzzystrmatch UPDATE TO '1.2'`, 0))
		ownershipUnchanged(t, p, c, q, before)
		run(fixture.Select(`SELECT levenshtein('kitten','sitting') AS distance,eo_added(4) AS added`, []string{"distance", "added"}, []string{"INT4", "INT4"}, [][]any{{int64(3), int64(5)}}))
		run(ownershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_proc'::regclass AND objid='levenshtein(text,text)'::regprocedure AND deptype='e'`, 0))
		run(ownershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_proc'::regclass AND objid='eo_added(integer)'::regprocedure AND refclassid='pg_extension'::regclass AND refobjid=(SELECT oid FROM pg_extension WHERE extname='fuzzystrmatch') AND deptype='e'`, 1))
		run(ownershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_proc'::regclass AND objid='daitch_mokotoff(text)'::regprocedure AND refclassid='pg_extension'::regclass AND refobjid=(SELECT oid FROM pg_extension WHERE extname='fuzzystrmatch') AND deptype='e'`, 1))
	})
}
