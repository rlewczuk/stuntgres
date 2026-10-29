package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDeparsePartitionConstraints(t *testing.T) {
	for _, tc := range []struct{ name, columns, strategy, bound, want string }{
		{"range", "k INT", "RANGE(k)", "FROM(0) TO(10)", "((k IS NOT NULL) AND (k >= 0) AND (k < 10))"},
		{"minimum", "k INT", "RANGE(k)", "FROM(MINVALUE) TO(10)", "((k IS NOT NULL) AND (k < 10))"},
		{"maximum", "k INT", "RANGE(k)", "FROM(10) TO(MAXVALUE)", "((k IS NOT NULL) AND (k >= 10))"},
		{"unbounded", "k INT", "RANGE(k)", "FROM(MINVALUE) TO(MAXVALUE)", "(k IS NOT NULL)"},
		{"equal_prefix", "a INT,b INT", "RANGE(a,b)", "FROM(1,2) TO(1,5)", "((a IS NOT NULL) AND (b IS NOT NULL) AND (a = 1) AND (b >= 2) AND (b < 5))"},
		{"lexicographic", "a INT,b INT", "RANGE(a,b)", "FROM(1,2) TO(3,4)", "((a IS NOT NULL) AND (b IS NOT NULL) AND ((a > 1) OR ((a = 1) AND (b >= 2))) AND ((a < 3) OR ((a = 3) AND (b < 4))))"},
		{"sentinel_suffix", "a INT,b INT", "RANGE(a,b)", "FROM(1,MINVALUE) TO(3,MAXVALUE)", "((a IS NOT NULL) AND (b IS NOT NULL) AND (a >= 1) AND (a <= 3))"},
		{"list_null", "k INT", "LIST(k)", "IN(NULL)", "(k IS NULL)"},
		{"list_one", "k INT", "LIST(k)", "IN(1)", "((k IS NOT NULL) AND (k = 1))"},
		{"list_many", "k INT", "LIST(k)", "IN(3,1)", "((k IS NOT NULL) AND (k = ANY (ARRAY[3, 1])))"},
		{"list_null_many", "k INT", "LIST(k)", "IN(3,1,NULL)", "((k IS NULL) OR (k = ANY (ARRAY[3, 1])))"},
		{"list_duplicate", "k INT", "LIST(k)", "IN(3,1,3,NULL,NULL)", "((k IS NULL) OR (k = ANY (ARRAY[3, 1])))"},
		{"text", "k TEXT", "LIST(k)", "IN('it''s')", "((k IS NOT NULL) AND (k = 'it''s'::text))"},
		{"date", "k DATE", "RANGE(k)", "FROM('2025-01-01') TO('2025-02-01')", "((k IS NOT NULL) AND (k >= '2025-01-01'::date) AND (k < '2025-02-01'::date))"},
		{"numeric", "k NUMERIC", "LIST(k)", "IN(1.25)", "((k IS NOT NULL) AND (k = 1.25))"},
		{"negative", "k INT", "LIST(k)", "IN(-2)", "((k IS NOT NULL) AND (k = '-2'::integer))"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture.DeparseRelationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE dp_bound("+tc.columns+") PARTITION BY "+tc.strategy, 0))
				run(fixture.Exec("CREATE TABLE dp_leaf PARTITION OF dp_bound FOR VALUES "+tc.bound, 0))
				s := fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_partition_constraintdef('dp_leaf'::regclass) AS definition", tc.want)
				run(s)
				run(fixture.Prepared(s))
				if tc.name == "range" {
					q := "CREATE TABLE dp_bad PARTITION OF dp_bound FOR VALUES FROM(NULL) TO(20)"
					w := fixture.ControlsReferenceDiagnostic("42P17", "cannot specify NULL in range bound")
					run(fixture.ErrorDiagnostic(q, w))
					run(s)
				}
			})
		})
	}
	for _, tc := range []struct{ name, columns, key, args string }{
		{"ordinary", "k INT", "k", "k"},
		{"multiple", "k INT,v TEXT", "k,v", "k, v"},
		{"expression", "k INT", "(k+1)", "(k + 1)"},
	} {
		t.Run("hash_"+tc.name, func(t *testing.T) {
			fixture.DeparseRelationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE dp_hash("+tc.columns+") PARTITION BY HASH("+tc.key+")", 0))
				run(fixture.Exec("CREATE TABLE dp_hash_leaf PARTITION OF dp_hash FOR VALUES WITH(MODULUS 4,REMAINDER 1)", 0))
				id := fixture.DeparseDefinitionID(t, c, "SELECT 'dp_hash'::regclass::oid")
				want := fmt.Sprintf("satisfies_hash_partition('%d'::oid, 4, 1, %s)", id, tc.args)
				s := fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_partition_constraintdef('dp_hash_leaf'::regclass) AS definition", want)
				run(s)
				run(fixture.Prepared(s))
			})
		})
	}
}

