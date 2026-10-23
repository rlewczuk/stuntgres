package reference_test

import (
	"bytes"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceCastDefinitionIOVolatility(t *testing.T) {
	// Each direction has an independent generated-column rejection. In
	// particular, a stable output codec cannot conceal a missing input classifier.
	for _, tc := range []struct {
		name, source, target, input, expression, typ string
		want                                         any
		setup                                        []string
		builtinPair                                  bool
	}{
		{"enum_enum", "cast_src", "cast_dst", "red", "(s::cast_dst)::text", "TEXT", "red", []string{"CREATE TYPE cast_src AS ENUM ('red')", "CREATE TYPE cast_dst AS ENUM ('red')"}, false},
		{"enum_output", "cast_src", "text", "red", "s::text", "TEXT", "red", []string{"CREATE TYPE cast_src AS ENUM ('red')"}, false},
		{"enum_input", "text", "cast_dst", "red", "(s::cast_dst)::text", "TEXT", "red", []string{"CREATE TYPE cast_dst AS ENUM ('red')"}, false},
		{"composite_output", "cast_record", "text", "(2001-02-03)", "s::text", "TEXT", "(2001-02-03)", []string{"CREATE TYPE cast_record AS (d date)"}, false},
		{"composite_input", "text", "cast_record", "(2001-02-03)", "((s::cast_record).d)::text", "TEXT", "2001-02-03", []string{"CREATE TYPE cast_record AS (d date)"}, false},
		{"range_output", "int4range", "text", "[1,4)", "s::text", "TEXT", "[1,4)", nil, true},
		{"range_input", "text", "int4range", "[1,4)", "lower(s::int4range)", "INT4", int64(1), nil, true},
		{"multirange_output", "int4multirange", "text", "{[1,4)}", "s::text", "TEXT", "{[1,4)}", nil, true},
		{"multirange_input", "text", "int4multirange", "{[1,4)}", "lower(s::int4multirange)", "INT4", int64(1), nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					conn, err := backend.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
					run(fixture.Exec("SET DateStyle='ISO, MDY'", 0))
					for _, q := range tc.setup {
						run(fixture.Exec(q, 0))
					}
					if tc.builtinPair {
						castDefinitionCleanup(t, p, backend, [2]string{tc.source, tc.target})
					}
					run(fixture.Exec("CREATE CAST ("+tc.source+" AS "+tc.target+") WITH INOUT", 0))
					run(fixture.Exec("CREATE TABLE codec_source(s "+tc.source+")", 0))
					run(fixture.Exec("INSERT INTO codec_source VALUES ($1)", 1, tc.input))
					q := "SELECT " + tc.expression + " AS v FROM codec_source"
					check := fixture.Select(q, []string{"v"}, []string{tc.typ}, [][]any{{tc.want}})
					run(check)
					run(fixture.Prepared(check))
					stmt, err := conn.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}, backend.Recorder, check)
					if err := stmt.Close(); err != nil {
						t.Fatal(err)
					}
					generated := "CREATE TABLE codec_generated(s " + tc.source + ",g " + tc.target + " GENERATED ALWAYS AS (s::" + tc.target + ") STORED)"
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic(generated, fixture.ControlsReferenceDiagnostic("42P17", "generation expression is not immutable"))
						s.Prepared, s.ErrorStage = prepared, "execute"
						run(s)
						run(fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='codec_generated'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
					}
					run(fixture.Exec("CREATE VIEW codec_retained AS SELECT "+tc.expression+" AS v FROM codec_source", 0))
					stored := fixture.Select("SELECT v FROM codec_retained", []string{"v"}, []string{tc.typ}, [][]any{{tc.want}})
					run(stored)
					if !backend.PG {
						var dump bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
							t.Fatalf("codec IO Restore: %v\n%s", err, dump.String())
						}
						run(fixture.Prepared(stored))
						s := fixture.ErrorDiagnostic(generated, fixture.ControlsReferenceDiagnostic("42P17", "generation expression is not immutable"))
						s.ErrorStage = "execute"
						run(s)
						if p.Postgres != nil {
							pg := fixture.Backend{DB: p.Postgres, Recorder: p.PostgresRecord, PG: true}
							if tc.builtinPair {
								castDefinitionCleanup(t, p, pg, [2]string{tc.source, tc.target})
							}
							if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
								t.Fatal(err)
							}
							if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
								t.Fatalf("actual codec IO SQL PostgreSQL replay: %v\n%s", err, dump.String())
							}
							fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, stored)
							fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s)
							if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
								t.Fatal(err)
							}
							if tc.builtinPair {
								if _, err := p.Postgres.ExecContext(p.Ctx, "DROP CAST ("+tc.source+" AS "+tc.target+")"); err != nil {
									t.Fatal(err)
								}
							}
						}
					}
				})
			}
		})
	}

	t.Run("composite_session_output", func(t *testing.T) {
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
					"SET DateStyle='ISO, MDY'",
					"CREATE TYPE codec_calendar AS (d date,t timestamp)",
					"CREATE CAST (codec_calendar AS text) WITH INOUT",
					"CREATE TABLE codec_dates(id integer,c codec_calendar)",
				} {
					run(fixture.Exec(q, 0))
				}
				run(fixture.Exec("INSERT INTO codec_dates VALUES (1,ROW('2001-02-03'::date,'2001-02-03 04:05:06'::timestamp)::codec_calendar),(2,ROW('2004-11-12'::date,'2004-11-12 13:14:15'::timestamp)::codec_calendar)", 2))
				run(fixture.Exec("CREATE VIEW codec_calendar_view AS SELECT id,c::text AS v FROM codec_dates", 0))
				q := "SELECT c::text AS v FROM codec_dates ORDER BY id"
				stmt, err := conn.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
				for _, tc := range []struct {
					style string
					rows  [][]any
				}{
					{"ISO, MDY", [][]any{{`(2001-02-03,"2001-02-03 04:05:06")`}, {`(2004-11-12,"2004-11-12 13:14:15")`}}},
					{"SQL, DMY", [][]any{{`(03/02/2001,"03/02/2001 04:05:06")`}, {`(12/11/2004,"12/11/2004 13:14:15")`}}},
				} {
					run(fixture.Exec("SET DateStyle='"+tc.style+"'", 0))
					s := fixture.Select(q, []string{"v"}, []string{"TEXT"}, tc.rows)
					run(s)
					run(fixture.Prepared(s))
					fixture.RunStep(t, p.Ctx, held, backend.Recorder, s)
					run(fixture.Select("SELECT v FROM codec_calendar_view ORDER BY id", []string{"v"}, []string{"TEXT"}, tc.rows))
				}
				if err := stmt.Close(); err != nil {
					t.Fatal(err)
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
						t.Fatalf("calendar IO Restore: %v\n%s", err, dump.String())
					}
					for _, style := range []struct {
						setting string
						rows    [][]any
					}{
						{"ISO, MDY", [][]any{{`(2001-02-03,"2001-02-03 04:05:06")`}, {`(2004-11-12,"2004-11-12 13:14:15")`}}},
						{"SQL, DMY", [][]any{{`(03/02/2001,"03/02/2001 04:05:06")`}, {`(12/11/2004,"12/11/2004 13:14:15")`}}},
					} {
						run(fixture.Exec("SET DateStyle='"+style.setting+"'", 0))
						run(fixture.Prepared(fixture.Select("SELECT v FROM codec_calendar_view ORDER BY id", []string{"v"}, []string{"TEXT"}, style.rows)))
					}
					if p.Postgres != nil {
						pgConn, err := p.Postgres.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer pgConn.Close()
						if _, err := pgConn.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
							t.Fatal(err)
						}
						if _, err := pgConn.ExecContext(p.Ctx, dump.String()); err != nil {
							t.Fatalf("actual calendar SQL PostgreSQL replay: %v\n%s", err, dump.String())
						}
						for _, style := range []struct {
							setting string
							rows    [][]any
						}{
							{"ISO, MDY", [][]any{{`(2001-02-03,"2001-02-03 04:05:06")`}, {`(2004-11-12,"2004-11-12 13:14:15")`}}},
							{"SQL, DMY", [][]any{{`(03/02/2001,"03/02/2001 04:05:06")`}, {`(12/11/2004,"12/11/2004 13:14:15")`}}},
						} {
							fixture.RunStep(t, p.Ctx, pgConn, p.PostgresRecord, fixture.Exec("SET DateStyle='"+style.setting+"'", 0))
							fixture.RunStep(t, p.Ctx, pgConn, p.PostgresRecord, fixture.Select("SELECT v FROM codec_calendar_view ORDER BY id", []string{"v"}, []string{"TEXT"}, style.rows))
						}
						if _, err := pgConn.ExecContext(p.Ctx, "RESET ALL; DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
							t.Fatal(err)
						}
					}
				}
			})
		}
	})

	t.Run("immutable_xid_real_control", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, backend := range p.Backends() {
			t.Run(backend.Name, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
				castDefinitionCleanup(t, p, backend, [2]string{"xid", "real"}, [2]string{"real", "xid"})
				for _, q := range []string{
					"CREATE CAST (xid AS real) WITH INOUT",
					"CREATE CAST (real AS xid) WITHOUT FUNCTION",
					"CREATE TABLE codec_immutable(s xid,g real GENERATED ALWAYS AS (s::real) STORED)",
				} {
					run(fixture.Exec(q, 0))
				}
				run(fixture.Exec("INSERT INTO codec_immutable(s) VALUES ('42'),(NULL)", 2))
				run(fixture.Select("SELECT g,g::xid::text AS word FROM codec_immutable ORDER BY s::text NULLS LAST", []string{"g", "word"}, []string{"FLOAT4", "TEXT"}, [][]any{{float64(42), "1109917696"}, {nil, nil}}))
				// Preserve the existing immutable old-binary/new-INOUT epoch proof.
				run(fixture.Exec("DROP CAST (xid AS real)", 0))
				run(fixture.Exec("CREATE CAST (xid AS real) WITHOUT FUNCTION", 0))
				run(fixture.Exec("CREATE TABLE codec_epoch(s xid,a real GENERATED ALWAYS AS (s::real) STORED,b real GENERATED ALWAYS AS (s::real) STORED)", 0))
				run(fixture.Exec("DROP CAST (xid AS real)", 0))
				run(fixture.Exec("CREATE CAST (xid AS real) WITH INOUT", 0))
				run(fixture.Exec("ALTER TABLE codec_epoch ALTER COLUMN b SET EXPRESSION AS (s::real)", 0))
				run(fixture.Exec("INSERT INTO codec_epoch(s) VALUES ('42')", 1))
				run(fixture.Select("SELECT a::xid::text AS old_word,b::xid::text AS new_word FROM codec_epoch", []string{"old_word", "new_word"}, []string{"TEXT", "TEXT"}, [][]any{{"42", "1109917696"}}))
			})
		}
	})
}
