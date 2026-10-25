package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRoutineAggregateDirectLiterals(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ad_literal_owner", "ad_literal_reader", "ad_literal_default_reader"})
	for _, authority := range []bool{true, false} {
		name := "default-acl-persistence"
		if authority {
			name = "support-denied-authority"
		}
		t.Run(name, func(t *testing.T) { aggregateDirectLiteralScenario(t, authority) })
	}
}

func aggregateDirectLiteralScenario(t *testing.T, authority bool) {
	t.Helper()
	aggregateDirectScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		aggregateDirectSetup(t, p, b, c)
		exec := func(q ...string) { aggregateDirectExec(t, p, b, c, q...) }
		reader := "ad_literal_default_reader"
		if authority {
			reader = "ad_literal_reader"
			exec("CREATE ROLE ad_literal_owner", "CREATE ROLE ad_literal_reader",
				"GRANT USAGE,CREATE ON SCHEMA public TO ad_literal_owner",
				"GRANT USAGE ON SCHEMA public TO ad_literal_reader",
				"GRANT SELECT ON ad_input TO ad_literal_reader",
				"REVOKE EXECUTE ON FUNCTION ad_s(integer,integer),ad_f(integer) FROM PUBLIC",
				"GRANT EXECUTE ON FUNCTION ad_s(integer,integer),ad_f(integer) TO ad_literal_owner",
				"ALTER AGGREGATE ad_a(integer) OWNER TO ad_literal_owner")
		} else {
			// Relation/schema ACL serialization is a separate capability. This
			// scenario starts with actual defaults, not a lossy ACL reset at Dump.
			exec("CREATE ROLE ad_literal_default_reader")
		}
		exec("CREATE FUNCTION ad_literal_procedure() RETURNS regprocedure BEGIN ATOMIC SELECT CASE WHEN ad_a(v)=30 THEN 'ad_s(integer,integer)'::regprocedure ELSE NULL::regprocedure END FROM ad_input; END",
			"CREATE FUNCTION ad_literal_proc() RETURNS regproc BEGIN ATOMIC SELECT CASE WHEN ad_a(v)=30 THEN 'ad_s'::regproc ELSE NULL::regproc END FROM ad_input; END")
		original := aggregateDirectIDs(t, p, c)["public.ad_s"]
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		if authority {
			exec("SET SESSION AUTHORIZATION ad_literal_reader")
		}
		names := []string{"ad_literal_procedure", "ad_literal_proc"}
		held := make([]fixture.HeldStatement, len(names))
		prepare := func() {
			for i, name := range names {
				held[i] = aggregateDirectHeld(t, p, c, "SELECT "+name+"()::text AS v")
			}
		}
		prepare()
		check := func(support string) {
			createAggregateBoth(t, p, b, c, aggregateDirectBag([]string{"ad_a", "ad_literal_procedure", "ad_literal_proc"},
				[2]string{"ad_a", support}, [2]string{"ad_a", "ad_f"},
				[2]string{"ad_literal_procedure", "ad_a"}, [2]string{"ad_literal_procedure", support},
				[2]string{"ad_literal_proc", "ad_a"}, [2]string{"ad_literal_proc", support}))
			for i, name := range names {
				want := support
				if i == 0 {
					want += "(integer,integer)"
				}
				s := fixture.Select(held[i].SQL, []string{"v"}, []string{"TEXT"}, [][]any{{want}})
				createAggregateBoth(t, p, b, c, s)
				fixture.RunStep(t, p.Ctx, held[i], b.Recorder, s)
				// Relational identity survives replacement's new OIDs; neither
				// a same-name competitor nor rendered text is authority.
				createAggregateBoth(t, p, b, c, fixture.Select("SELECT "+name+"()::oid='"+support+"(integer,integer)'::regprocedure::oid AS original_identity", []string{"original_identity"}, []string{"BOOL"}, [][]any{{true}}))
			}
			createAggregateBoth(t, p, b, c, fixture.Select("SELECT has_function_privilege('"+reader+"','"+support+"(integer,integer)'::regprocedure::oid,'EXECUTE') AS transition_execute,has_function_privilege('"+reader+"','ad_f(integer)'::regprocedure::oid,'EXECUTE') AS final_execute", []string{"transition_execute", "final_execute"}, []string{"BOOL", "BOOL"}, [][]any{{!authority, !authority}}))
		}
		// In the authority scenario, both literals refer to the transition
		// address but invoke neither support as the support-denied reader.
		check("ad_s")
		exec("RESET SESSION AUTHORIZATION",
			"ALTER FUNCTION ad_s(integer,integer) RENAME TO ad_step",
			"CREATE FUNCTION ad_s(integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT 999$$")
		ids := aggregateDirectIDs(t, p, c)
		if ids["public.ad_step"] != original || ids["public.ad_s"] == original {
			t.Fatalf("literal reference identity changed: original=%d current=%v", original, ids)
		}
		if authority {
			exec("SET SESSION AUTHORIZATION ad_literal_reader")
		}
		check("ad_step")
		if authority || b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		prepare()
		check("ad_step")
	})
}
