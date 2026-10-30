package reference_test

import (
	"stuntgres/reference/fixture"
)

func ownershipSetup(run func(fixture.Step)) {
	for _, q := range []string{"CREATE SCHEMA eo_old", "CREATE SCHEMA eo_new", "CREATE SCHEMA eo_other", "CREATE EXTENSION unaccent SCHEMA eo_old"} {
		run(fixture.Exec(q, 0))
	}
}

const ownershipEdges = `SELECT d.classid::bigint,d.objid::bigint,d.refobjid::bigint,d.deptype FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.refclassid='pg_extension'::regclass AND e.extname='unaccent' AND d.deptype='e' ORDER BY d.classid,d.objid`
const ownershipProcedures = `SELECT p.oid::bigint,p.proowner::bigint,p.proname,p.proargtypes::text FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname IN ('eo_old','eo_new') ORDER BY p.oid`

func ownershipCallable(run func(fixture.Step), schema string) {
	q := "SELECT " + schema + ".unaccent('Hôtel') AS translated," + schema + ".unaccent(NULL::text) AS absent," + schema + ".unaccent('') AS empty"
	s := fixture.Select(q, []string{"translated", "absent", "empty"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"Hotel", nil, ""}})
	run(s)
	run(fixture.Prepared(s))
}

func ownershipMemberCount(run func(fixture.Step), n int64) {
	run(fixture.OwnershipCount(`SELECT count(*) FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.refclassid='pg_extension'::regclass AND e.extname='unaccent' AND d.deptype='e'`, n))
}
