package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePartitionPruningExpressions(t *testing.T) {
	referencePruningScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := func(s fixture.Step) {
			if !t.Run(s.Query, func(t *testing.T) {
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}) {
				t.FailNow()
			}
		}
		for _, c := range []struct {
			name, typ, strategy, key, bound, input, predicate, projection string
			types                                                         []string
			rows                                                          [][]any
		}{
			{"wide", "BIGINT", "LIST", "k", "IN(9007199254740993)", "(9007199254740992),(9007199254740993),(9007199254740993)", "k=9007199254740993", "k", []string{"INT8"}, [][]any{{int64(9007199254740993)}, {int64(9007199254740993)}}},
			{"numeric", "NUMERIC(5,2)", "LIST", "k", "IN(1.001)", "(1.00),(1.02),(NULL)", "k=1.000", "k", []string{"NUMERIC"}, [][]any{{"1.00"}}},
			{"bpchar", "CHAR(3)", "LIST", "k", "IN('a')", "('a'),('a  '),('b')", "k='a'::CHAR(3)", "k::TEXT AS k", []string{"TEXT"}, [][]any{{"a"}, {"a"}}},
			{"text", "TEXT", "LIST", `k COLLATE "C" text_ops`, "IN('A')", "('A'),('a'),(NULL)", `k COLLATE "C"='A'`, "k", []string{"TEXT"}, [][]any{{"A"}}},
			{"lower", "TEXT", "LIST", "(lower(k))", "IN('a')", "('A'),('a'),('B')", "lower(k)='a'", "k", []string{"TEXT"}, [][]any{{"A"}, {"a"}}},
			{"related", "TEXT", "LIST", "(lower(k))", "IN('a')", "('A'),('a'),('B')", "k='B'", "k", []string{"TEXT"}, [][]any{{"B"}}},
			{"derived", "INT", "RANGE", "(k+1)", "FROM(2) TO(4)", "(1),(2),(3)", "k+1=2", "k", []string{"INT4"}, [][]any{{int64(1)}}},
			{"unmatched", "INT", "RANGE", "(k+1)", "FROM(2) TO(4)", "(1),(2),(3)", "k=3", "k", []string{"INT4"}, [][]any{{int64(3)}}},
			{"bool", "BOOL", "LIST", "k", "IN(FALSE,NULL)", "(FALSE),(TRUE),(NULL)", "k IS NOT NULL", "k", []string{"BOOL"}, [][]any{{false}, {true}}},
		} {
			name := "pp_expr_" + c.name
			run(fixture.Exec("CREATE TABLE "+name+"(k "+c.typ+") PARTITION BY "+c.strategy+"("+c.key+")", 0))
			run(fixture.Exec("CREATE TABLE "+name+"_a PARTITION OF "+name+" FOR VALUES "+c.bound, 0))
			run(fixture.Exec("CREATE TABLE "+name+"_d PARTITION OF "+name+" DEFAULT", 0))
			// These fixtures each contain exactly three physical rows.
			run(fixture.Exec("INSERT INTO "+name+" VALUES"+c.input, 3))
			s := fixture.Select("SELECT "+c.projection+" FROM "+name+" WHERE "+c.predicate, []string{"k"}, c.types, c.rows)
			s.Bag = true
			run(s)
		}
		run(fixture.Exec("CREATE TYPE pp_color AS ENUM('red','green','blue')", 0))
		run(fixture.Exec("CREATE DOMAIN pp_id AS BIGINT", 0))
		for _, c := range []struct{ name, typ, bound, input, predicate, value string }{
			{"enum", "pp_color", "'green'", "'green'", "k='green'::pp_color", "green"},
			{"domain", "pp_id", "9007199254740993", "9007199254740993", "k=9007199254740993::pp_id", "9007199254740993"},
		} {
			name := "pp_expr_" + c.name
			run(fixture.Exec("CREATE TABLE "+name+"(k "+c.typ+") PARTITION BY LIST(k)", 0))
			run(fixture.Exec("CREATE TABLE "+name+"_a PARTITION OF "+name+" FOR VALUES IN("+c.bound+",NULL)", 0))
			run(fixture.Exec("INSERT INTO "+name+" VALUES("+c.input+"),(NULL)", 2))
			run(fixture.Select("SELECT k::TEXT AS k FROM "+name+" WHERE "+c.predicate, []string{"k"}, []string{"TEXT"}, [][]any{{c.value}}))
		}
		// Stable, volatile, subquery and row-dependent operands remain legal SQL;
		// none of these assertions promises a Boolean evaluation order or count.
		for _, predicate := range []string{"k=1 OR random()<0", "k=1 OR k=k+100", "k=(SELECT 1)", "k=1 AND CURRENT_DATE=CURRENT_DATE"} {
			referencePruningKeys(run, b.PG, "SELECT k FROM pp_expr_derived WHERE "+predicate, int64(1))
		}
	})
}
