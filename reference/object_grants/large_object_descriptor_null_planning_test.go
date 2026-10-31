package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// REL_18_6 optimizer/util/clauses.c simplifies strict scalar calls with a
// constant NULL after planning their children, even with volatile siblings.
// These expectations are independent of runtime NULL argument evaluation.
func TestReferenceLargeObjectDescriptorNullPlanning(t *testing.T) {
	largeObjectScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("CREATE SEQUENCE og_descriptor_null_steps START 41 CACHE 1", 0))
		const next = "nextval('og_descriptor_null_steps'::regclass)"
		for _, tc := range []struct {
			expression string
			typ        string
		}{
			{"lo_open(NULL::oid," + next + "::integer)", "INT4"},
			{"lo_open(" + next + "::oid,NULL::integer)", "INT4"},
			{"loread(NULL::integer," + next + "::integer)", "BYTEA"},
			{"loread(" + next + "::integer,NULL::integer)", "BYTEA"},
			{"lowrite(" + next + "::integer,NULL::bytea)", "INT4"},
			{"lowrite(NULL::integer,decode(" + next + "::text,'escape'))", "INT4"},
			// lo_close is unary: the strict arithmetic child supplies the
			// volatile sibling, and the resulting NULL must not close a slot.
			{"lo_close(NULL::integer)", "INT4"},
			{"lo_close((NULL::bigint+" + next + ")::integer)", "INT4"},
			// The removed family exemption also affected prerequisite callers.
			{"lo_get(NULL::oid," + next + ",1)", "BYTEA"},
			{"lo_get(" + next + "::oid,NULL::bigint,1)", "BYTEA"},
			{"lo_get(0::oid," + next + ",NULL::integer)", "BYTEA"},
			{"lo_put(NULL::oid," + next + ",decode('aa','hex'))", "VOID"},
			{"lo_put(" + next + "::oid,0::bigint,NULL::bytea)", "VOID"},
			{"lo_from_bytea(" + next + "::oid,NULL::bytea)", "OID"},
			{"lo_from_bytea(NULL::oid,decode(" + next + "::text,'escape'))", "OID"},
			{"lo_create((NULL::bigint+" + next + ")::oid)", "OID"},
			{"lo_creat((NULL::bigint+" + next + ")::integer)", "OID"},
			{"lo_unlink((NULL::bigint+" + next + ")::oid)", "INT4"},
			{"lo_get((NULL::bigint+" + next + ")::oid)", "BYTEA"},
		} {
			run(largeObjectValue(tc.expression, tc.typ, nil))
			run(fixture.Select("SELECT last_value,is_called FROM og_descriptor_null_steps", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(41), false}}))
		}
		// Prove the sequence was usable, rather than merely inaccessible to
		// eliminated calls. Its first real demand still returns START.
		run(largeObjectValue(next, "INT8", int64(41)))
		run(fixture.Select("SELECT last_value,is_called FROM og_descriptor_null_steps", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(41), true}}))

		// A stored row NULL is not known while planning. Its reached runtime
		// siblings must still execute, unlike the constant cases above.
		run(fixture.Exec("CREATE TABLE og_descriptor_runtime_null(o oid,n integer,b bytea,z integer)", 0))
		run(fixture.Exec("INSERT INTO og_descriptor_runtime_null VALUES(NULL,NULL,NULL,0)", 1))
		for i, tc := range []struct {
			expression string
			typ        string
		}{
			{"lo_open(o," + next + "::integer)", "INT4"},
			{"loread(n," + next + "::integer)", "BYTEA"},
			{"lowrite(" + next + "::integer,b)", "INT4"},
			{"lo_close(n+" + next + "::integer)", "INT4"},
		} {
			run(fixture.Select("SELECT "+tc.expression+" AS v FROM og_descriptor_runtime_null", []string{"v"}, []string{tc.typ}, [][]any{{nil}}))
			run(fixture.Select("SELECT last_value,is_called FROM og_descriptor_null_steps", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(42 + i), true}}))
		}
		// The same reached runtime demand also preserves row-dependent
		// arithmetic failures. Existing parameter-planning witnesses remain
		// in TestReferenceLargeObjectDescriptorDiagnostics.
		for _, expression := range []string{
			"lo_open(o,1/z)",
			"loread(n,1/z)",
			"lowrite(1/z,b)",
			"lo_close(n+(1/z))",
		} {
			run(largeObjectError("SELECT "+expression+" FROM og_descriptor_runtime_null", "22012", "division by zero"))
		}
	})
}
