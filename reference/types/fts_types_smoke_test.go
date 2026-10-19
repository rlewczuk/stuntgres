package reference_test

import (
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSTypesFeatureSmoke(t *testing.T) {
	ftsTypesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		var notices []fixture.Diagnostic
		_, conn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
		run(fixture.Exec("CREATE TABLE fts_smoke(id integer,v tsvector,q tsquery)", 0))
		run(fixture.Exec("INSERT INTO fts_smoke VALUES(1,'a:3B,1D,3A a:2c','fat & (rat | cat)')", 1))
		query := "INSERT INTO fts_smoke VALUES($1,$2,$3)"
		stmt, err := conn.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: query}
		fixture.RunStep(t, p.Ctx, held, recorder, fixture.Exec(query, 1, int64(2), []byte("b:16384D"), "a <1> b:*a"))
		fixture.RunStep(t, p.Ctx, held, recorder, fixture.Exec(query, 1, int64(3), nil, nil))
		if len(notices) != 0 {
			t.Fatal("unexpected input notices", notices)
		}
		fixture.RunStep(t, p.Ctx, held, recorder, fixture.Exec(query, 1, int64(4), "", ""))
		if !reflect.DeepEqual(notices, []fixture.Diagnostic{ftsTypesEmptyNotice("", "", "unnamed portal parameter $3 = ''")}) {
			t.Fatal("empty input notice", notices)
		}
		notices = nil
		want := [][]any{{int64(1), "'a':1,2C,3A", "'fat' & ( 'rat' | 'cat' )"}, {int64(2), "'b':16383", "'a' <-> 'b':*A"}, {int64(3), nil, nil}, {int64(4), "", ""}}
		selectRows := fixture.Select("SELECT id,v,q FROM fts_smoke ORDER BY id", []string{"id", "v", "q"}, []string{"INT4", "TSVECTOR", "TSQUERY"}, want)
		run(selectRows)
		run(fixture.Prepared(selectRows))
		if len(notices) != 0 {
			t.Fatal("output notices", notices)
		}
		d := fixture.ControlsReferenceDiagnostic("42601", "wrong position info in tsvector: \"a:0\"")
		d.Position = "8"
		s := fixture.ErrorDiagnostic("SELECT 'a:0'::tsvector", d)
		s.SelectRows = true
		run(s)
		s.Prepared = true
		s.ErrorStage = "prepare"
		run(s)
		d = fixture.ControlsReferenceDiagnostic("42601", "no operand in tsquery: \"a &\"")
		d.Context = "unnamed portal parameter $1 = '...'"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT $1::tsquery", d, "a &")
			s.SelectRows = true
			s.Prepared = prepared
			s.ErrorStage = "execute"
			run(s)
		}
		for _, tc := range []struct{ table, typ, dbtype string }{{"fts_nn_vector", "tsvector", "TSVECTOR"}, {"fts_nn_query", "tsquery", "TSQUERY"}} {
			run(fixture.Exec("CREATE TABLE "+tc.table+"(v "+tc.typ+" NOT NULL)", 0))
			run(fixture.Exec("INSERT INTO "+tc.table+" VALUES('a')", 1))
			d := fixture.ControlsReferenceDiagnostic("23502", "null value in column \"v\" of relation \""+tc.table+"\" violates not-null constraint")
			d.Detail = "Failing row contains (null)."
			d.Schema = "public"
			d.Table = tc.table
			d.Column = "v"
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("INSERT INTO "+tc.table+" VALUES('b'),(NULL)", d)
				s.Prepared = prepared
				s.ErrorStage = "execute"
				run(s)
				run(fixture.Select("SELECT v FROM "+tc.table, []string{"v"}, []string{tc.dbtype}, [][]any{{"'a'"}}))
			}
		}
		run(selectRows)
		if !b.PG {
			ftsTypesNativeControls(t, p, b, "fts_smoke", want)
		}
	})
}
