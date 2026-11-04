package reference_test

import (
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceStatisticsMCVItemsValues(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		s.run(fixture.Exec("CREATE TABLE mcv_t(a INT,b INT)", 0))
		s.run(fixture.Exec("CREATE STATISTICS mcv_s(mcv) ON a,b FROM mcv_t", 0))
		mcvItemsCheck(t, s, "mcv_s")
		s.run(fixture.Exec("ANALYZE mcv_t", 0))
		mcvItemsCheck(t, s, "mcv_s")
		s.run(fixture.Exec("INSERT INTO mcv_t VALUES(1,1),(1,1),(2,2),(NULL,NULL)", 4))
		mcvItemsCheck(t, s, "mcv_s")
		s.run(fixture.Exec("ANALYZE mcv_t", 0))
		items := mcvItemsRead(t, s.pair.Ctx, s, "mcv_s")
		mcvItemsAssert(t, items, mcvItemsFour...)
		if items[0].rawValues != "{1,1}" || items[0].frequency != .5 || items[0].baseline != .25 {
			t.Fatalf("most frequent pair at index zero: %#v", items)
		}
		stmt, err := s.conn.PrepareContext(s.pair.Ctx, mcvItemsQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, name := range []string{"mcv_s", "absent", "mcv_s"} {
			rows, err := stmt.QueryContext(s.pair.Ctx, name)
			if err != nil {
				t.Fatal(err)
			}
			want := mcvItemsFour
			if name == "absent" {
				want = nil
			}
			mcvItemsAssert(t, mcvItemsConsume(t, rows, s.pg), want...)
		}
	})
}

func TestReferenceStatisticsMCVItemsRefresh(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		mcvSetup(t, s)
		before := mcvItemsRead(t, s.pair.Ctx, s, "mcv_s")
		mcvItemsAssert(t, before, mcvItemsFour...)
		s.run(fixture.Exec("INSERT INTO mcv_t VALUES(2,2)", 1))
		stale := mcvItemsRead(t, s.pair.Ctx, s, "mcv_s")
		if !reflect.DeepEqual(before, stale) {
			t.Fatal("DML changed retained items", before, stale)
		}
		s.run(fixture.Exec("ANALYZE mcv_t", 0))
		mcvItemsCheck(t, s, "mcv_s", mcvItemsFive...)
		for _, q := range []string{
			"CREATE TABLE mcv_cap(a INT,b INT)",
			"CREATE STATISTICS mcv_cap_s(mcv) ON a,b FROM mcv_cap",
			"ALTER STATISTICS mcv_cap_s SET STATISTICS 1",
		} {
			s.run(fixture.Exec(q, 0))
		}
		s.run(fixture.Exec("INSERT INTO mcv_cap VALUES(1,1),(1,1),(1,1),(1,1),(1,2),(1,2),(1,2),(2,2),(2,2),(NULL,NULL)", 10))
		s.run(fixture.Exec("ANALYZE mcv_cap", 0))
		mcvItemsCheck(t, s, "mcv_cap_s", mcvItemsExpected{[]any{"1", "1"}, .4, .28})
		s.run(fixture.Exec("ALTER STATISTICS mcv_cap_s SET STATISTICS 2", 0))
		s.run(fixture.Exec("ANALYZE mcv_cap", 0))
		mcvItemsCheck(t, s, "mcv_cap_s", mcvItemsExpected{[]any{"1", "1"}, .4, .28}, mcvItemsExpected{[]any{"1", "2"}, .3, .35})
	})
}

func TestReferenceStatisticsMCVItemsNulls(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		s.run(fixture.Exec("CREATE TABLE mcv_strings(a TEXT,b TEXT)", 0))
		s.run(fixture.Exec("CREATE STATISTICS mcv_strings_s(mcv) ON a,b FROM mcv_strings", 0))
		s.run(fixture.Exec(`INSERT INTO mcv_strings VALUES(NULL,'x'),('NULL','x'),('','x'),('a,b','x'),('a"b','x'),('a\b','x'),('{a}','x')`, 7))
		s.run(fixture.Exec("ANALYZE mcv_strings", 0))
		var want []mcvItemsExpected
		for _, value := range []any{nil, "NULL", "", "a,b", `a"b`, `a\b`, "{a}"} {
			want = append(want, mcvItemsExpected{[]any{value, "x"}, 1.0 / 7, 1.0 / 7})
		}
		mcvItemsCheck(t, s, "mcv_strings_s", want...)
		// Literal spellings are witnesses independent even of the expected-array helper.
		for _, c := range []struct {
			value any
			raw   string
		}{
			{nil, "{NULL,x}"}, {"NULL", `{"NULL",x}`}, {"", `{"",x}`},
			{"a,b", `{"a,b",x}`}, {`a"b`, `{"a\"b",x}`}, {`a\b`, `{"a\\b",x}`},
		} {
			if got := mcvItemsArray([]any{c.value, "x"}); got != c.raw {
				t.Fatalf("independent array grammar witness %q want %q", got, c.raw)
			}
		}
	})
}

func TestReferenceStatisticsMCVItemsFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"mcv_smoke_actor"})
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		mcvSetup(t, s)
		mcvItemsCheck(t, s, "mcv_s", mcvItemsFour...)
		step := fixture.Select("SELECT * FROM pg_mcv_list_items(NULL::pg_mcv_list)", mcvItemsNames, mcvItemsTypes, nil)
		step.CheckMetadata = mcvItemsMetadata(s.pg)
		s.run(step)
		s.run(fixture.Select("SELECT m.index,m.ordinality FROM pg_mcv_list_items("+mcvItemsArgument+") WITH ORDINALITY m ORDER BY m.index", []string{"index", "ordinality"}, []string{"INT4", "INT8"}, [][]any{{int64(0), int64(1)}, {int64(1), int64(2)}, {int64(2), int64(3)}}, "mcv_s"))
		s.run(fixture.Exec("INSERT INTO mcv_t VALUES(2,2)", 1))
		mcvItemsCheck(t, s, "mcv_s", mcvItemsFour...)
		s.run(fixture.Exec("ANALYZE mcv_t", 0))
		mcvItemsCheck(t, s, "mcv_s", mcvItemsFive...)
		mcvItemsSmokeAuthority(t, s)
		if !s.pg {
			mcvItemsStaleRestore(t, s)
		}
	})
}
