package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func operatorEndpointWitnesses(t *testing.T) {
	rangeOperatorScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE edges(k integer,a numrange,b numrange,e numeric)", 0))
		run(fixture.Exec("INSERT INTO edges VALUES (1,'[1,2)','[2,3)',2),(2,'(1,2)','(2,3)',1),(3,'[1,2]','[2,3]',2),(4,'(,Infinity)','[Infinity,Infinity]', 'Infinity'),(5,'(,NaN)','[NaN,NaN]','NaN'),(6,'(,)','[NaN,NaN]','NaN')", 6))
		operatorBool(t, p, b, "a -|- b", "edges ORDER BY k", [][]any{{true}, {false}, {false}, {true}, {true}, {false}})
		operatorBool(t, p, b, "a && b", "edges ORDER BY k", [][]any{{false}, {false}, {true}, {false}, {false}, {true}})
		operatorBool(t, p, b, "a @> e", "edges ORDER BY k", [][]any{{false}, {false}, {true}, {false}, {false}, {true}})
		run(fixture.Exec("CREATE TABLE temporal_edges(k integer,a tsrange,b tsrange)", 0))
		run(fixture.Exec("INSERT INTO temporal_edges VALUES (1,'[2000-01-01,2000-01-02)','[2000-01-02 00:00:00.000001,2000-01-03)'),(2,'[2000-01-01,2000-01-02]','[2000-01-02,2000-01-03)'),(3,'[2000-01-01,2000-01-02)','[2000-01-02,2000-01-03)')", 3))
		operatorBool(t, p, b, "a -|- b", "temporal_edges ORDER BY k", [][]any{{false}, {false}, {true}})
		operatorBool(t, p, b, "a && b", "temporal_edges ORDER BY k", [][]any{{false}, {true}, {false}})
		run(fixture.Exec("CREATE TABLE date_edges(k integer,a daterange,b daterange)", 0))
		run(fixture.Exec("INSERT INTO date_edges VALUES (1,'[2000-02-28,2000-02-29]','[2000-03-01,2000-03-02)'),(2,'(,infinity)','[infinity,infinity]'),(3,'[-infinity,-infinity]','(-infinity,)')", 3))
		operatorBool(t, p, b, "a -|- b", "date_edges ORDER BY k", [][]any{{true}, {true}, {true}})
		operatorBool(t, p, b, "a && b", "date_edges ORDER BY k", [][]any{{false}, {false}, {false}})
		run(fixture.Exec("CREATE TABLE canonical(a int4range,b int4range)", 0))
		run(fixture.Exec("INSERT INTO canonical VALUES ('[1,3]','[1,4)')", 1))
		operatorBool(t, p, b, "a = b", "canonical", [][]any{{true}})
	})
}

func operatorEmptyWitnesses(t *testing.T, p *fixture.Pair, b fixture.Backend, f operatorFamily) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE empties(k integer,r "+f.name+",s "+f.name+",m "+f.multi()+",n "+f.multi()+")", 0))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO empties VALUES (1,'empty','empty','{}','{}'),(2,'[1,3)','empty','{[1,3)}','{}'),(3,'empty','[1,3)','{}','{[1,3)}'),(4,NULL,NULL,NULL,NULL)", 4))
	for _, pair := range [][2]string{{"r", "s"}, {"r", "n"}, {"m", "s"}, {"m", "n"}} {
		for _, op := range []string{"@>", "<@", "&&", "<<", ">>", "&<", "&>", "-|-"} {
			rows := [][]any{{false}, {false}, {false}, {nil}}
			if op == "@>" {
				rows = [][]any{{true}, {true}, {false}, {nil}}
			}
			if op == "<@" {
				rows = [][]any{{true}, {false}, {true}, {nil}}
			}
			operatorBool(t, p, b, pair[0]+" "+op+" "+pair[1], "empties ORDER BY k", rows)
		}
	}
}
