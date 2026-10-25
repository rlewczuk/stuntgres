package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func pgAMLinkage(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	pgAMInventory(t, p, b)
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
	for _, q := range []string{
		"CREATE TABLE am_link(n INT)",
		"CREATE INDEX am_link_b ON am_link USING btree(n)",
		"CREATE INDEX am_link_h ON am_link USING hash(n)",
		"CREATE TABLE am_link_g(tenant INET,during INT4RANGE,CONSTRAINT am_link_ex EXCLUDE USING gist(tenant inet_ops WITH =,during WITH &&))",
	} {
		run(fixture.Exec(q, 0))
	}
	run(fixture.Exec("INSERT INTO am_link VALUES(7)", 1))
	run(fixture.Exec("INSERT INTO am_link_g VALUES('10.0.0.1','[1,5)')", 1))
	run(fixture.ErrorDiagnostic("INSERT INTO am_link_g VALUES('10.0.0.1','[3,7)')", fixture.ExclusionReferenceConflict("am_link_g", "am_link_ex", "tenant, during", "10.0.0.1, [3,7)", "10.0.0.1, [1,5)")))
	run(fixture.Select("SELECT n FROM am_link", []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}}))
	run(fixture.Select("SELECT tenant,during FROM am_link_g", []string{"tenant", "during"}, []string{"INET", "INT4RANGE"}, [][]any{{"10.0.0.1", "[1,5)"}}))
	links := fixture.Select("SELECT c.relname,a.oid,a.amname,a.amtype FROM pg_class c JOIN pg_am a ON c.relam=a.oid JOIN pg_namespace n ON c.relnamespace=n.oid WHERE n.nspname='public' AND c.relname IN ('am_link','am_link_b','am_link_h','am_link_ex') ORDER BY c.relname", []string{"relname", "oid", "amname", "amtype"}, []string{"NAME", "OID", "NAME", "CHAR"}, [][]any{{"am_link", int64(2), "heap", "t"}, {"am_link_b", int64(403), "btree", "i"}, {"am_link_ex", int64(783), "gist", "i"}, {"am_link_h", int64(405), "hash", "i"}})
	classes := fixture.Select("SELECT o.opcname,a.oid,a.amname FROM pg_opclass o JOIN pg_am a ON o.opcmethod=a.oid WHERE o.opcnamespace=11 AND ((o.opcname='int4_ops' AND o.opcmethod IN (403,405)) OR (o.opcname IN ('inet_ops','range_ops') AND o.opcmethod=783)) ORDER BY o.opcname,a.oid", []string{"opcname", "oid", "amname"}, []string{"NAME", "OID", "NAME"}, [][]any{{"inet_ops", int64(783), "gist"}, {"int4_ops", int64(403), "btree"}, {"int4_ops", int64(405), "hash"}, {"range_ops", int64(783), "gist"}})
	for _, s := range []fixture.Step{links, classes} {
		fixture.PgDescriptionRows(t, p, b, s)
		fixture.PgDescriptionRows(t, p, b, fixture.Prepared(s))
	}
	for _, q := range []string{"DROP INDEX am_link_b", "DROP INDEX am_link_h", "DROP TABLE am_link_g", "DROP TABLE am_link"} {
		run(fixture.Exec(q, 0))
	}
	pgAMInventory(t, p, b)
}

func TestReferencePGAMLinkage(t *testing.T) { pgAMScenario(t, pgAMLinkage) }
