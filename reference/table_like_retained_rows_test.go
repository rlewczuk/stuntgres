package reference_test

import (
	"bytes"
	"fmt"
	"stuntgres"
	"testing"
)

func TestReferenceTableLikeTemporaryRetainedRows(t *testing.T) {
	temporaryTableScenario(t, func(s *tempReferenceSession) {
		run := func(step referenceStep) {
			step.prepared = s.prepared
			referenceRunStep(s.t, s.p.ctx, s.c, s.record, step)
		}
		s.utility("CREATE TABLE public.carrier(v INT)", nil)
		run(referenceExec("INSERT INTO public.carrier VALUES(7)", 1))
		s.utility("CREATE TEMP TABLE row_source(dead TEXT,a INT NOT NULL,g INT GENERATED ALWAYS AS(a*2) STORED,n INT,active BOOL,CONSTRAINT positive CHECK(a>0))", nil)
		s.utility("ALTER TABLE row_source DROP COLUMN dead", nil)
		s.utility("CREATE INDEX row_expression ON row_source((a+1))", nil)
		s.utility("CREATE INDEX row_partial ON row_source((10/n)) WHERE active", nil)
		run(referenceExec("INSERT INTO row_source(a,n,active) VALUES(3,2,true)", 1))
		if !s.postgres {
			var dump bytes.Buffer
			if err := stuntgres.Dump(s.p.ctx, s.p.native, &dump); err != nil {
				s.t.Fatal(err)
			}
			if err := stuntgres.Reset(s.p.ctx, s.p.native); err != nil {
				s.t.Fatal(err)
			}
			if err := stuntgres.Restore(s.p.ctx, s.p.native, bytes.NewReader(dump.Bytes())); err != nil {
				s.t.Fatal(err)
			}
		}
		sourceRows := referenceSelect("SELECT * FROM row_source", []string{"a", "g", "n", "active"}, []string{"INT4", "INT4", "INT4", "BOOL"}, [][]any{{int64(3), int64(6), int64(2), true}})
		run(sourceRows)
		for _, temporary := range []bool{false, true} {
			prefix, name, schema := "", "public.row_copy", "public"
			if temporary {
				prefix, name = "TEMP ", "row_temp_copy"
				s.exec(s.c, "SET search_path TO pg_temp,public", 0)
				schema = tempReferenceNamespace(s.t, s, false)
				s.exec(s.c, "SET search_path TO public", 0)
			}
			table := map[bool]string{false: "row_copy", true: "row_temp_copy"}[temporary]
			s.utility("CREATE "+prefix+"TABLE "+name+"(lead TEXT,LIKE pg_temp.row_source INCLUDING ALL)", nil)
			run(referenceSelect("SELECT * FROM "+name, []string{"lead", "a", "g", "n", "active"}, []string{"TEXT", "INT4", "INT4", "INT4", "BOOL"}, nil))
			referenceTableDDLMetadata(s.t, s.p.ctx, s.c, s.postgres, s.prepared, "SELECT * FROM "+name, []string{"lead", "a", "g", "n", "active"}, []string{"TEXT", "INT4", "INT4", "INT4", "BOOL"}, []bool{true, false, true, true, true}, 0)
			run(referenceExec("INSERT INTO "+name+"(lead,a,n,active) VALUES('x',4,2,true),('y',5,0,false)", 2))
			run(referenceExec("UPDATE "+name+" SET a=6 WHERE lead='x'", 1))
			want := referenceSelect("SELECT * FROM "+name+" ORDER BY lead", []string{"lead", "a", "g", "n", "active"}, []string{"TEXT", "INT4", "INT4", "INT4", "BOOL"}, [][]any{{"x", int64(6), int64(12), int64(2), true}, {"y", int64(5), int64(10), int64(0), false}})
			run(want)
			run(tableLikeError("INSERT INTO "+name+"(lead,a,n,active) VALUES('bad',-1,1,false)", referenceDiagnostic{Code: "23514", Message: fmt.Sprintf(`new row for relation %q violates check constraint "positive"`, map[bool]string{false: "row_copy", true: "row_temp_copy"}[temporary]), Detail: "Failing row contains (bad, -1, -2, 1, f).", Schema: schema, Table: map[bool]string{false: "row_copy", true: "row_temp_copy"}[temporary], Constraint: "positive"}))
			run(tableLikeError("UPDATE "+name+" SET active=true WHERE lead='y'", referenceDiagnostic{Code: "22012", Message: "division by zero"}))
			run(want)
			renderSchema := schema
			if temporary && s.postgres {
				// PostgreSQL renders the owner's physical TEMP namespace through
				// pg_temp; the catalog schema and diagnostic still name its identity.
				renderSchema = "pg_temp"
			}
			run(referenceSelect("SELECT indexname,indexdef FROM pg_indexes WHERE schemaname='"+schema+"' AND tablename='"+table+"' ORDER BY indexname", []string{"indexname", "indexdef"}, []string{"NAME", "TEXT"}, [][]any{{table + "_expr_idx", "CREATE INDEX " + table + "_expr_idx ON " + renderSchema + "." + table + " USING btree (((a + 1)))"}, {table + "_expr_idx1", "CREATE INDEX " + table + "_expr_idx1 ON " + renderSchema + "." + table + " USING btree (((10 / n))) WHERE active"}}))
			run(tableLikeError("CREATE "+prefix+"TABLE row_failed(LIKE pg_temp.row_source INCLUDING ALL,a INT)", referenceDiagnostic{Code: "42701", Message: `column "a" specified more than once`}))
			tableLikeAbsent(run, "row_failed", s.prepared)
			s.utility("CREATE "+prefix+"TABLE row_failed(LIKE pg_temp.row_source INCLUDING ALL EXCLUDING GENERATED EXCLUDING CONSTRAINTS EXCLUDING INDEXES)", nil)
			run(referenceExec("INSERT INTO row_failed(a,g,n,active) VALUES(-1,17,0,true)", 1))
			run(referenceSelect("SELECT * FROM row_failed", []string{"a", "g", "n", "active"}, []string{"INT4", "INT4", "INT4", "BOOL"}, [][]any{{int64(-1), int64(17), int64(0), true}}))
			run(referenceSelect("SELECT count(*) AS n FROM pg_indexes WHERE schemaname='"+schema+"' AND tablename='row_failed'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			s.utility("DROP TABLE row_failed", nil)
			run(sourceRows)
		}
		s.utility("DROP TABLE pg_temp.row_source", nil)
		for _, name := range []string{"public.row_copy", "row_temp_copy"} {
			run(referenceExec("INSERT INTO "+name+"(lead,a,n,active) VALUES('after',7,1,true)", 1))
			run(referenceSelect("SELECT g FROM "+name+" WHERE lead='after'", []string{"g"}, []string{"INT4"}, [][]any{{int64(14)}}))
		}
		if !s.postgres {
			var dump bytes.Buffer
			if err := stuntgres.Dump(s.p.ctx, s.p.native, &dump); err != nil {
				s.t.Fatal(err)
			}
			if err := stuntgres.Reset(s.p.ctx, s.p.native); err != nil {
				s.t.Fatal(err)
			}
			if err := stuntgres.Restore(s.p.ctx, s.p.native, bytes.NewReader(dump.Bytes())); err != nil {
				s.t.Fatal(err)
			}
			tableLikeAbsent(run, "pg_temp.row_source", s.prepared)
			for _, name := range []string{"public.row_copy", "row_temp_copy"} {
				run(referenceSelect("SELECT * FROM "+name+" ORDER BY lead", []string{"lead", "a", "g", "n", "active"}, []string{"TEXT", "INT4", "INT4", "INT4", "BOOL"}, [][]any{{"after", int64(7), int64(14), int64(1), true}, {"x", int64(6), int64(12), int64(2), true}, {"y", int64(5), int64(10), int64(0), false}}))
				referenceTableDDLMetadata(s.t, s.p.ctx, s.c, false, s.prepared, "SELECT * FROM "+name, []string{"lead", "a", "g", "n", "active"}, []string{"TEXT", "INT4", "INT4", "INT4", "BOOL"}, []bool{true, false, true, true, true}, 3)
				run(referenceExec("UPDATE "+name+" SET a=8 WHERE lead='after'", 1))
				run(referenceSelect("SELECT g FROM "+name+" WHERE lead='after'", []string{"g"}, []string{"INT4"}, [][]any{{int64(16)}}))
				run(referenceExec("INSERT INTO "+name+"(lead,a,n,active) VALUES('zero',9,0,false)", 1))
				run(tableLikeError("UPDATE "+name+" SET active=true WHERE lead='zero'", referenceDiagnostic{Code: "22012", Message: "division by zero"}))
				run(referenceSelect("SELECT g,active FROM "+name+" WHERE lead='zero'", []string{"g", "active"}, []string{"INT4", "BOOL"}, [][]any{{int64(18), false}}))
				schema, table := "public", "row_copy"
				if name == "row_temp_copy" {
					s.exec(s.c, "SET search_path TO pg_temp,public", 0)
					schema = tempReferenceNamespace(s.t, s, false)
					s.exec(s.c, "SET search_path TO public", 0)
					table = "row_temp_copy"
				}
				run(tableLikeError("INSERT INTO "+name+"(lead,a,n,active) VALUES('bad',-1,1,false)", referenceDiagnostic{Code: "23514", Message: fmt.Sprintf(`new row for relation %q violates check constraint "positive"`, table), Detail: "Failing row contains (bad, -1, -2, 1, f).", Schema: schema, Table: table, Constraint: "positive"}))
				run(referenceSelect("SELECT count(*) AS n FROM "+name, []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
			}
			if s.p.postgres != nil {
				if _, err := s.p.postgres.ExecContext(s.p.ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					s.t.Fatal(err)
				}
				if _, err := s.p.postgres.ExecContext(s.p.ctx, dump.String()); err != nil {
					s.t.Fatal("actual retained row-copy SQL replay", err)
				}
				replay := func(step referenceStep) {
					step.prepared = s.prepared
					referenceRunStep(s.t, s.p.ctx, s.p.postgres, s.p.postgresRecord, step)
				}
				replay(referenceSelect("SELECT lead,a,g,n,active FROM public.row_copy ORDER BY lead", []string{"lead", "a", "g", "n", "active"}, []string{"TEXT", "INT4", "INT4", "INT4", "BOOL"}, [][]any{{"after", int64(7), int64(14), int64(1), true}, {"x", int64(6), int64(12), int64(2), true}, {"y", int64(5), int64(10), int64(0), false}}))
				replay(referenceExec("UPDATE public.row_copy SET a=8 WHERE lead='after'", 1))
				replay(referenceSelect("SELECT g FROM public.row_copy WHERE lead='after'", []string{"g"}, []string{"INT4"}, [][]any{{int64(16)}}))
				replay(tableLikeError("UPDATE public.row_copy SET active=true WHERE lead='y'", referenceDiagnostic{Code: "22012", Message: "division by zero"}))
				replay(tableLikeError("INSERT INTO public.row_copy(lead,a,n,active) VALUES('bad',-1,1,false)", referenceDiagnostic{Code: "23514", Message: `new row for relation "row_copy" violates check constraint "positive"`, Detail: "Failing row contains (bad, -1, -2, 1, f).", Schema: "public", Table: "row_copy", Constraint: "positive"}))
				if _, err := s.p.postgres.ExecContext(s.p.ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					s.t.Fatal(err)
				}
			}
		}
	})
}
