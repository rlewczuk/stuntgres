package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePartitionDetachConcurrentOperatorFamilies(t *testing.T) {
	for _, family := range []string{"pattern_varchar", "custom_integer"} {
		for _, strong := range []bool{false, true} {
			t.Run(family+"/"+map[bool]string{false: "generated", true: "stronger"}[strong], func(t *testing.T) {
				detachConcurrentScenario(t, func(p *fixture.Pair, b fixture.Backend) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					definition, key, bound, value, outside := "k VARCHAR(8) COLLATE \"C\"", "k pg_catalog.text_pattern_ops", "FROM('b') TO('d')", "'c'", "'a'"
					stronger := `k IS NOT NULL AND k OPERATOR(pg_catalog.~>=~) 'c'::TEXT AND k OPERATOR(pg_catalog.~<~) 'd'::TEXT`
					if family == "custom_integer" {
						run(fixture.Exec("CREATE OPERATOR public.## (LEFTARG=integer,RIGHTARG=integer,FUNCTION=pg_catalog.int4ge)", 0))
						run(fixture.Exec("CREATE OPERATOR CLASS public.dc_family_class FOR TYPE integer USING btree AS OPERATOR 1 pg_catalog.<,OPERATOR 2 pg_catalog.<=,OPERATOR 3 pg_catalog.=,OPERATOR 4 public.##,OPERATOR 5 pg_catalog.>,FUNCTION 1 pg_catalog.btint4cmp(integer,integer)", 0))
						definition, key, bound, value, outside = "k INT", "k public.dc_family_class", "FROM(1) TO(4)", "2", "0"
						stronger = "k IS NOT NULL AND k OPERATOR(public.##) 2 AND k<4"
					}
					run(fixture.Exec("CREATE TABLE dc_family_parent("+definition+") PARTITION BY RANGE("+key+")", 0))
					run(fixture.Exec("CREATE TABLE dc_family_child PARTITION OF dc_family_parent FOR VALUES "+bound, 0))
					name := "dc_family_child_k_check"
					if strong {
						name = "dc_family_strong"
						run(fixture.Exec("ALTER TABLE dc_family_child ADD CONSTRAINT dc_family_strong CHECK("+stronger+")", 0))
					}
					run(fixture.Exec("INSERT INTO dc_family_parent VALUES("+value+")", 1))
					detachConcurrentExecute(t, p, b, b.DB, "ALTER TABLE dc_family_parent DETACH PARTITION dc_family_child CONCURRENTLY", true)
					witness := func(pair *fixture.Pair, backend fixture.Backend) {
						r := func(s fixture.Step) { fixture.RunStep(t, pair.Ctx, backend.DB, backend.Recorder, s) }
						r(detachConcurrentGraph("dc_family_parent", "dc_family_child", false, false))
						r(fixture.Select("SELECT count(*) FROM pg_constraint WHERE conrelid='dc_family_child'::regclass AND contype='c' AND convalidated AND conenforced AND conislocal", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
						rows := [][]any{{"c"}}
						typ := "VARCHAR"
						bad := "a"
						if family == "custom_integer" {
							rows = [][]any{{int64(2)}}
							typ = "INT4"
							bad = "0"
						}
						r(fixture.Select("SELECT k FROM dc_family_child", []string{"k"}, []string{typ}, rows))
						r(fixture.ImplicitError("INSERT INTO dc_family_child VALUES("+outside+")", fixture.Diagnostic{Code: "23514", Message: `new row for relation "dc_family_child" violates check constraint "` + name + `"`, Detail: "Failing row contains (" + bad + ").", Schema: "public", Table: "dc_family_child", Constraint: name}))
						if family == "custom_integer" {
							r(fixture.Select("SELECT EXISTS(SELECT 1 FROM pg_depend WHERE classid=2606 AND objid IN(SELECT oid FROM pg_constraint WHERE conrelid='dc_family_child'::regclass AND contype='c') AND refclassid=2617 AND refobjid='public.##(integer,integer)'::regoperator::OID AND deptype='n') AS retained", []string{"retained"}, []string{"BOOL"}, [][]any{{true}}))
						}
					}
					witness(p, b)
					if !b.PG {
						var dump bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
							t.Fatal(err)
						}
						fresh := fixture.NewFixture(t, false, true)
						if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(dump.Bytes())); err != nil {
							t.Fatal(err)
						}
						witness(fresh, fixture.Backend{DB: fresh.Memory, Recorder: fresh.MemoryRecord})
					}
				})
			})
		}
	}
}
