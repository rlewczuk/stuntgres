package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func userOperatorReplayGraphSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, earlyNegator bool) {
	t.Helper()
	userOperatorExec(t, p, b, c, "CREATE SCHEMA userop_review_graph")
	d := "CREATE OPERATOR userop_review_graph.#!# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.int4ne,NEGATOR=OPERATOR(userop_review_graph.#?#))"
	a := "CREATE OPERATOR userop_review_graph.#~# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.int4eq,COMMUTATOR=OPERATOR(userop_review_graph.#@#))"
	if earlyNegator {
		userOperatorExec(t, p, b, c, d, a)
	} else {
		userOperatorExec(t, p, b, c, a, d)
	}
	userOperatorExec(t, p, b, c,
		"CREATE OPERATOR userop_review_graph.#@# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.int4eq)",
		"CREATE OPERATOR userop_review_graph.#?# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.int4eq,COMMUTATOR=OPERATOR(userop_review_graph.#@#))")
	userOperatorExec(t, p, b, c,
		"CREATE ROLE userop_graph_peer",
		"CREATE ROLE userop_graph_shell",
		"CREATE OPERATOR userop_review_graph.#^^# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.int4eq,COMMUTATOR=OPERATOR(userop_review_graph.#&&#),NEGATOR=OPERATOR(userop_review_graph.#&&#))",
		"CREATE OPERATOR userop_review_graph.#&&# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.int4ne,COMMUTATOR=OPERATOR(userop_review_graph.#^^#),NEGATOR=OPERATOR(userop_review_graph.#^^#))",
		"CREATE OPERATOR userop_review_graph.#||# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.int4eq,COMMUTATOR=OPERATOR(userop_review_graph.#||#),NEGATOR=OPERATOR(userop_review_graph.#!!#))",
		"CREATE OPERATOR userop_review_graph.#!!# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.int4ne,NEGATOR=OPERATOR(userop_review_graph.#||#))",
		"CREATE OPERATOR userop_review_graph.#~~# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.int4eq,COMMUTATOR=OPERATOR(userop_review_graph.#%%#))",
		"CREATE OPERATOR userop_review_graph.#@@# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.int4ne,NEGATOR=OPERATOR(userop_review_graph.#%%#))",
		"ALTER OPERATOR userop_review_graph.#&&# (int4,int4) OWNER TO userop_graph_peer",
		"ALTER OPERATOR userop_review_graph.#%%# (int4,int4) OWNER TO userop_graph_shell",
		"CREATE VIEW userop_review_graph.consumer AS SELECT 2 OPERATOR(userop_review_graph.#^^#) 2 AS v")
}

func userOperatorReplayGraphCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, tc := range []struct {
		name, commutator, negator string
		code                      int64
		result                    bool
	}{
		{"#!#", "", "#?#", 144, false},
		{"#?#", "#@#", "", 65, true},
		{"#~#", "#@#", "", 65, true},
		{"#@#", "#?#", "", 65, true},
	} {
		comm, neg := "0", "0"
		if tc.commutator != "" {
			comm = "'userop_review_graph." + tc.commutator + "(int4,int4)'::regoperator::oid"
		}
		if tc.negator != "" {
			neg = "'userop_review_graph." + tc.negator + "(int4,int4)'::regoperator::oid"
		}
		q := "SELECT oprcom=" + comm + " AND oprnegate=" + neg + " AND oprcode::oid=$1::oid AND oprleft=23 AND oprright=23 AND oprresult=16 AND oprkind='b' AND NOT oprcanhash AND NOT oprcanmerge AND oprrest::oid=0 AND oprjoin::oid=0 AND oprowner=(SELECT oid FROM pg_roles WHERE rolname=current_user) AS v FROM pg_operator WHERE oid='userop_review_graph." + tc.name + "(int4,int4)'::regoperator"
		userOperatorValue(t, p, b, c, q, "BOOL", true, tc.code)
		userOperatorValue(t, p, b, c, "SELECT 2 OPERATOR(userop_review_graph."+tc.name+") 2 AS v", "BOOL", tc.result)
	}
	for _, tc := range []struct {
		name, commutator, negator, owner string
		code                             int64
		result                           bool
	}{
		{"#^^#", "#&&#", "#&&#", "", 65, true},
		{"#&&#", "#^^#", "#^^#", "userop_graph_peer", 144, false},
		{"#||#", "#||#", "#!!#", "", 65, true},
		{"#!!#", "", "#||#", "", 144, false},
		{"#~~#", "#%%#", "", "", 65, true},
		{"#@@#", "", "#%%#", "", 144, false},
		{"#%%#", "#~~#", "#@@#", "userop_graph_shell", 0, false},
	} {
		comm, neg := "0", "0"
		if tc.commutator != "" {
			comm = "'userop_review_graph." + tc.commutator + "(int4,int4)'::regoperator::oid"
		}
		if tc.negator != "" {
			neg = "'userop_review_graph." + tc.negator + "(int4,int4)'::regoperator::oid"
		}
		owner := "current_user"
		if tc.owner != "" {
			owner = "'" + tc.owner + "'"
		}
		result := "16"
		if tc.code == 0 {
			result = "0"
		}
		q := "SELECT oprcom=" + comm + " AND oprnegate=" + neg + " AND oprcode::oid=$1::oid AND oprleft=23 AND oprright=23 AND oprresult=" + result + " AND oprkind='b' AND NOT oprcanhash AND NOT oprcanmerge AND oprrest::oid=0 AND oprjoin::oid=0 AND oprowner=(SELECT oid FROM pg_roles WHERE rolname=" + owner + ") AS v FROM pg_operator WHERE oid='userop_review_graph." + tc.name + "(int4,int4)'::regoperator"
		userOperatorValue(t, p, b, c, q, "BOOL", true, tc.code)
		if tc.code != 0 {
			userOperatorValue(t, p, b, c, "SELECT 2 OPERATOR(userop_review_graph."+tc.name+") 2 AS v", "BOOL", tc.result)
		}
	}
	userOperatorValue(t, p, b, c, "SELECT v FROM userop_review_graph.consumer", "BOOL", true)
}

