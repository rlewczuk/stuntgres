package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceDeparseRelationsReconstructionConsumers(t *testing.T) {
	deparseRelationsScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		deparseRelationsSeed(t, p, b, c)
		calls := []string{"pg_get_viewdef('v'::text)", "pg_get_viewdef('v'::regclass)", "pg_get_viewdef('v'::text,false)", "pg_get_viewdef('v'::text,true)", "pg_get_viewdef('v'::regclass,false)", "pg_get_viewdef('v'::regclass,true)"}
		for _, n := range []int{-2147483648, -1, 0, 1, 20, 80, 2147483647} {
			calls = append(calls, fmt.Sprintf("pg_get_viewdef('v'::regclass,%d)", n))
		}
		names := []string{"v"}
		for i, call := range calls {
			d := deparseRelationsRead(t, p, b, c, "SELECT "+call+" AS definition")
			name := fmt.Sprintf("copy_%d", i)
			names = append(names, name)
			referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE VIEW "+name+" AS "+d, 0))
		}
		for _, extra := range []bool{false, true} {
			if extra {
				referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("INSERT INTO t VALUES(4,'d',true)", 1))
			}
			for _, name := range names {
				s := deparseRelationsBag("SELECT k,label FROM "+name, extra)
				referenceRunStep(t, p.ctx, c, b.recorder, s)
				referenceRunStep(t, p.ctx, c, b.recorder, referencePrepared(s))
			}
		}
		for _, tc := range []struct {
			name, body     string
			columns, types []string
			rows           [][]any
		}{
			{"nested", "SELECT k,label FROM v", []string{"k", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(3), nil}, {int64(3), nil}, {int64(4), "d"}}},
			{"zero_width", "SELECT FROM t WHERE visible", []string{}, []string{}, [][]any{{}, {}, {}, {}}},
			{"value_view", "SELECT * FROM (VALUES(1,'x'::text),(1,NULL::text)) AS x(k,label)", []string{"k", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}, {int64(1), nil}}},
			{"grouped", "SELECT id,count(*) AS n FROM t WHERE visible GROUP BY id", []string{"id", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(3), int64(2)}, {int64(4), int64(1)}}},
			{"joined", "SELECT a.*,b.txt AS other FROM t a JOIN t b USING(id) WHERE a.id=1", []string{"id", "txt", "visible", "other"}, []string{"INT4", "TEXT", "BOOL", "TEXT"}, [][]any{{int64(1), "a", true, "a"}}},
			{"hidden_order", "SELECT txt FROM t WHERE visible ORDER BY id DESC", []string{"txt"}, []string{"TEXT"}, [][]any{{"d"}, {nil}, {nil}, {"a"}}},
		} {
			referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE VIEW "+tc.name+" AS "+tc.body, 0))
			d := deparseRelationsRead(t, p, b, c, "SELECT pg_get_viewdef($1::text,true) AS definition", tc.name)
			referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE VIEW replay_"+tc.name+" AS "+d, 0))
			s := referenceSelect("SELECT * FROM replay_"+tc.name, tc.columns, tc.types, tc.rows)
			s.bag = tc.name != "hidden_order"
			referenceRunStep(t, p.ctx, c, b.recorder, s)
			referenceRunStep(t, p.ctx, c, b.recorder, referencePrepared(s))
		}
		for _, q := range []string{"CREATE SEQUENCE seq START 10", "CREATE VIEW dormant AS SELECT 1/0 AS failure FROM t", "CREATE VIEW seq_view AS SELECT nextval('seq'::regclass) AS draw FROM t"} {
			referenceRunStep(t, p.ctx, c, b.recorder, referenceExec(q, 0))
		}
		for _, name := range []string{"dormant", "seq_view"} {
			d := deparseRelationsRead(t, p, b, c, "SELECT pg_get_viewdef($1::text) AS definition", name)
			referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE VIEW replay_"+name+" AS "+d, 0))
		}
		referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT nextval('seq'::regclass) AS draw", []string{"draw"}, []string{"INT8"}, [][]any{{int64(10)}}))
		failure := referenceImplicitError("SELECT * FROM replay_dormant", referenceDiagnostic{Code: "22012", Message: "division by zero"})
		failure.selectRows = true
		referenceRunStep(t, p.ctx, c, b.recorder, failure)
		referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT draw FROM replay_seq_view", []string{"draw"}, []string{"INT8"}, [][]any{{int64(11)}, {int64(12)}, {int64(13)}, {int64(14)}, {int64(15)}}))
	})
}
