package reference_test

import (
	"bytes"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceCastDefinitionIOTypmod(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
			for _, q := range []string{
				"CREATE TYPE cast_src AS ENUM ('red','éclair')",
				"CREATE CAST (cast_src AS varchar) WITH INOUT AS ASSIGNMENT",
				"CREATE CAST (cast_src AS bpchar) WITH INOUT AS ASSIGNMENT",
				"CREATE TABLE io_varchar(v varchar(2))",
				"CREATE TABLE io_bpchar(v character(2))",
				"CREATE TABLE io_array(v varchar(2)[])",
			} {
				run(fixture.Exec(q, 0))
			}
			for _, tc := range []struct {
				name, expression, typ string
				input, want           any
			}{
				{"varchar", "($1::cast_src)::varchar(2)", "VARCHAR", "red", "re"},
				{"bpchar", "($1::cast_src)::character(2)", "BPCHAR", "red", "re"},
				{"utf8", "($1::cast_src)::varchar(1)", "VARCHAR", "éclair", "é"},
				{"unconstrained", "($1::cast_src)::varchar", "VARCHAR", "red", "red"},
				{"null", "($1::cast_src)::varchar(2)", "VARCHAR", nil, nil},
				{"bpchar_null", "($1::cast_src)::character(2)", "BPCHAR", nil, nil},
				{"array", "(array_fill($1::cast_src,ARRAY[1])::varchar(2)[])[1]", "VARCHAR", "red", "re"},
				{"bpchar_array", "(array_fill($1::cast_src,ARRAY[1])::character(2)[])[1]", "BPCHAR", "red", "re"},
				{"utf8_array", "(array_fill($1::cast_src,ARRAY[1])::varchar(1)[])[1]", "VARCHAR", "éclair", "é"},
				{"array_null", "(array_fill($1::cast_src,ARRAY[1])::varchar(2)[])[1]", "VARCHAR", nil, nil},
			} {
				t.Run(tc.name, func(t *testing.T) {
					q := "SELECT " + tc.expression + " AS v"
					s := fixture.Select(q, []string{"v"}, []string{tc.typ}, [][]any{{tc.want}}, tc.input)
					fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
					fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Prepared(s))
					stmt, err := conn.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
					for range 2 {
						fixture.RunStep(t, p.Ctx, held, backend.Recorder, s)
					}
				})
			}
			for _, tc := range []struct{ name, query, message, table string }{
				{"varchar", "INSERT INTO io_varchar VALUES (NULL),('red'::cast_src)", "value too long for type character varying(2)", "io_varchar"},
				{"bpchar", "INSERT INTO io_bpchar VALUES (NULL),('red'::cast_src)", "value too long for type character(2)", "io_bpchar"},
				{"array", "INSERT INTO io_array VALUES (array_fill(NULL::cast_src,ARRAY[1])),(array_fill('red'::cast_src,ARRAY[1]))", "value too long for type character varying(2)", "io_array"},
			} {
				t.Run("assignment_"+tc.name, func(t *testing.T) {
					for _, prepared := range []bool{false, true} {
						// Full fields, including absent Position/Context, are oracle
						// candidates. Discovery reports rather than ignores differences.
						s := fixture.ErrorDiagnostic(tc.query, fixture.ControlsReferenceDiagnostic("22001", tc.message))
						s.Prepared, s.ErrorStage = prepared, "execute"
						fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
						fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select("SELECT count(*) AS n FROM "+tc.table, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
					}
				})
			}
			for _, q := range []string{
				"CREATE VIEW io_sized_scalar AS SELECT ('red'::cast_src)::varchar(2) AS v,('éclair'::cast_src)::character(1) AS u",
				"CREATE VIEW io_sized_array AS SELECT array_cat(array_fill('red'::cast_src,ARRAY[1],ARRAY[0]),array_fill(NULL::cast_src,ARRAY[1]))::varchar(2)[] AS v",
				"CREATE VIEW io_resized_array AS SELECT v::character(1)[] AS v FROM io_sized_array",
				"CREATE TABLE io_defaults(v varchar(2) DEFAULT (('red'::cast_src)::varchar(2)),a varchar(1)[] DEFAULT (array_fill('éclair'::cast_src,ARRAY[1],ARRAY[0])::varchar(1)[]))",
				"DROP CAST (cast_src AS varchar)",
				"DROP CAST (cast_src AS bpchar)",
			} {
				run(fixture.Exec(q, 0))
			}
			checks := []fixture.Step{
				fixture.Select("SELECT v,u FROM io_sized_scalar", []string{"v", "u"}, []string{"VARCHAR", "BPCHAR"}, [][]any{{"re", "é"}}),
				fixture.Select("SELECT array_lower(v,1) AS lo,array_upper(v,1) AS hi,v[0] AS v,v[1] AS null_leaf FROM io_sized_array", []string{"lo", "hi", "v", "null_leaf"}, []string{"INT4", "INT4", "VARCHAR", "VARCHAR"}, [][]any{{int64(0), int64(1), "re", nil}}),
				fixture.Select("SELECT array_lower(v,1) AS lo,v[0] AS v,v[1] AS null_leaf FROM io_resized_array", []string{"lo", "v", "null_leaf"}, []string{"INT4", "BPCHAR", "BPCHAR"}, [][]any{{int64(0), "r", nil}}),
				fixture.Select("SELECT c.relname AS name,a.atttypmod AS typmod FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid WHERE c.relname IN ('io_sized_array','io_resized_array') AND a.attname='v' ORDER BY c.relname", []string{"name", "typmod"}, []string{"NAME", "INT4"}, [][]any{{"io_resized_array", int64(5)}, {"io_sized_array", int64(6)}}),
			}
			for _, s := range checks {
				run(s)
				run(fixture.Prepared(s))
			}
			if !backend.PG {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatalf("sized IO Restore: %v\n%s", err, dump.String())
				}
				for _, s := range checks {
					run(fixture.Prepared(s))
				}
				if p.Postgres != nil {
					// No global builtin pairs: public reset removes both custom
					// endpoints and any temporary replay cast windows.
					if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
						t.Fatal(err)
					}
					if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
						t.Fatalf("actual sized IO SQL PostgreSQL replay: %v\n%s", err, dump.String())
					}
					for _, s := range checks {
						fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s)
					}
					fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec("INSERT INTO io_defaults DEFAULT VALUES", 1))
					fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Select("SELECT v,array_lower(a,1) AS lo,a[0] AS u FROM io_defaults", []string{"v", "lo", "u"}, []string{"VARCHAR", "INT4", "VARCHAR"}, [][]any{{"re", int64(0), "é"}}))
					if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
						t.Fatal(err)
					}
				}
			}
			run(fixture.Exec("INSERT INTO io_defaults DEFAULT VALUES", 1))
			run(fixture.Select("SELECT v,array_lower(a,1) AS lo,a[0] AS u FROM io_defaults", []string{"v", "lo", "u"}, []string{"VARCHAR", "INT4", "VARCHAR"}, [][]any{{"re", int64(0), "é"}}))
		})
	}
}
