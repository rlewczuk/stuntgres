package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// REL_18_6 executor/functions.c retains the caller's active snapshot for
// read-only (STABLE) SQL bodies, but advances the command frontier for VOLATILE
// bodies. inv_api.c opens INV_READ against that active snapshot. Guarded FROM
// bodies prevent scalar inlining from erasing the activation being exercised.
func TestReferenceLargeObjectDescriptorRoutineFrontiers(t *testing.T) {
	for _, isolation := range []string{"READ COMMITTED", "REPEATABLE READ"} {
		t.Run(isolation, func(t *testing.T) {
			largeObjectScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				defer largeObjectCleanup(t, p, c, 910182, 910183, 910184)
				_, observe := descriptorPeer(t, p, b, prepared)
				run(fixture.Exec("CREATE TABLE og_descriptor_frontier_guard(x integer)", 0))
				run(fixture.Exec("INSERT INTO og_descriptor_frontier_guard VALUES(1)", 1))
				for _, volatility := range []string{"STABLE", "VOLATILE"} {
					name := map[string]string{"STABLE": "og_descriptor_open_stable", "VOLATILE": "og_descriptor_open_volatile"}[volatility]
					run(fixture.Exec("CREATE FUNCTION "+name+"(oid) RETURNS integer LANGUAGE SQL "+volatility+" AS 'SELECT lo_open($1,262144) FROM og_descriptor_frontier_guard'", 0))
				}
				run(largeObjectValue("lo_from_bytea(910182::oid,decode('aa00','hex'))", "OID", int64(910182)))

				// STABLE must not reconstruct its inherited opening frontier from
				// the journal after evaluating an argument that creates an object.
				run(fixture.Exec("BEGIN ISOLATION LEVEL "+isolation, 0))
				descriptorRecover(run, descriptorRoutineMissing("SELECT og_descriptor_open_stable(lo_from_bytea(910183::oid,decode('ccdd','hex')))", "og_descriptor_open_stable", 910183))
				run(fixture.Select("SELECT count(*) AS v FROM pg_largeobject_metadata WHERE oid=910183::oid", []string{"v"}, []string{"INT8"}, [][]any{{int64(0)}}))
				fd := descriptorOpen(t, p, c, prepared, 910182, descriptorRead, 0)
				run(descriptorReadStep(fd, 20, string([]byte{0xaa, 0x00})))
				run(descriptorClose(fd))
				// VOLATILE advances only its own body frontier. Its returned
				// descriptor survives activation teardown and reads created bytes.
				run(largeObjectValue("og_descriptor_open_volatile(lo_from_bytea(910183::oid,decode('ccdd','hex')))", "INT4", int64(0)))
				run(descriptorReadStep(0, 20, string([]byte{0xcc, 0xdd})))
				run(descriptorClose(0))
				// Success must restore the outer opening frontier: the later
				// direct open fails, rolling the argument creation back too.
				descriptorRecover(run, largeObjectMissing("SELECT lo_open(CASE WHEN og_descriptor_open_volatile(lo_from_bytea(910184::oid,decode('eeff','hex')))=0 THEN 910184::oid ELSE 0::oid END,262144)", 910184))
				run(fixture.Select("SELECT count(*) AS v FROM pg_largeobject_metadata WHERE oid=910184::oid", []string{"v"}, []string{"INT8"}, [][]any{{int64(0)}}))
				run(fixture.Exec("ROLLBACK", 0))

				for _, tc := range []struct {
					name string
					body string
				}{
					{"og_descriptor_open_stable", string([]byte{0xdd, 0x00})},
					{"og_descriptor_open_volatile", string([]byte{0xbb, 0xff})},
				} {
					run(fixture.Exec("BEGIN ISOLATION LEVEL "+isolation, 0))
					// A previous command's own write belongs to both frontiers.
					run(largeObjectPut(910182, 0, "dd00"))
					updated := "CASE WHEN lo_put(910182::oid,0::bigint,decode('bbff','hex')) IS NULL THEN 0::oid ELSE 910182::oid END"
					// Make the outer open depend on the body result, not on
					// target-list evaluation order. The body takes slot 0, then
					// the outer open takes slot 1 with its restored old frontier.
					run(largeObjectValue("lo_open(CASE WHEN "+tc.name+"("+updated+")=0 THEN 910182::oid ELSE 0::oid END,262144)", "INT4", int64(1)))
					run(descriptorReadStep(0, 20, tc.body))
					run(descriptorReadStep(1, 20, string([]byte{0xdd, 0x00})))
					run(descriptorClose(0))
					run(descriptorClose(1))
					// Both kinds of argument really wrote; only READ opening
					// visibility differs, not persistence of the mutation.
					run(largeObjectBytes(910182, "bbff"))
					run(fixture.Exec("ROLLBACK", 0))
				}

				// A failing body must unwind its fresh activation and argument
				// writes as well. Recover without leaving the outer transaction.
				run(fixture.Exec("BEGIN ISOLATION LEVEL "+isolation, 0))
				missing := "CASE WHEN lo_put(910182::oid,0::bigint,decode('bbff','hex')) IS NULL THEN 0::oid ELSE 910184::oid END"
				descriptorRecover(run, descriptorRoutineMissing("SELECT og_descriptor_open_volatile("+missing+")", "og_descriptor_open_volatile", 910184))
				run(largeObjectValue("og_descriptor_open_stable(910182::oid)", "INT4", int64(0)))
				run(descriptorReadStep(0, 20, string([]byte{0xaa, 0x00})))
				run(descriptorClose(0))
				run(largeObjectBytes(910182, "aa00"))
				run(fixture.Exec("ROLLBACK", 0))

				if isolation == "REPEATABLE READ" {
					// A fresh VOLATILE own-write frontier must not replace the
					// retained committed base. Avoid writing the peer-modified
					// object here: that would add an unrelated heap SSI conflict.
					run(fixture.Exec("BEGIN ISOLATION LEVEL REPEATABLE READ", 0))
					run(largeObjectValue("1::integer", "INT4", int64(1)))
					observe(largeObjectPut(910182, 0, "bbff"))
					run(largeObjectValue("og_descriptor_open_volatile(lo_from_bytea(910183::oid,decode('ccdd','hex')))", "INT4", int64(0)))
					run(descriptorReadStep(0, 20, string([]byte{0xcc, 0xdd})))
					run(largeObjectValue("og_descriptor_open_volatile(910182::oid)", "INT4", int64(1)))
					run(descriptorReadStep(1, 20, string([]byte{0xaa, 0x00})))
					live := descriptorOpen(t, p, c, prepared, 910182, descriptorWrite, 2)
					run(descriptorReadStep(live, 20, string([]byte{0xbb, 0xff})))
					run(fixture.Exec("ROLLBACK", 0))
				}
			})
		})
	}
}

func descriptorRoutineMissing(query, routine string, oid uint32) fixture.Step {
	// Full portable diagnostic; the statement-1 Context follows the existing
	// guarded SQL-body convention. Root must confirm it with pinned PG18.6
	// discovery, rather than treating this source-derived expectation as observed.
	s := fixture.ImplicitError(query, fixture.Diagnostic{
		Code:    "42704",
		Message: fmt.Sprintf("large object %d does not exist", oid),
		Context: `SQL function "` + routine + `" statement 1`,
	})
	s.SelectRows = true
	s.ErrorStage = "execute"
	return s
}
