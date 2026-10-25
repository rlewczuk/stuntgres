package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceObjectGrantINETSubnetAuthority(t *testing.T) {
	for _, tc := range []struct {
		name, op, alias, sibling string
		oid                      int64
		want, siblingWant        bool
	}{
		{"network_sub", "<<", "#<#", "network_sup", 927, true, false},
		{"network_subeq", "<<=", "#<=#", "network_sup", 928, true, false},
		{"network_sup", ">>", "#>#", "network_sub", 929, false, true},
		{"network_supeq", ">>=", "#>=#", "network_sub", 930, false, true},
		{"network_overlap", "&&", "#&#", "network_sup", 3551, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
					fixture.RoleReferenceCleanup(t, []string{"og_object_reader", "og_object_other"})
					fixture.SchemaReferenceCleanup(t, "og_subnet")
					p := fixture.NewScenario(t)
					for _, b := range p.Backends() {
						t.Run(b.Name, func(t *testing.T) {
							c, err := b.DB.Conn(p.Ctx)
							if err != nil {
								t.Fatal(err)
							}
							defer c.Close()
							run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
							target := "pg_catalog." + tc.name + "(inet,inet)"
							defer objectGrantRoutineCleanup(t, p, c, []string{target})
							run(fixture.Exec("CREATE ROLE og_object_reader", 0))
							run(fixture.Exec("CREATE ROLE og_object_other", 0))
							run(fixture.Select("SELECT oid::regprocedure::text AS identity,proname,pronamespace::bigint AS namespace,pronargs,prorettype::bigint AS result,proisstrict,provolatile,proparallel FROM pg_proc WHERE oid=$1", []string{"identity", "proname", "namespace", "pronargs", "result", "proisstrict", "provolatile", "proparallel"}, []string{"TEXT", "NAME", "INT8", "INT2", "INT8", "BOOL", "CHAR", "CHAR"}, [][]any{{tc.name + "(inet,inet)", tc.name, int64(11), int64(2), int64(16), true, "i", "s"}}, tc.oid))
							run(fixture.Exec("CREATE SCHEMA og_subnet AUTHORIZATION og_object_other", 0))
							fixture.RoleGrantActor(run, "og_object_other", func() {
								run(fixture.Exec("CREATE TABLE og_subnet.source(id INTEGER,a INET,b CIDR,n INET)", 0))
								run(fixture.Exec("INSERT INTO og_subnet.source VALUES(1,'192.168.1.5','192.168.1/24',NULL)", 1))
								run(fixture.Exec("CREATE OPERATOR og_subnet."+tc.alias+" (LEFTARG=inet,RIGHTARG=inet,FUNCTION=pg_catalog."+tc.name+")", 0))
								run(fixture.Exec("CREATE VIEW og_subnet.saved AS SELECT a "+tc.op+" b AS v FROM og_subnet.source", 0))
								run(fixture.Exec("GRANT SELECT ON og_subnet.source,og_subnet.saved TO og_object_reader", 0))
							})
							run(fixture.Exec("GRANT USAGE ON SCHEMA og_subnet TO og_object_reader", 0))
							value := func(e, tail string, want any, args ...any) fixture.Step {
								return fixture.Select("SELECT "+e+" AS v FROM og_subnet.source"+tail, []string{"v"}, []string{"BOOL"}, [][]any{{want}}, args...)
							}
							cases := []objectGrantProviderCase{
								{value("a "+tc.op+" b", "", tc.want), true, true},
								{value("a OPERATOR(pg_catalog."+tc.op+") b", "", tc.want), true, true},
								{value("pg_catalog."+tc.name+"(a,b)", "", tc.want), true, true},
								{value("a OPERATOR(og_subnet."+tc.alias+") b", "", tc.want), true, true},
								{value("(CASE WHEN id=1 THEN $1::inet ELSE a END) "+tc.op+" b", "", tc.want, "192.168.1.5"), true, true},
								{value("n "+tc.op+" b", "", nil), true, true},
								{value("a "+tc.op+" n", "", nil), true, true},
								{fixture.Select("SELECT v FROM og_subnet.saved", []string{"v"}, []string{"BOOL"}, [][]any{{tc.want}}), true, true},
								{fixture.Select("SELECT INET '192.168.1.5' "+tc.op+" CIDR '192.168.1/24' AS v", []string{"v"}, []string{"BOOL"}, [][]any{{tc.want}}), true, false},
								{fixture.Select("SELECT NULL::inet "+tc.op+" INET '::/0' AS v", []string{"v"}, []string{"BOOL"}, [][]any{{nil}}), false, false},
								{value("CASE WHEN FALSE THEN a "+tc.op+" b ELSE NULL END", "", nil), false, true},
							}
							for _, tail := range []string{" WHERE FALSE", " WHERE id>99", " LIMIT 0"} {
								cases = append(cases, objectGrantProviderCase{fixture.Select("SELECT a "+tc.op+" b AS v FROM og_subnet.source"+tail, []string{"v"}, []string{"BOOL"}, nil), true, true})
							}
							controls := []fixture.Step{value("pg_catalog."+tc.sibling+"(a,b)", "", tc.siblingWant), fixture.Select("SELECT a FROM og_subnet.source", []string{"a"}, []string{"INET"}, [][]any{{"192.168.1.5"}})}
							objectGrantProviderLifecycle(t, p, b, c, run, prepared, target, "function", tc.name, cases, controls)
						})
					}
				})
			}
		})
	}
}