func TestReferenceDeparsePartitionDefaultAncestors(t *testing.T) {
	fixture.DeparseRelationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"CREATE TABLE dp_root(k INT,v INT) PARTITION BY RANGE(k)",
			"CREATE TABLE dp_mid PARTITION OF dp_root FOR VALUES FROM(0) TO(10) PARTITION BY RANGE(k)",
			"CREATE TABLE dp_nested PARTITION OF dp_mid FOR VALUES FROM(2) TO(5)",
			"CREATE TABLE dp_only PARTITION OF dp_mid DEFAULT",
			"CREATE TABLE dp_top(k INT) PARTITION BY LIST(k)",
			"CREATE TABLE dp_top_default PARTITION OF dp_top DEFAULT",
			"CREATE TABLE dp_other PARTITION OF dp_root FOR VALUES FROM(10) TO(20) PARTITION BY LIST(v)",
			"CREATE TABLE dp_other_leaf PARTITION OF dp_other FOR VALUES IN(3)",
			"CREATE TABLE dp_inherited PARTITION OF dp_other DEFAULT PARTITION BY HASH(k)",
			"CREATE TABLE dp_hleaf PARTITION OF dp_inherited FOR VALUES WITH(MODULUS 2,REMAINDER 0)",
		} {
			run(fixture.Exec(q, 0))
		}
		text := func(name string, want any) {
			run(fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_partition_constraintdef('"+name+"'::regclass) AS definition", want))
		}
		text("dp_nested", "((k IS NOT NULL) AND (k >= 0) AND (k < 10) AND (k IS NOT NULL) AND (k >= 2) AND (k < 5))")
		text("dp_top_default", nil)
		run(fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_partkeydef('dp_other'::regclass) AS definition", "LIST (v)"))
		text("dp_other_leaf", "((k IS NOT NULL) AND (k >= 10) AND (k < 20) AND (v IS NOT NULL) AND (v = 3))")
		id := fixture.DeparseDefinitionID(t, c, "SELECT 'dp_inherited'::regclass::oid")
		text("dp_hleaf", fmt.Sprintf("((k IS NOT NULL) AND (k >= 10) AND (k < 20) AND (NOT ((v IS NOT NULL) AND (v = 3))) AND satisfies_hash_partition('%d'::oid, 2, 0, k))", id))
		run(fixture.Exec("DROP TABLE dp_nested", 0))
		ancestor := "((k IS NOT NULL) AND (k >= 0) AND (k < 10))"
		text("dp_only", ancestor)
		q := "SELECT pg_get_partition_constraintdef($1) AS definition"
		held, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		did := fixture.DeparseDefinitionID(t, c, "SELECT 'dp_top_default'::regclass::oid")
		executor := fixture.HeldStatement{Executor: c, Statement: held, SQL: q}
		check := func(want any) {
			fixture.RunStep(t, p.Ctx, executor, b.Recorder, fixture.DeparseRelationsTextStep(t, b, q, want, did))
		}
		check(nil)
		run(fixture.Exec("CREATE TABLE dp_values PARTITION OF dp_top FOR VALUES IN(3,1)", 0))
		check("(NOT ((k IS NOT NULL) AND (k = ANY (ARRAY[1, 3]))))")
		run(fixture.Exec("CREATE TABLE dp_null PARTITION OF dp_top FOR VALUES IN(NULL)", 0))
		check("(NOT ((k IS NULL) OR (k = ANY (ARRAY[1, 3]))))")
		run(fixture.Exec("DROP TABLE dp_values", 0))
		check("(NOT (k IS NULL))")
		run(fixture.Exec("DROP TABLE dp_null", 0))
		check(nil)
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("ALTER TABLE dp_root DETACH PARTITION dp_mid", 0))
		text("dp_mid", nil)
		run(fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_partkeydef('dp_mid'::regclass) AS definition", "RANGE (k)"))
		run(fixture.Exec("ROLLBACK", 0))
		text("dp_mid", ancestor)
		run(fixture.Exec("ALTER TABLE dp_root DETACH PARTITION dp_mid", 0))
		text("dp_only", nil)
		text("dp_mid", nil)
	})
}
