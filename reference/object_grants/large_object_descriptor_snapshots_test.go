package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLargeObjectDescriptorSnapshots(t *testing.T) {
	largeObjectScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer largeObjectCleanup(t, p, c, 910182, 910183, 910184)
		peer, observe := descriptorPeer(t, p, b, prepared)
		create := largeObjectValue("lo_from_bytea(910182::oid,decode('616263646566','hex'))", "OID", int64(910182))
		run(create)
		run(fixture.Exec("BEGIN", 0))
		frozen := descriptorOpen(t, p, c, prepared, 910182, descriptorRead, 0)
		live := descriptorOpen(t, p, c, prepared, 910182, descriptorWrite, 1)
		writer := descriptorOpen(t, p, c, prepared, 910182, descriptorRead|descriptorWrite, 2)
		run(descriptorWriteStep(writer, []byte("UV")))
		after := descriptorOpen(t, p, c, prepared, 910182, descriptorRead|1, 3)
		run(descriptorWriteStep(writer, []byte("ZZ")))
		run(descriptorReadStep(frozen, 20, "abcdef"))
		run(descriptorReadStep(live, 20, "UVZZef"))
		run(descriptorReadStep(after, 20, "UVcdef"))
		run(descriptorReadStep(writer, 20, "ef"))
		run(fixture.Exec("ROLLBACK", 0))

		run(fixture.Exec("BEGIN", 0))
		frozen = descriptorOpen(t, p, c, prepared, 910182, descriptorRead, 0)
		before := descriptorOpen(t, p, c, prepared, 910182, descriptorWrite, 1)
		live = descriptorOpen(t, p, c, prepared, 910182, descriptorWrite|1, 2)
		observe(fixture.Exec("BEGIN", 0))
		pw := descriptorOpen(t, p, peer, prepared, 910182, descriptorWrite, 0)
		observe(descriptorWriteStep(pw, []byte("XY")))
		run(descriptorReadStep(before, 20, "abcdef"))
		observe(fixture.Exec("COMMIT", 0))
		run(descriptorReadStep(frozen, 20, "abcdef"))
		run(descriptorReadStep(live, 20, "XYcdef"))
		run(fixture.Exec("COMMIT", 0))

		// Establish RR with an ordinary query before the peer commits, not lo_open.
		run(largeObjectPut(910182, 0, "616263646566"))
		run(fixture.Exec("BEGIN ISOLATION LEVEL REPEATABLE READ", 0))
		run(largeObjectValue("1::integer", "INT4", int64(1)))
		observe(largeObjectPut(910182, 0, "5859"))
		frozen = descriptorOpen(t, p, c, prepared, 910182, descriptorRead, 0)
		live = descriptorOpen(t, p, c, prepared, 910182, descriptorWrite, 1)
		run(descriptorReadStep(frozen, 20, "abcdef"))
		run(descriptorReadStep(live, 20, "XYcdef"))
		run(fixture.Exec("ROLLBACK", 0))
		run(largeObjectPut(910182, 0, "616263646566"))
		run(fixture.Exec("BEGIN ISOLATION LEVEL REPEATABLE READ", 0))
		run(largeObjectValue("1::integer", "INT4", int64(1)))
		run(largeObjectPut(910182, 2, "5556"))
		after = descriptorOpen(t, p, c, prepared, 910182, descriptorRead, 0)
		run(descriptorReadStep(after, 20, "abUVef"))
		run(fixture.Exec("ROLLBACK", 0))
		observe(largeObjectPut(910182, 0, "5859"))
		run(fixture.Exec("BEGIN ISOLATION LEVEL READ COMMITTED", 0))
		fresh := descriptorOpen(t, p, c, prepared, 910182, descriptorRead, 0)
		run(descriptorReadStep(fresh, 20, "XYcdef"))
		run(fixture.Exec("COMMIT", 0))

		// Guarded FROM prevents inlining; its VOLATILE body gets a fresh query
		// frontier. The descriptor must outlive the body's scratch activation.
		run(fixture.Exec("CREATE TABLE og_descriptor_guard(x integer)", 0))
		run(fixture.Exec("INSERT INTO og_descriptor_guard VALUES(1)", 1))
		run(fixture.Exec("CREATE FUNCTION og_descriptor_open(oid) RETURNS integer LANGUAGE SQL VOLATILE AS 'SELECT lo_open($1,262144) FROM og_descriptor_guard'", 0))
		run(fixture.Exec("BEGIN", 0))
		run(largeObjectValue("og_descriptor_open(lo_from_bytea(910183::oid,decode('00ff','hex')))", "INT4", int64(0)))
		run(descriptorReadStep(0, 20, string([]byte{0,255})))
		descriptorRecover(run, largeObjectMissing("SELECT lo_open(lo_from_bytea(910184::oid,decode('aa','hex')),262144)", 910184))
		descriptorRecover(run, largeObjectMissing("SELECT og_descriptor_open(lo_from_bytea(910184::oid,decode('aa','hex'))) AS body_open,lo_open(910184::oid,262144) AS outer_open", 910184))
		descriptorRecover(run, largeObjectMissing("SELECT lo_get(910184::oid)", 910184))
		run(fixture.Exec("COMMIT", 0))

		// Fresh VOLATILE activations still honor an already-established RR base.
		run(fixture.Exec("BEGIN ISOLATION LEVEL REPEATABLE READ", 0))
		run(largeObjectValue("1::integer", "INT4", int64(1)))
		observe(largeObjectPut(910182, 0, "5556"))
		run(largeObjectValue("og_descriptor_open(910182::oid)", "INT4", int64(0)))
		run(descriptorReadStep(0, 20, "XYcdef"))
		live = descriptorOpen(t, p, c, prepared, 910182, descriptorWrite, 1)
		run(descriptorReadStep(live, 20, "UVcdef"))
		run(fixture.Exec("ROLLBACK", 0))
	})
}
