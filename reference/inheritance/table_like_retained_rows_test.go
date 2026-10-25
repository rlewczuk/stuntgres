package reference_test

import (
	"bytes"
	"fmt"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTableLikeTemporaryRetainedRows(t *testing.T) {
	fixture.TemporaryTableScenario(t, func(s *fixture.TempReferenceSession) {
		run := func(step fixture.Step) {
			step.Prepared = s.Prepared
			fixture.RunStep(s.T, s.P.Ctx, s.C, s.Record, step)
		}
		s.Utility("CREATE TABLE public.carrier(v INT)", nil)
		run(fixture.Exec("INSERT INTO public.carrier VALUES(7)", 1))
		s.Utility("CREATE TEMP TABLE row_source(dead TEXT,a INT NOT NULL,g INT GENERATED ALWAYS AS(a*2) STORED,n INT,active BOOL,CONSTRAINT positive CHECK(a>0))", nil)
		s.Utility("ALTER TABLE row_source DROP COLUMN dead", nil)
		s.Utility("CREATE INDEX row_expression ON row_source((a+1))", nil)
		s.Utility("CREATE INDEX row_partial ON row_source((10/n)) WHERE active", nil)
		run(fixture.Exec("INSERT INTO row_source(a,n,active) VALUES(3,2,true)", 1))
		if !s.Postgres {
			var dump bytes.Buffer
			if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &dump); err != nil {
				s.T.Fatal(err)
			}
			if err := stuntgres.Reset(s.P.Ctx, s.P.Native); err != nil {
				s.T.Fatal(err)
			}
			if err := stuntgres.Restore(s.P.Ctx, s.P.Native, bytes.NewReader(dump.Bytes())); err != nil {
				s.T.Fatal(err)
			}
		}
		sourceRows := fixture.Select("SELECT * FROM row_source", []string{"a", "g", "n", "active"}, []string{"INT4", "INT4", "INT4", "BOOL"}, [][]any{{int64(3), int64(6), int64(2), true}})
		run(sourceRows)
		for _, temporary := range []bool{false, true} {
			prefix, name, schema := "", "public.row_copy", "public"
			if temporary {
				prefix, name = "TEMP ", "row_temp_copy"
				s.Exec(s.C, "SET search_path TO pg_temp,public", 0)
				schema = fixture.TempReferenceNamespace(s.T, s, false)
				s.Exec(s.C, "SET search_path TO public", 0)
			}
			table := map[bool]string{false: "row_copy", true: "row_temp_copy"}[temporary]
			s.Utility("CREATE "+prefix+"TABLE "+name+"(lead TEXT,LIKE pg_temp.row_source INCLUDING ALL)", nil)
			run(fixture.Select("SELECT * FROM "+name, []string{"lead", "a", "g", "n", "active"}, []string{"TEXT", "INT4", "INT4", "INT4", "BOOL"}, nil))
			fixture.TableDDLMetadata(s.T, s.P.Ctx, s.C, s.Postgres, s.Prepared, "SELECT * FROM "+name, []string{"lead", "a", "g", "n", "active"}, []string{"TEXT", "INT4", "INT4", "INT4", "BOOL"}, []bool{true, false, true, true, true}, 0)
			run(fixture.Exec("INSERT INTO "+name+"(lead,a,n,active) VALUES('x',4,2,true),('y',5,0,false)", 2))
			run(fixture.Exec("UPDATE "+name+" SET a=6 WHERE lead='x'", 1))
			want := fixture.Select("SELECT * FROM "+name+" ORDER BY lead", []string{"lead", "a", "g", "n", "active"}, []string{"TEXT", "INT4", "INT4", "INT4", "BOOL"}, [][]any{{"x", int64(6), int64(12), int64(2), true}, {"y", int64(5), int64(10), int64(0), false}})
			run(want)
			run(fixture.TableLikeError("INSERT INTO "+name+"(lead,a,n,active) VALUES('bad',-1,1,false)", fixture.Diagnostic{Code: "23514", Message: fmt.Sprintf(`new row for relation %q violates check constraint "positive"`, map[bool]string{false: "row_copy", true: "row_temp_copy"}[temporary]), Detail: "Failing row contains (bad, -1, -2, 1, f).", Schema: schema, Table: map[bool]string{false: "row_copy", true: "row_temp_copy"}[temporary], Constraint: "positive"}))
			run(fixture.TableLikeError("UPDATE "+name+" SET active=true WHERE lead='y'", fixture.Diagnostic{Code: "22012", Message: "division by zero"}))
			run(want)
			renderSchema := schema
			if temporary && s.Postgres {
				// PostgreSQL renders the owner's physical TEMP namespace through
				// pg_temp; the catalog schema and diagnostic still name its identity.
				renderSchema = "pg_temp"
			}
			run(fixture.Select("SELECT indexname,indexdef FROM pg_indexes WHERE schemaname='"+schema+"' AND tablename='"+table+"' ORDER BY indexname", []string{"indexname", "indexdef"}, []string{"NAME", "TEXT"}, [][]any{{table + "_expr_idx", "CREATE INDEX " + table + "_expr_idx ON " + renderSchema + "." + table + " USING btree (((a + 1)))"}, {table + "_expr_idx1", "CREATE INDEX " + table + "_expr_idx1 ON " + renderSchema + "." + table + " USING btree (((10 / n))) WHERE active"}}))
			run(fixture.TableLikeError("CREATE "+prefix+"TABLE row_failed(LIKE pg_temp.row_source INCLUDING ALL,a INT)", fixture.Diagnostic{Code: "42701", Message: `column "a" specified more than once`}))
			fixture.TableLikeAbsent(run, "row_failed", s.Prepared)
			s.Utility("CREATE "+prefix+"TABLE row_failed(LIKE pg_temp.row_source INCLUDING ALL EXCLUDING GENERATED EXCLUDING CONSTRAINTS EXCLUDING INDEXES)", nil)
			run(fixture.Exec("INSERT INTO row_failed(a,g,n,active) VALUES(-1,17,0,true)", 1))
			run(fixture.Select("SELECT * FROM row_failed", []string{"a", "g", "n", "active"}, []string{"INT4", "INT4", "INT4", "BOOL"}, [][]any{{int64(-1), int64(17), int64(0), true}}))
			run(fixture.Select("SELECT count(*) AS n FROM pg_indexes WHERE schemaname='"+schema+"' AND tablename='row_failed'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			s.Utility("DROP TABLE row_failed", nil)
			run(sourceRows)
		}
		s.Utility("DROP TABLE pg_temp.row_source", nil)
		for _, name := range []string{"public.row_copy", "row_temp_copy"} {
			run(fixture.Exec("INSERT INTO "+name+"(lead,a,n,active) VALUES('after',7,1,true)", 1))
			run(fixture.Select("SELECT g FROM "+name+" WHERE lead='after'", []string{"g"}, []string{"INT4"}, [][]any{{int64(14)}}))
		}
		if !s.Postgres {
			var dump bytes.Buffer
			if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &dump); err != nil {
				s.T.Fatal(err)
			}
			if err := stuntgres.Reset(s.P.Ctx, s.P.Native); err != nil {
				s.T.Fatal(err)
			}
			if err := stuntgres.Restore(s.P.Ctx, s.P.Native, bytes.NewReader(dump.Bytes())); err != nil {
				s.T.Fatal(err)
			}
			fixture.TableLikeAbsent(run, "pg_temp.row_source", s.Prepared)
			for _, name := range []string{"public.row_copy", "row_temp_copy"} {
				run(fixture.Select("SELECT * FROM "+name+" ORDER BY lead", []string{"lead", "a", "g", "n", "active"}, []string{"TEXT", "INT4", "INT4", "INT4", "BOOL"}, [][]any{{"after", int64(7), int64(14), int64(1), true}, {"x", int64(6), int64(12), int64(2), true}, {"y", int64(5), int64(10), int64(0), false}}))
				fixture.TableDDLMetadata(s.T, s.P.Ctx, s.C, false, s.Prepared, "SELECT * FROM "+name, []string{"lead", "a", "g", "n", "active"}, []string{"TEXT", "INT4", "INT4", "INT4", "BOOL"}, []bool{true, false, true, true, true}, 3)
				run(fixture.Exec("UPDATE "+name+" SET a=8 WHERE lead='after'", 1))
				run(fixture.Select("SELECT g FROM "+name+" WHERE lead='after'", []string{"g"}, []string{"INT4"}, [][]any{{int64(16)}}))
				run(fixture.Exec("INSERT INTO "+name+"(lead,a,n,active) VALUES('zero',9,0,false)", 1))
				run(fixture.TableLikeError("UPDATE "+name+" SET active=true WHERE lead='zero'", fixture.Diagnostic{Code: "22012", Message: "division by zero"}))
				run(fixture.Select("SELECT g,active FROM "+name+" WHERE lead='zero'", []string{"g", "active"}, []string{"INT4", "BOOL"}, [][]any{{int64(18), false}}))
				schema, table := "public", "row_copy"
				if name == "row_temp_copy" {
					s.Exec(s.C, "SET search_path TO pg_temp,public", 0)
					schema = fixture.TempReferenceNamespace(s.T, s, false)
					s.Exec(s.C, "SET search_path TO public", 0)
					table = "row_temp_copy"
				}
				run(fixture.TableLikeError("INSERT INTO "+name+"(lead,a,n,active) VALUES('bad',-1,1,false)", fixture.Diagnostic{Code: "23514", Message: fmt.Sprintf(`new row for relation %q violates check constraint "positive"`, table), Detail: "Failing row contains (bad, -1, -2, 1, f).", Schema: schema, Table: table, Constraint: "positive"}))
				run(fixture.Select("SELECT count(*) AS n FROM "+name, []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
			}
			if s.P.Postgres != nil {
				if _, err := s.P.Postgres.ExecContext(s.P.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					s.T.Fatal(err)
				}
				if _, err := s.P.Postgres.ExecContext(s.P.Ctx, dump.String()); err != nil {
					s.T.Fatal("actual retained row-copy SQL replay", err)
				}
				replay := func(step fixture.Step) {
					step.Prepared = s.Prepared
					fixture.RunStep(s.T, s.P.Ctx, s.P.Postgres, s.P.PostgresRecord, step)
				}
				replay(fixture.Select("SELECT lead,a,g,n,active FROM public.row_copy ORDER BY lead", []string{"lead", "a", "g", "n", "active"}, []string{"TEXT", "INT4", "INT4", "INT4", "BOOL"}, [][]any{{"after", int64(7), int64(14), int64(1), true}, {"x", int64(6), int64(12), int64(2), true}, {"y", int64(5), int64(10), int64(0), false}}))
				replay(fixture.Exec("UPDATE public.row_copy SET a=8 WHERE lead='after'", 1))
				replay(fixture.Select("SELECT g FROM public.row_copy WHERE lead='after'", []string{"g"}, []string{"INT4"}, [][]any{{int64(16)}}))
				replay(fixture.TableLikeError("UPDATE public.row_copy SET active=true WHERE lead='y'", fixture.Diagnostic{Code: "22012", Message: "division by zero"}))
				replay(fixture.TableLikeError("INSERT INTO public.row_copy(lead,a,n,active) VALUES('bad',-1,1,false)", fixture.Diagnostic{Code: "23514", Message: `new row for relation "row_copy" violates check constraint "positive"`, Detail: "Failing row contains (bad, -1, -2, 1, f).", Schema: "public", Table: "row_copy", Constraint: "positive"}))
				if _, err := s.P.Postgres.ExecContext(s.P.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					s.T.Fatal(err)
				}
			}
		}
	})
}
