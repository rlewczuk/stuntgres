package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLargeObjectNullAndDiagnostics(t *testing.T) {
	largeObjectScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer largeObjectCleanup(t, p, c, 910031, 910032, 910033)
		run(largeObjectValue("lo_from_bytea(910031::oid,decode('00ff','hex'))", "OID", int64(910031)))
		for _, tc := range []struct{ expression, typ string }{
			{"lo_create(NULL::oid)", "OID"},
			{"lo_creat(NULL::integer)", "OID"},
			{"lo_from_bytea(NULL::oid,decode('ff','hex'))", "OID"},
			{"lo_from_bytea(910032::oid,NULL::bytea)", "OID"},
			{"lo_get(NULL::oid)", "BYTEA"},
			{"lo_get(NULL::oid,-1::bigint,-1)", "BYTEA"},
			{"lo_get(910033::oid,NULL::bigint,-1)", "BYTEA"},
			{"lo_get(910033::oid,-1::bigint,NULL::integer)", "BYTEA"},
			{"lo_put(NULL::oid,-1::bigint,decode('ff','hex'))", "VOID"},
			{"lo_put(910031::oid,NULL::bigint,decode('ff','hex'))", "VOID"},
			{"lo_put(910031::oid,-1::bigint,NULL::bytea)", "VOID"},
			{"lo_unlink(NULL::oid)", "INT4"},
		} {
			run(largeObjectValue(tc.expression, tc.typ, nil))
		}
		run(largeObjectBytes(910031, "00ff"))
		run(largeObjectValue("lo_create(910032::oid)", "OID", int64(910032)))
		// Parameter-dependent expression prevents planner constant folding; strict
		// NULL suppresses the provider, not evaluation of other arguments.
		run(largeObjectError("SELECT lo_put(NULL::oid,(1/$1::integer)::bigint,decode('','hex'))", "22012", "division by zero", int64(0)))
		for _, q := range []string{"SELECT lo_get(910033::oid)", "SELECT lo_put(910033::oid,0::bigint,decode('','hex'))", "SELECT lo_unlink(910033::oid)"} {
			run(largeObjectMissing(q, 910033))
		}
		run(largeObjectError("SELECT lo_get(910033::oid,-1::bigint,-1)", "22023", "requested length cannot be negative"))
		run(largeObjectError("SELECT lo_get(910031::oid,-1::bigint,0)", "22023", "invalid large object seek target: -1"))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(largeObjectError("SELECT lo_get(910031::oid,-1::bigint,0)", "42501", "permission denied for large object 910031"))
			run(largeObjectError("SELECT lo_put(910031::oid,-1::bigint,decode('','hex'))", "42501", "permission denied for large object 910031"))
			run(largeObjectError("SELECT lo_get(910031::oid,-1::bigint,-1)", "22023", "requested length cannot be negative"))
		})
		for _, tc := range []struct{ expression, name string }{
			{"lo_create(910033::oid)", "lo_create"},
			{"lo_creat(-1)", "lo_creat"},
			{"lo_from_bytea(910033::oid,decode('ff','hex'))", "lo_from_bytea"},
			{"lo_put(910031::oid,0::bigint,decode('ff','hex'))", "lo_put"},
			{"lo_unlink(910031::oid)", "lo_unlink"},
		} {
			run(fixture.Exec("BEGIN READ ONLY", 0))
			run(largeObjectError("SELECT "+tc.expression, "25006", "cannot execute "+tc.name+"() in a read-only transaction"))
			run(fixture.Exec("ROLLBACK", 0))
		}
		run(fixture.Exec("BEGIN READ ONLY", 0))
		for _, tc := range []struct{ expression, typ string }{
			{"lo_create(NULL::oid)", "OID"}, {"lo_creat(NULL::integer)", "OID"},
			{"lo_from_bytea(910033::oid,NULL::bytea)", "OID"},
			{"lo_put(910031::oid,0::bigint,NULL::bytea)", "VOID"}, {"lo_unlink(NULL::oid)", "INT4"},
		} {
			run(largeObjectValue(tc.expression, tc.typ, nil))
		}
		run(fixture.Exec("COMMIT", 0))
		run(largeObjectBytes(910031, "00ff"))
		run(largeObjectValue("lo_create(910033::oid)", "OID", int64(910033)))
	})
}
