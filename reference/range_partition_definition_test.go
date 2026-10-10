package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceRangePartitionDefinition(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			referenceRangeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
				cases := []struct {
					name, columns, key, lower, upper, input, query string
					types                                          []string
					rows                                           [][]any
				}{
					{"integer", "k INT,payload TEXT", "k", "1+0", "'3'::INT", "(1,'a'),(2,'b')", "k,payload", []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), "b"}}},
					{"wide", "k BIGINT", "k", "'9007199254740993'", "'9007199254740995'", "(9007199254740993),(9007199254740994)", "k", []string{"INT8"}, [][]any{{int64(9007199254740993)}, {int64(9007199254740994)}}},
					{"text", "k TEXT", `k COLLATE "C" text_ops`, "'a'", "'c'", "('a'),('b')", "k", []string{"TEXT"}, [][]any{{"a"}, {"b"}}},
					{"boolean", "k BOOL", "k", "FALSE", "TRUE", "(FALSE)", "k", []string{"BOOL"}, [][]any{{false}}},
					{"numeric", "k NUMERIC(5,2)", "k", "'1.001'", "'1.025'", "(1.00),(1.02)", "k", []string{"NUMERIC"}, [][]any{{"1.00"}, {"1.02"}}},
					{"expression", "k INT", "(k+1)", "2", "4", "(1),(2)", "k", []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}},
					{"immutable", "k TEXT", "(lower(k))", "'a'", "'c'", "('A'),('B')", "k", []string{"TEXT"}, [][]any{{"A"}, {"B"}}},
				}
				for _, c := range cases {
					t.Run(c.name, func(t *testing.T) {
						run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
						name := "rp_def_" + c.name
						run(referenceExec("CREATE TABLE "+name+"("+c.columns+") PARTITION BY RANGE("+c.key+")", 0))
						run(referenceExec("CREATE TABLE "+name+"_leaf PARTITION OF "+name+" FOR VALUES FROM("+c.lower+") TO("+c.upper+")", 0))
						run(referenceExec("INSERT INTO "+name+" VALUES"+c.input, int64(len(c.rows))))
						names := []string{"k"}
						if len(c.types) == 2 {
							names = append(names, "payload")
						}
						run(referenceSelect("SELECT "+c.query+" FROM "+name+" ORDER BY k", names, c.types, c.rows))
					})
				}
				run(referenceExec("CREATE TABLE rp_date(k DATE) PARTITION BY RANGE(k)", 0))
				run(referenceExec("CREATE TABLE rp_date_low PARTITION OF rp_date FOR VALUES FROM(MINVALUE) TO('infinity')", 0))
				run(referenceExec("CREATE TABLE rp_date_high PARTITION OF rp_date FOR VALUES FROM('infinity') TO(MAXVALUE)", 0))
				run(referenceExec("INSERT INTO rp_date VALUES('-infinity'),('2000-01-01'),('infinity')", 3))
				run(referenceSelect("SELECT k::TEXT AS k FROM rp_date_low ORDER BY k", []string{"k"}, []string{"TEXT"}, [][]any{{"-infinity"}, {"2000-01-01"}}))
				run(referenceSelect("SELECT k::TEXT AS k FROM rp_date_high", []string{"k"}, []string{"TEXT"}, [][]any{{"infinity"}}))
				run(referenceExec("CREATE TYPE rp_color AS ENUM('red','green','blue')", 0))
				run(referenceExec("CREATE DOMAIN rp_definition_id AS BIGINT", 0))
				for _, c := range []struct{ name, typ, lower, upper, input, output string }{{"enum", "rp_color", "'red'", "'blue'", "'green'", "green"}, {"domain", "rp_definition_id", "9007199254740993", "9007199254740995", "9007199254740994", "9007199254740994"}} {
					run(referenceExec("CREATE TABLE rp_"+c.name+"(k "+c.typ+") PARTITION BY RANGE(k)", 0))
					run(referenceExec("CREATE TABLE rp_"+c.name+"_leaf PARTITION OF rp_"+c.name+" FOR VALUES FROM("+c.lower+") TO("+c.upper+")", 0))
					run(referenceExec("INSERT INTO rp_"+c.name+" VALUES("+c.input+")", 1))
					run(referenceSelect("SELECT k::TEXT AS k FROM rp_"+c.name, []string{"k"}, []string{"TEXT"}, [][]any{{c.output}}))
				}
				run(referenceExec("CREATE SEQUENCE rp_bound_seq START 10", 0))
				run(referenceExec("CREATE TABLE rp_once(k BIGINT) PARTITION BY RANGE(k)", 0))
				run(referenceExec("CREATE TABLE rp_once_leaf PARTITION OF rp_once FOR VALUES FROM(nextval('rp_bound_seq')) TO(20)", 0))
				run(referenceSelect("SELECT last_value FROM rp_bound_seq", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(10)}}))
				run(referenceExec("INSERT INTO rp_once VALUES(10),(19)", 2))
				run(referenceSelect("SELECT last_value FROM rp_bound_seq", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(10)}}))
				run(referenceExec("CREATE TABLE rp_stable(k DATE) PARTITION BY RANGE(k)", 0))
				run(referenceExec("CREATE TABLE rp_stable_leaf PARTITION OF rp_stable FOR VALUES FROM(CURRENT_DATE) TO(MAXVALUE)", 0))
				run(referenceExec("INSERT INTO rp_stable VALUES(CURRENT_DATE)", 1))
				run(referenceSelect("SELECT COUNT(*) FROM rp_stable", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
				run(referenceSelect(`SELECT row_number() OVER (PARTITION BY (k-k) ORDER BY k) AS ordinal FROM rp_def_integer ORDER BY k`, []string{"ordinal"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(2)}}))
			})
		})
	}
}