func TestReferenceUserOperatorReplayGraph(t *testing.T) {
	for _, early := range []bool{true, false} {
		name := "negator_shell_early"
		if !early {
			name = "commutator_shell_early"
		}
		t.Run(name, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"userop_graph_peer", "userop_graph_shell"})
			fixture.SchemaReferenceCleanup(t, "userop_review_graph")
			p := fixture.NewScenario(t)
			var emitted []byte
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					userOperatorReplayGraphSetup(t, p, b, c, early)
					userOperatorReplayGraphCheck(t, p, b, c)
					if b.PG {
						if len(emitted) == 0 {
							t.Fatal("actual native graph dump is required")
						}
						userOperatorExec(t, p, b, c, "DROP SCHEMA userop_review_graph CASCADE")
						if _, err = c.ExecContext(p.Ctx, string(emitted)); err != nil {
							t.Fatal("actual native graph SQL replay", err)
						}
						userOperatorReplayGraphCheck(t, p, b, c)
						return
					}
					var sourceOID int64
					if err = c.QueryRowContext(p.Ctx, "SELECT 'userop_review_graph.#!#(int4,int4)'::regoperator::oid").Scan(&sourceOID); err != nil {
						t.Fatal(err)
					}
					var dump bytes.Buffer
					if err = stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
						t.Fatal(err)
					}
					emitted = bytes.Clone(dump.Bytes())
					if err = stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					if err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
						t.Fatal("native graph replay", err)
					}
					userOperatorReplayGraphCheck(t, p, b, c)
					var redump bytes.Buffer
					if err = stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(emitted, redump.Bytes()) {
						t.Fatal("equivalent graph redump changed")
					}
					fresh := fixture.NewFixture(t, false, true)
					fc, err := fresh.Memory.Conn(fresh.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer fc.Close()
					fb := fixture.Backend{DB: fresh.Memory, Recorder: fresh.MemoryRecord}
					userOperatorExec(t, fresh, fb, fc,
						"CREATE ROLE userop_graph_peer", "CREATE ROLE userop_graph_shell",
						"CREATE TEMP TABLE graph_collision(i integer)",
						"CREATE OPERATOR public.#/# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4mi)")
					if err = stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(emitted)); err != nil {
						t.Fatal("fresh graph replay", err)
					}
					userOperatorReplayGraphCheck(t, fresh, fb, fc)
					var freshOID int64
					if err = fc.QueryRowContext(fresh.Ctx, "SELECT 'userop_review_graph.#!#(int4,int4)'::regoperator::oid").Scan(&freshOID); err != nil {
						t.Fatal(err)
					}
					if freshOID == sourceOID {
						t.Fatal("fresh graph fixture did not perturb operator identity")
					}
					redump.Reset()
					if err = stuntgres.Dump(fresh.Ctx, fresh.Native, &redump); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(emitted, redump.Bytes()) {
						t.Fatal("fresh equivalent graph redump changed")
					}
				})
			}
		})
	}
}

func TestReferenceUserOperatorOrderedArgumentValidation(t *testing.T) {
	userOperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c, "CREATE TABLE userop_ordered_sentinel(v integer)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO userop_ordered_sentinel VALUES(42)", 1))
		for _, options := range []string{
			"LEFTARG=SETOF int4,LEFTARG=int4,RIGHTARG=int4",
			"LEFTARG=int4,RIGHTARG=SETOF int4,RIGHTARG=int4",
		} {
			t.Run(options, func(t *testing.T) {
				q := "CREATE OPERATOR public.#%# (" + options + ",FUNCTION=pg_catalog.int4pl,COMMUTATOR=OPERATOR(public.#@#))"
				userOperatorError(t, p, b, c, q, "42P13", "SETOF type not allowed for operator argument", "", "", "", false)
				userOperatorValue(t, p, b, c, "SELECT count(*) AS v FROM pg_operator WHERE oprnamespace='public'::regnamespace AND oprname IN ('#%#','#@#')", "INT8", int64(0))
				userOperatorValue(t, p, b, c, "SELECT v FROM userop_ordered_sentinel", "INT4", int64(42))
			})
		}
		userOperatorExec(t, p, b, c, "CREATE OPERATOR public.#%# (LEFTARG=int8,LEFTARG=int4,RIGHTARG=int8,RIGHTARG=int4,FUNCTION=pg_catalog.int4pl)")
		userOperatorValue(t, p, b, c, "SELECT 2 OPERATOR(public.#%#) 3 AS v", "INT4", int64(5))
		userOperatorValue(t, p, b, c, "SELECT oprleft=23 AND oprright=23 AND oprcode::oid=177 AND oprresult=23 AS v FROM pg_operator WHERE oid='public.#%#(int4,int4)'::regoperator", "BOOL", true)
	})
}
