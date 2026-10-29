package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

// Exact single-input diagnostics complement the private real-worker overlap
// witness. Bags include PG planner seed input plus one input per executor state,
// without fixing launched workers or cross-worker warning order.
func TestReferenceAggregateParallelACLNotices(t *testing.T) {
	aggregateCombineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ *sql.Conn) {
		grantor := "postgres"
		if b.PG {
			grantor = "stuntgres" // The pinned fixture's bootstrap role is ID 10.
		}
		var notices []fixture.Diagnostic
		_, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, recorder, s) }
		warning := func(position, context string) fixture.Diagnostic {
			return fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "0L000", Message: "defaulting grantor to user ID 10", Position: position, Context: context}
		}
		exact := func(want ...fixture.Diagnostic) {
			t.Helper()
			if len(notices) != len(want) || (len(want) != 0 && !reflect.DeepEqual(notices, want)) {
				t.Fatalf("notices=%#v want %#v", notices, want)
			}
			notices = nil
		}
		// PG prepagg.c inputs INITCOND once per planning pass, in addition
		// to executor input: serial has one state, Partial/Finalize two
		// leader states and zero to two worker states. Native has no planner
		// input and partial execution has two partitions plus one leader.
		seedBag := func(allowSerial, allowPartial bool, minPlans, maxPlans int) {
			t.Helper()
			base, workers := 0, 0
			for _, notice := range notices {
				want := warning("", "")
				if b.PG && notice.Context == "parallel worker" {
					want.Context = "parallel worker"
				}
				if !reflect.DeepEqual(notice, want) {
					t.Fatalf("ACL seed diagnostic=%#v want %#v", notice, want)
				}
				if notice.Context == "parallel worker" {
					workers++
				} else {
					base++
				}
			}
			valid := false
			for plans := minPlans; plans <= maxPlans; plans++ {
				serial := base == plans+1 && workers == 0
				partial := base == 3 && workers == 0
				if b.PG {
					partial = base == plans+2 && workers <= 2
				}
				valid = valid || allowSerial && serial || allowPartial && partial
			}
			if !valid {
				t.Fatalf("ACL seed multiplicity: base=%d workers=%d; planning=%d..%d allowed serial=%v partial=%v; notices=%#v", base, workers, minPlans, maxPlans, allowSerial, allowPartial, notices)
			}
			notices = nil
		}
		run(fixture.Exec("SET client_min_messages=warning", 0))
		for _, step := range []fixture.Step{
			fixture.Exec("CREATE TABLE ac_acl_warning_input(id integer,padding text)", 0),
			fixture.Exec("INSERT INTO ac_acl_warning_input SELECT generate_series(1,16384),repeat('x',1024)", 16384),
			fixture.Exec("CREATE FUNCTION ac_acl_warning_step(aclitem,aclitem) RETURNS aclitem LANGUAGE SQL AS $$SELECT $1$$", 0),
			fixture.Exec("CREATE FUNCTION ac_acl_warning_merge(aclitem,aclitem) RETURNS aclitem LANGUAGE SQL AS $$SELECT coalesce($1,$2)$$", 0),
			fixture.Exec("CREATE FUNCTION ac_acl_warning_final(aclitem) RETURNS text LANGUAGE SQL AS $$SELECT $1::text$$", 0),
		} {
			run(step)
		}
		exact()
		// PG18.6 DefineAggregate validates this non-pseudotype INITCOND once,
		// and stores text for a fresh interpretation at execution time.
		run(fixture.Exec("CREATE AGGREGATE ac_acl_warning_agg(aclitem) (SFUNC=ac_acl_warning_step,STYPE=aclitem,COMBINEFUNC=ac_acl_warning_merge,FINALFUNC=ac_acl_warning_final,INITCOND='=r',PARALLEL=SAFE)", 0))
		exact(warning("", ""))
		badSeed := fixture.ErrorDiagnostic("CREATE AGGREGATE ac_acl_warning_bad(aclitem) (SFUNC=ac_acl_warning_step,STYPE=aclitem,COMBINEFUNC=ac_acl_warning_merge,INITCOND='=r!',PARALLEL=SAFE)", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: "extra garbage at the end of the ACL specification"})
		badSeed.ErrorStage = "execute"
		for _, step := range []fixture.Step{badSeed, fixture.Prepared(badSeed)} {
			run(step)
			exact(warning("", ""))
			run(fixture.Select("SELECT count(*) AS n FROM pg_proc WHERE proname='ac_acl_warning_bad'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			exact()
		}
		if b.PG {
			for _, setting := range []string{"min_parallel_table_scan_size=0", "parallel_setup_cost=0", "parallel_tuple_cost=0"} {
				run(fixture.Exec("SET "+setting, 0))
			}
		}
		// The explicitly granted ACL parameter demands the ordinary operation's
		// immutable role/authority capture without generating its own warning.
		q := "SELECT ac_acl_warning_agg($1::aclitem) FILTER(WHERE id<=4) AS v FROM ac_acl_warning_input WHERE id<=$2"
		explicit := "=w/" + grantor
		run(fixture.Exec("SET max_parallel_workers_per_gather=2", 0))
		if b.PG {
			aggregateOwnershipPartialPlan(t, p, c, q, explicit, 16384)
			// prepagg inputs once while planning; ExecutorStart initializes
			// two leader AggStates. EXPLAIN does not run Gather or workers.
			exact(warning("", ""), warning("", ""), warning("", ""))
		}
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		exact()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		freshPlans := 0
		if b.PG {
			freshPlans = 1
		}
		heldRuns := 0
		for _, ceiling := range []int{2, 0, 2} {
			run(fixture.Exec(fmt.Sprintf("SET max_parallel_workers_per_gather=%d", ceiling), 0))
			exact()
			for _, limit := range []int{16384, 0, 16384} {
				step := aggregateFinalText(q, "=r/"+grantor, explicit, limit)
				step.CheckMetadata = aggregateFinalMetadata(b.PG, false)
				allowSerial, allowPartial := true, ceiling != 0
				if b.PG && ceiling == 2 && limit == 16384 {
					// This query and these parameters have the matching actual
					// Partial/Finalize witness above. Empty input may custom-plan
					// serially; its finite serial-or-partial union stays explicit.
					allowSerial = false
				}
				run(step)
				seedBag(allowSerial, allowPartial, freshPlans, freshPlans)
				run(fixture.Prepared(step))
				seedBag(allowSerial, allowPartial, freshPlans, freshPlans)
				fixture.RunStep(t, p.Ctx, held, recorder, step)
				heldRuns++
				if !b.PG {
					// Native consults the current ceiling on every execution.
					seedBag(true, ceiling != 0, 0, 0)
				} else if heldRuns <= 5 {
					// Default auto policy builds one custom plan for each of
					// the first five executions of this parameterized handle.
					seedBag(allowSerial, allowPartial, 1, 1)
				} else {
					// Cached generic reuse has no planning input; a custom/new
					// generic plan has one, rejected generic plus custom two.
					// Retained topology need not follow the current ceiling or
					// the separate EXPLAIN witness.
					seedBag(true, true, 0, 2)
				}
			}
		}
		// Normal lexical input keeps its exact literal position and bind portal;
		// it must not inherit the last closed evaluator's private sink.
		literal := fixture.Select("SELECT '=r'::aclitem AS a", []string{"a"}, []string{"ACLITEM"}, [][]any{{"=r/" + grantor}})
		run(literal)
		exact(warning("8", ""))
		run(fixture.Prepared(literal))
		exact(warning("8", ""))
		literalStmt, err := c.PrepareContext(p.Ctx, literal.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer literalStmt.Close()
		exact(warning("8", ""))
		literalHeld := fixture.HeldStatement{Executor: c, Statement: literalStmt, SQL: literal.Query}
		for range 2 {
			fixture.RunStep(t, p.Ctx, literalHeld, recorder, literal)
			exact()
		}
		parameter := "SELECT $1::aclitem AS a"
		parameterStmt, err := c.PrepareContext(p.Ctx, parameter)
		if err != nil {
			t.Fatal(err)
		}
		defer parameterStmt.Close()
		exact()
		parameterHeld := fixture.HeldStatement{Executor: c, Statement: parameterStmt, SQL: parameter}
		value := fixture.Select(parameter, []string{"a"}, []string{"ACLITEM"}, [][]any{{"=r/" + grantor}}, "=r")
		portal := "unnamed portal parameter $1 = '...'"
		run(value)
		exact(warning("", portal))
		run(fixture.Prepared(value))
		exact(warning("", portal))
		for range 2 {
			fixture.RunStep(t, p.Ctx, parameterHeld, recorder, value)
			exact(warning("", portal))
		}
		bad := fixture.ErrorDiagnostic(parameter, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: "extra garbage at the end of the ACL specification", Context: portal}, "=r!")
		bad.SelectRows, bad.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, parameterHeld, recorder, bad)
		exact(warning("", portal))
		fixture.RunStep(t, p.Ctx, parameterHeld, recorder, value)
		exact(warning("", portal))
		run(fixture.Exec("SET client_min_messages=error", 0))
		step := aggregateFinalText(q, "=r/"+grantor, explicit, 16384)
		step.CheckMetadata = aggregateFinalMetadata(b.PG, false)
		fixture.RunStep(t, p.Ctx, held, recorder, step)
		exact()
		run(fixture.Exec("SET client_min_messages=warning", 0))
		fixture.RunStep(t, p.Ctx, parameterHeld, recorder, fixture.Select(parameter, []string{"a"}, []string{"ACLITEM"}, [][]any{{explicit}}, explicit))
		exact() // Neither the failed input nor the filtered seed leaves a residue.
		fixture.RunStep(t, p.Ctx, held, recorder, step)
		maxPlans := 0
		if b.PG {
			maxPlans = 2
		}
		seedBag(true, true, 0, maxPlans)
	})
}
