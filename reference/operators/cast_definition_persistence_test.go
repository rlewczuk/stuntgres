package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceCastDefinitionPersistence(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			castDefinitionCleanup(t, p, backend, [2]string{"xid", "real"})
			for _, q := range []string{castDefinitionSource, castDefinitionScore, castDefinitionEdge + " AS ASSIGNMENT", "CREATE TYPE cast_other AS ENUM ('red')", "CREATE CAST (cast_src AS cast_other) WITH INOUT", "CREATE CAST (xid AS real) WITHOUT FUNCTION", "CREATE TABLE cast_saved(v integer DEFAULT (('blue'::cast_src)::integer))", "INSERT INTO cast_saved VALUES (('red'::cast_src)::integer)", "CREATE VIEW cast_saved_view AS SELECT ('red'::cast_src)::integer AS v"} {
				n := int64(0)
				if strings.HasPrefix(q, "INSERT") {
					n = 1
				}
				run(fixture.Exec(q, n))
			}
			for _, q := range []string{
				"CREATE TYPE cast_replay_other AS ENUM ('red')",
				"CREATE CAST (cast_src AS cast_replay_other) WITH INOUT",
				"CREATE VIEW cast_dropped_io AS SELECT ('red'::cast_src)::cast_replay_other AS v",
				"DROP CAST (cast_src AS cast_replay_other)",
				"CREATE TYPE cast_array_src AS ENUM ('red','blue')",
				`CREATE FUNCTION cast_array_old(cast_array_src) RETURNS integer LANGUAGE SQL AS $$ SELECT CASE WHEN $1 IS NULL THEN 99 WHEN $1::text='red' THEN 41 ELSE 42 END $$`,
				"CREATE CAST (cast_array_src AS integer) WITH FUNCTION cast_array_old(cast_array_src)",
				"CREATE VIEW cast_array_frozen AS SELECT (array_cat(array_cat(array_fill('red'::cast_array_src,ARRAY[1],ARRAY[0]),array_fill(NULL::cast_array_src,ARRAY[1])),array_fill('blue'::cast_array_src,ARRAY[1])))::integer[] AS v",
				"DROP CAST (cast_array_src AS integer)",
				`CREATE FUNCTION cast_array_new(cast_array_src) RETURNS integer LANGUAGE SQL AS $$ SELECT CASE WHEN $1 IS NULL THEN 88 ELSE 77 END $$`,
				"CREATE CAST (cast_array_src AS integer) WITH FUNCTION cast_array_new(cast_array_src)",
			} {
				run(fixture.Exec(q, 0))
			}
			for _, s := range castDefinitionFrozenReplayChecks() {
				run(s)
			}
			if !backend.PG {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				run(fixture.Select("SELECT count(*) AS n FROM pg_cast WHERE castsource='xid'::regtype AND casttarget='real'::regtype", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				for _, s := range castDefinitionFrozenReplayChecks() {
					run(fixture.Prepared(s))
				}
				// Actual emitted bytes, not an independently authored replay surrogate.
				if p.Postgres != nil {
					castDefinitionCleanup(t, p, fixture.Backend{DB: p.Postgres, Recorder: p.PostgresRecord, PG: true}, [2]string{"xid", "real"})
					if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
						t.Fatal(err)
					}
					if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
						t.Fatalf("native SQL replay on PostgreSQL: %v\n%s", err, dump.String())
					}
					var value int64
					if err := p.Postgres.QueryRowContext(p.Ctx, "SELECT v FROM cast_saved").Scan(&value); err != nil || value != 41 {
						t.Fatalf("native dump PG value=%d error=%v", value, err)
					}
					for _, s := range castDefinitionFrozenReplayChecks() {
						fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s)
					}
					if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
						t.Fatal(err)
					}
					if _, err := p.Postgres.ExecContext(p.Ctx, "DROP CAST (xid AS real)"); err != nil {
						t.Fatal(err)
					}
				}
				// Late invalid DDL and cancellation must preserve the installed graph.
				bad := append(append([]byte(nil), dump.Bytes()...), []byte("\nCREATE CAST (xid AS real) WITH FUNCTION missing_cast_function(xid);\n")...)
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(bad)); err == nil {
					t.Fatal("late invalid CREATE CAST installed partial replacement")
				}
				ctx, cancel := context.WithCancel(p.Ctx)
				err := stuntgres.Restore(ctx, p.Native, &fixture.EnumCancelReader{Reader: bytes.NewReader(dump.Bytes()), Cancel: cancel})
				cancel()
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("late canceled Restore: %v", err)
				}
			}
			for _, prepared := range []bool{false, true} {
				for _, tc := range []struct {
					in  any
					out int64
				}{{"red", 41}, {"blue", 42}, {nil, 99}} {
					s := fixture.Select("SELECT ($1::cast_src)::integer AS v", []string{"v"}, []string{"INT4"}, [][]any{{tc.out}}, tc.in)
					s.Prepared = prepared
					run(s)
				}
			}
			run(fixture.Select("SELECT v FROM cast_saved_view", []string{"v"}, []string{"INT4"}, [][]any{{int64(41)}}))
			run(fixture.Exec("INSERT INTO cast_saved DEFAULT VALUES", 1))
			run(fixture.Select("SELECT v FROM cast_saved ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(41)}, {int64(42)}}))
			run(fixture.Select("SELECT ('1065353216'::xid)::real AS v", []string{"v"}, []string{"FLOAT4"}, [][]any{{float64(1)}}))
		})
	}
}

func castDefinitionFrozenReplayChecks() []fixture.Step {
	return []fixture.Step{
		fixture.Select("SELECT v::text AS v FROM cast_dropped_io", []string{"v"}, []string{"TEXT"}, [][]any{{"red"}}),
		fixture.Select("SELECT array_lower(v,1) AS lo,v[0] AS first,v[1] AS null_element,v[2] AS last FROM cast_array_frozen", []string{"lo", "first", "null_element", "last"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(0), int64(41), int64(99), int64(42)}}),
		fixture.Select("SELECT ('red'::cast_array_src)::integer AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(77)}}),
		fixture.Select("SELECT count(*) AS n FROM pg_cast WHERE castsource='cast_src'::regtype AND casttarget='cast_replay_other'::regtype", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}),
		fixture.Select("SELECT count(*) AS n FROM pg_cast WHERE castsource='cast_array_src'::regtype AND casttarget='integer'::regtype AND castfunc='cast_array_new(cast_array_src)'::regprocedure::oid AND castmethod::text='f' AND castcontext::text='e'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}),
	}
}

func TestReferenceCastDefinitionRawFloat4Persistence(t *testing.T) {
	for _, final := range []string{"absent", "binary", "function", "inout"} {
		t.Run(final, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
					castDefinitionCleanup(t, p, backend, [2]string{"real", "xid"}, [2]string{"xid", "real"})
					for _, q := range []string{"CREATE CAST (xid AS real) WITHOUT FUNCTION", "CREATE CAST (real AS xid) WITHOUT FUNCTION", "CREATE TYPE cast_payload AS (v real)", "CREATE TABLE raw_saved(id integer,v real,a real[],c cast_payload)", "CREATE TABLE raw_default(v real DEFAULT (('2139095041'::xid)::real))", "CREATE VIEW raw_literal AS SELECT ('2143289345'::xid)::real AS v"} {
						run(fixture.Exec(q, 0))
					}
					words := []string{"0", "1065353216", "1", "2147483648", "2139095040", "2143289345", "2139095041"}
					for i, word := range words {
						run(fixture.Exec("INSERT INTO raw_saved VALUES ($1,($2::xid)::real,ARRAY[($2::xid)::real],ROW(($2::xid)::real)::cast_payload)", 1, int64(i), word))
					}
					if final != "binary" {
						run(fixture.Exec("DROP CAST (xid AS real)", 0))
					}
					if final == "function" {
						run(fixture.Exec("CREATE FUNCTION final_real(xid) RETURNS real LANGUAGE SQL AS $$ SELECT 7::real $$", 0))
						run(fixture.Exec("CREATE CAST (xid AS real) WITH FUNCTION final_real(xid)", 0))
					}
					if final == "inout" {
						run(fixture.Exec("CREATE CAST (xid AS real) WITH INOUT", 0))
					}
					q := "SELECT v::xid::text AS scalar,a[1]::xid::text AS array_leaf,(c).v::xid::text AS composite_leaf FROM raw_saved ORDER BY id"
					want := make([][]any, len(words))
					for i, word := range words {
						want[i] = []any{word, word, word}
					}
					run(fixture.Select(q, []string{"scalar", "array_leaf", "composite_leaf"}, []string{"TEXT", "TEXT", "TEXT"}, want))
					if !backend.PG {
						// Keep the real native cursor alive across replacement as well.
						old, err := backend.DB.QueryContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer old.Close()
						var dump bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
							t.Fatalf("raw32 Restore final=%s: %v\n%s", final, err, dump.String())
						}
						for i, word := range words {
							if !old.Next() {
								t.Fatalf("retained raw row %d: %v", i, old.Err())
							}
							var a, b, c string
							if err := old.Scan(&a, &b, &c); err != nil {
								t.Fatal(err)
							}
							if a != word || b != word || c != word {
								t.Fatalf("retained raw32=%q,%q,%q want %q", a, b, c, word)
							}
						}
						if old.Next() || old.Err() != nil {
							t.Fatalf("retained raw tail: %v", old.Err())
						}
						old.Close()
						if p.Postgres != nil {
							castDefinitionCleanup(t, p, fixture.Backend{DB: p.Postgres, Recorder: p.PostgresRecord, PG: true}, [2]string{"real", "xid"}, [2]string{"xid", "real"})
							if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
								t.Fatal(err)
							}
							if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
								t.Fatalf("actual raw32 SQL replay final=%s: %v\n%s", final, err, dump.String())
							}
							rows, err := p.Postgres.QueryContext(p.Ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							for _, word := range words {
								if !rows.Next() {
									rows.Close()
									t.Fatal("raw replay missing row")
								}
								var a, b, c string
								if err := rows.Scan(&a, &b, &c); err != nil {
									rows.Close()
									t.Fatal(err)
								}
								if a != word || b != word || c != word {
									rows.Close()
									t.Fatalf("PG replay raw32 %q,%q,%q want %q", a, b, c, word)
								}
							}
							if rows.Next() || rows.Err() != nil {
								rows.Close()
								t.Fatalf("raw replay tail: %v", rows.Err())
							}
							rows.Close()
							if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
								t.Fatal(err)
							}
							if _, err := p.Postgres.ExecContext(p.Ctx, "DROP CAST (real AS xid); DROP CAST IF EXISTS (xid AS real)"); err != nil {
								t.Fatal(err)
							}
						}
					}
					run(fixture.Prepared(fixture.Select(q, []string{"scalar", "array_leaf", "composite_leaf"}, []string{"TEXT", "TEXT", "TEXT"}, want)))
					run(fixture.Exec("INSERT INTO raw_default DEFAULT VALUES", 1))
					run(fixture.Select("SELECT v::xid::text AS v FROM raw_default", []string{"v"}, []string{"TEXT"}, [][]any{{"2139095041"}}))
					run(fixture.Select("SELECT v::xid::text AS v FROM raw_literal", []string{"v"}, []string{"TEXT"}, [][]any{{"2143289345"}}))
					if final == "absent" {
						run(fixture.Select("SELECT count(*) AS n FROM pg_cast WHERE castsource='xid'::regtype AND casttarget='real'::regtype", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
					} else {
						method := "b"
						if final == "function" {
							method = "f"
						}
						if final == "inout" {
							method = "i"
						}
						run(fixture.Select("SELECT castmethod::text AS method FROM pg_cast WHERE castsource='xid'::regtype AND casttarget='real'::regtype", []string{"method"}, []string{"TEXT"}, [][]any{{method}}))
					}
					if final == "function" {
						run(fixture.Select("SELECT ('1'::xid)::real AS v", []string{"v"}, []string{"FLOAT4"}, [][]any{{float64(7)}}))
					}
					if final == "inout" {
						run(fixture.Select("SELECT ('2'::xid)::real AS v", []string{"v"}, []string{"FLOAT4"}, [][]any{{float64(2)}}))
					}
					// Fresh arithmetic must not carry an operand's stale raw32 marker.
					run(fixture.Select("SELECT (v+1::real)::xid::text AS v FROM raw_saved WHERE id=1", []string{"v"}, []string{"TEXT"}, [][]any{{"1073741824"}}))
				})
			}
		})
	}
}

// A real INSERT target slice must retain nondefault bounds; ARRAY constructors
// alone would silently rebase this composite carrier during native replay.
func TestReferenceCastDefinitionRawCompositeArrayBounds(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			castDefinitionCleanup(t, p, backend, [2]string{"xid", "real"}, [2]string{"real", "xid"})
			for _, q := range []string{
				"CREATE CAST (xid AS real) WITHOUT FUNCTION",
				"CREATE CAST (real AS xid) WITHOUT FUNCTION",
				"CREATE TYPE cast_bound_payload AS (v real,a real[])",
				"CREATE TABLE cast_bound_saved(a cast_bound_payload[])",
				"INSERT INTO cast_bound_saved VALUES (array_cat(array_fill(ROW(('2139095041'::xid)::real,('[0:1]={2139095041,NULL}'::pg_catalog._xid)::real[])::cast_bound_payload,ARRAY[1,2],ARRAY[0,-2]),array_fill(ROW(('2143289345'::xid)::real,('[0:1]={2143289345,NULL}'::pg_catalog._xid)::real[])::cast_bound_payload,ARRAY[1,2],ARRAY[1,-2])))",
			} {
				count := int64(0)
				if len(q) > 6 && q[:6] == "INSERT" {
					count = 1
				}
				run(fixture.Exec(q, count))
			}
			q := "SELECT array_lower(a,1) AS lo1,array_lower(a,2) AS lo2,((a[0][-2]).v)::xid::text AS first,((a[1][-1]).v)::xid::text AS last,array_lower((a[0][-2]).a,1) AS nested_lo,(((a[0][-2]).a)[0])::xid::text AS nested_first,(((a[1][-1]).a)[0])::xid::text AS nested_last,(((a[0][-2]).a)[1]) IS NULL AS nested_null FROM cast_bound_saved"
			step := fixture.Select(q, []string{"lo1", "lo2", "first", "last", "nested_lo", "nested_first", "nested_last", "nested_null"}, []string{"INT4", "INT4", "TEXT", "TEXT", "INT4", "TEXT", "TEXT", "BOOL"}, [][]any{{int64(0), int64(-2), "2139095041", "2143289345", int64(0), "2139095041", "2143289345", true}})
			run(step)
			if !backend.PG {
				var script bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &script); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(script.Bytes())); err != nil {
					t.Fatalf("bounded raw replay: %v\n%s", err, script.String())
				}
				run(fixture.Prepared(step))
				if p.Postgres != nil {
					if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public; DROP CAST IF EXISTS (xid AS real); DROP CAST IF EXISTS (real AS xid)"); err != nil {
						t.Fatal(err)
					}
					if _, err := p.Postgres.ExecContext(p.Ctx, script.String()); err != nil {
						t.Fatalf("actual bounded raw SQL replay: %v\n%s", err, script.String())
					}
					fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, step)
					if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public; DROP CAST (xid AS real); DROP CAST (real AS xid)"); err != nil {
						t.Fatal(err)
					}
				}
			}
		})
	}
}

func TestReferenceCastDefinitionBuiltinInvariance(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			castDefinitionCleanup(t, p, backend, [2]string{"xid", "real"})
			for _, phase := range []string{"before", "after"} {
				if phase == "after" {
					run(fixture.Exec("CREATE CAST (xid AS real) WITHOUT FUNCTION", 0))
				}
				for _, prepared := range []bool{false, true} {
					for _, s := range []fixture.Step{
						fixture.Select("SELECT abs(-7::integer) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}),
						fixture.Select("SELECT length('abc'::text) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(3)}}),
						fixture.Select("SELECT coalesce(NULL::integer,42::integer) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}),
						fixture.Select("SELECT count(*) FILTER (WHERE v>1) AS v FROM (VALUES(1),(2),(3)) AS x(v)", []string{"v"}, []string{"INT8"}, [][]any{{int64(2)}}),
						fixture.Error("SELECT abs('x'::text)", "42883"),
					} {
						s.Prepared = prepared
						run(s)
					}
				}
			}
			for _, q := range []string{castDefinitionSource, castDefinitionScore, "CREATE CAST (cast_src AS integer) WITH FUNCTION cast_score(cast_src) AS IMPLICIT"} {
				run(fixture.Exec(q, 0))
			}
			for _, prepared := range []bool{false, true} {
				for _, tc := range []struct {
					sql  string
					want int64
				}{{"SELECT abs('red'::cast_src) AS v", 41}, {"SELECT abs(NULL::cast_src) AS v", 99}} {
					s := fixture.Select(tc.sql, []string{"v"}, []string{"INT4"}, [][]any{{tc.want}})
					s.Prepared = prepared
					run(s)
				}
			}
		})
	}
}

// The default's standard SQL provider reads its own table. Dump must defer the
// SET DEFAULT until that provider exists, and bind its historical IO at that
// actual statement, not at CREATE TABLE. NOT VALID checks bind after the data.
func TestReferenceCastDefinitionReplayPhases(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			castDefinitionCleanup(t, p, backend)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			for _, q := range []string{
				"CREATE TYPE cast_phase_src AS ENUM ('red','blue')",
				"CREATE TYPE cast_phase_dst AS ENUM ('red','blue')",
				"CREATE CAST (cast_phase_src AS cast_phase_dst) WITH INOUT",
				"CREATE TABLE cast_phase_saved(v cast_phase_dst)",
				"INSERT INTO cast_phase_saved VALUES ('red')",
				"CREATE FUNCTION cast_phase_default() RETURNS cast_phase_src LANGUAGE SQL BEGIN ATOMIC SELECT 'red'::cast_phase_src FROM cast_phase_saved LIMIT 1; END",
				"ALTER TABLE cast_phase_saved ALTER COLUMN v SET DEFAULT (cast_phase_default()::cast_phase_dst)",
				"ALTER TABLE cast_phase_saved ADD CONSTRAINT cast_phase_check CHECK (v::text = (('red'::cast_phase_src)::cast_phase_dst)::text) NOT VALID",
				"CREATE DOMAIN cast_phase_domain AS text",
				"ALTER DOMAIN cast_phase_domain ADD CONSTRAINT cast_phase_domain_check CHECK (VALUE = (('red'::cast_phase_src)::cast_phase_dst)::text) NOT VALID",
				"CREATE FUNCTION cast_phase_stored() RETURNS cast_phase_dst LANGUAGE SQL BEGIN ATOMIC SELECT ('red'::cast_phase_src)::cast_phase_dst; END",
				"DROP CAST (cast_phase_src AS cast_phase_dst)",
				"CREATE FUNCTION cast_phase_new(cast_phase_src) RETURNS cast_phase_dst LANGUAGE SQL AS $$ SELECT 'blue'::cast_phase_dst $$",
				"CREATE CAST (cast_phase_src AS cast_phase_dst) WITH FUNCTION cast_phase_new(cast_phase_src)",
			} {
				count := int64(0)
				if strings.HasPrefix(q, "INSERT") {
					count = 1
				}
				run(fixture.Exec(q, count))
			}
			checks := []fixture.Step{
				fixture.Select("SELECT cast_phase_stored()::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"red"}}),
				fixture.Select("SELECT ('red'::cast_phase_src)::cast_phase_dst::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"blue"}}),
				fixture.Select("SELECT 'red'::cast_phase_domain::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"red"}}),
				fixture.Select("SELECT conname,convalidated FROM pg_constraint WHERE conname IN ('cast_phase_check','cast_phase_domain_check') ORDER BY conname", []string{"conname", "convalidated"}, []string{"NAME", "BOOL"}, [][]any{{"cast_phase_check", false}, {"cast_phase_domain_check", false}}),
				fixture.Select("SELECT count(*) AS n FROM pg_cast WHERE castsource='cast_phase_src'::regtype AND casttarget='cast_phase_dst'::regtype AND castmethod::text='f' AND castcontext::text='e' AND castfunc='cast_phase_new(cast_phase_src)'::regprocedure::oid", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}),
			}
			for _, check := range checks {
				run(check)
			}
			castDefinitionReplaySQL(t, p, backend, checks, []fixture.Step{
				fixture.Exec("INSERT INTO cast_phase_saved DEFAULT VALUES", 1),
				fixture.Select("SELECT v::text AS v FROM cast_phase_saved ORDER BY v", []string{"v"}, []string{"TEXT"}, [][]any{{"red"}, {"red"}}),
				fixture.Error("INSERT INTO cast_phase_saved VALUES ('blue')", "23514"),
			})
		})
	}
}

// Independently altered members are separate SQL consumers even when Dump
// normally groups them into CREATE TABLE/DOMAIN. Each keeps its original
// selected array recipe; expanded omitted defaults are not written call args.
func TestReferenceCastDefinitionReplayConflicts(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			pairs := [][2]string{{"xid", "real"}, {"real", "xid"}}
			castDefinitionCleanup(t, p, backend, pairs...)
			run := func(s fixture.Step) {
				t.Helper()
				defer func() {
					if t.Failed() {
						t.Logf("replay conflicts SQL: %s", s.Query)
					}
				}()
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
			}
			oldArray := "((array_cat(array_fill('red'::cast_epoch_src,ARRAY[1],ARRAY[0]),array_fill(NULL::cast_epoch_src,ARRAY[1])))::integer[])"
			newArray := oldArray
			// The reverse binary edge exposes old word 42 and REAL 42's word
			// 1109917696 (0x42280000), without an unrelated SQL send provider.
			generatedArray := "(array_cat(array_fill(src,ARRAY[1],ARRAY[0]),array_fill(NULL::xid,ARRAY[1])))::real[]"
			for _, q := range []string{
				"CREATE TYPE cast_epoch_src AS ENUM ('red')",
				"CREATE FUNCTION cast_epoch_old(cast_epoch_src) RETURNS integer LANGUAGE SQL AS $$ SELECT CASE WHEN $1 IS NULL THEN 99 ELSE 41 END $$",
				"CREATE CAST (cast_epoch_src AS integer) WITH FUNCTION cast_epoch_old(cast_epoch_src) AS IMPLICIT",
				"CREATE TABLE cast_epoch_saved(a integer[] DEFAULT " + oldArray + ",b integer[],CONSTRAINT cast_epoch_old_check CHECK (a[0] = " + oldArray + "[0]))",
				"CREATE DOMAIN cast_epoch_domain AS integer CONSTRAINT cast_epoch_domain_old CHECK (VALUE >= " + oldArray + "[0])",
				"ALTER DOMAIN cast_epoch_domain ADD CONSTRAINT cast_epoch_domain_invalid CHECK (VALUE <> " + oldArray + "[0]) NOT VALID",
				"CREATE FUNCTION cast_epoch_default(a integer[] DEFAULT " + oldArray + ") RETURNS integer[] LANGUAGE SQL AS $$ SELECT $1 $$",
				"CREATE VIEW cast_epoch_case AS SELECT CASE 'red'::cast_epoch_src WHEN 41 THEN 1 ELSE 2 END AS v",
				"CREATE TABLE cast_epoch_parent(a integer[],b integer[])",
				"CREATE TABLE cast_epoch_child() INHERITS (cast_epoch_parent)",
				"ALTER TABLE ONLY cast_epoch_child ADD CONSTRAINT cast_epoch_child_old CHECK (a[0] = " + oldArray + "[0]) NO INHERIT",
				"CREATE TABLE cast_epoch_leaf() INHERITS (cast_epoch_parent)",
				"ALTER TABLE ONLY cast_epoch_leaf ADD CONSTRAINT cast_epoch_leaf_old CHECK (a[0] = " + oldArray + "[0])",
				"CREATE TABLE cast_epoch_recursive_parent(a integer[],b integer[])",
				"CREATE TABLE cast_epoch_recursive_child() INHERITS (cast_epoch_recursive_parent)",
				"ALTER TABLE cast_epoch_recursive_parent ADD CONSTRAINT cast_epoch_recursive_old CHECK (a[0] = " + oldArray + "[0])",
				"CREATE CAST (xid AS real) WITHOUT FUNCTION",
				"CREATE CAST (real AS xid) WITHOUT FUNCTION",
				"CREATE TABLE cast_epoch_generated(src xid,a real[] GENERATED ALWAYS AS (" + generatedArray + ") STORED,b real[] GENERATED ALWAYS AS (" + generatedArray + ") STORED)",
				"INSERT INTO cast_epoch_generated(src) VALUES ('42')",
				"DROP CAST (xid AS real)",
				"CREATE CAST (xid AS real) WITH INOUT",
				"DROP CAST (cast_epoch_src AS integer)",
				"CREATE FUNCTION cast_epoch_new(cast_epoch_src) RETURNS integer LANGUAGE SQL AS $$ SELECT CASE WHEN $1 IS NULL THEN 88 ELSE 77 END $$",
				"CREATE CAST (cast_epoch_src AS integer) WITH FUNCTION cast_epoch_new(cast_epoch_src) AS IMPLICIT",
				"ALTER TABLE cast_epoch_saved ALTER COLUMN b SET DEFAULT " + newArray,
				"ALTER TABLE cast_epoch_saved ADD CONSTRAINT cast_epoch_new_check CHECK (b[0] = " + newArray + "[0])",
				"ALTER TABLE ONLY cast_epoch_child ADD CONSTRAINT cast_epoch_child_new CHECK (b[0] = " + newArray + "[0]) NO INHERIT",
				"INSERT INTO cast_epoch_child VALUES ('[0:1]={41,99}','[0:1]={77,88}')",
				"ALTER TABLE ONLY cast_epoch_leaf ADD CONSTRAINT cast_epoch_leaf_new CHECK (b[0] = " + newArray + "[0])",
				"INSERT INTO cast_epoch_leaf VALUES ('[0:1]={41,99}','[0:1]={77,88}')",
				"ALTER TABLE cast_epoch_recursive_parent ADD CONSTRAINT cast_epoch_recursive_new CHECK (b[0] = " + newArray + "[0])",
				"INSERT INTO cast_epoch_recursive_child VALUES ('[0:1]={41,99}','[0:1]={77,88}')",
				"ALTER TABLE cast_epoch_generated ALTER COLUMN b SET EXPRESSION AS (" + generatedArray + ")",
				"ALTER DOMAIN cast_epoch_domain ADD CONSTRAINT cast_epoch_domain_new CHECK (VALUE <= " + newArray + "[0])",
				"CREATE TABLE cast_epoch_domain_saved(v cast_epoch_domain)",
				"INSERT INTO cast_epoch_domain_saved VALUES (50)",
				"CREATE VIEW cast_epoch_mixed AS SELECT (cast_epoch_default())[0] AS old_value," + newArray + "[0] AS new_value",
			} {
				count := int64(0)
				if strings.HasPrefix(q, "INSERT") {
					count = 1
				}
				run(fixture.Exec(q, count))
			}
			checks := []fixture.Step{
				fixture.Select("SELECT old_value,new_value FROM cast_epoch_mixed", []string{"old_value", "new_value"}, []string{"INT4", "INT4"}, [][]any{{int64(41), int64(77)}}),
				fixture.Select("SELECT v FROM cast_epoch_case", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}),
				fixture.Select("SELECT CASE 'red'::cast_epoch_src WHEN 41 THEN 1 ELSE 2 END AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}}),
				fixture.Select("SELECT v::integer AS v FROM cast_epoch_domain_saved", []string{"v"}, []string{"INT4"}, [][]any{{int64(50)}}),
				fixture.Select("SELECT a[0] AS old_value,b[0] AS new_value FROM ONLY cast_epoch_child", []string{"old_value", "new_value"}, []string{"INT4", "INT4"}, [][]any{{int64(41), int64(77)}}),
				fixture.Select("SELECT conname,convalidated,connoinherit,conislocal,coninhcount FROM pg_constraint WHERE conname IN ('cast_epoch_child_old','cast_epoch_child_new') ORDER BY conname", []string{"conname", "convalidated", "connoinherit", "conislocal", "coninhcount"}, []string{"NAME", "BOOL", "BOOL", "BOOL", "INT2"}, [][]any{{"cast_epoch_child_new", true, true, true, int64(0)}, {"cast_epoch_child_old", true, true, true, int64(0)}}),
				fixture.Select("SELECT a[0] AS old_value,b[0] AS new_value FROM ONLY cast_epoch_leaf", []string{"old_value", "new_value"}, []string{"INT4", "INT4"}, [][]any{{int64(41), int64(77)}}),
				fixture.Select("SELECT conname,convalidated,connoinherit,conislocal,coninhcount FROM pg_constraint WHERE conname IN ('cast_epoch_leaf_old','cast_epoch_leaf_new') ORDER BY conname", []string{"conname", "convalidated", "connoinherit", "conislocal", "coninhcount"}, []string{"NAME", "BOOL", "BOOL", "BOOL", "INT2"}, [][]any{{"cast_epoch_leaf_new", true, false, true, int64(0)}, {"cast_epoch_leaf_old", true, false, true, int64(0)}}),
				fixture.Select("SELECT count(*) AS n FROM pg_constraint WHERE conrelid='cast_epoch_parent'::regclass", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}),
				fixture.Select("SELECT a[0] AS old_value,b[0] AS new_value FROM ONLY cast_epoch_recursive_child", []string{"old_value", "new_value"}, []string{"INT4", "INT4"}, [][]any{{int64(41), int64(77)}}),
				fixture.Select("SELECT array_lower(a,1) AS lo,array_lower(b,1) AS new_lo,a[0]::xid::text AS old_word,a[1] IS NULL AS old_null,b[0]::xid::text AS new_word,b[1] IS NULL AS new_null FROM cast_epoch_generated", []string{"lo", "new_lo", "old_word", "old_null", "new_word", "new_null"}, []string{"INT4", "INT4", "TEXT", "BOOL", "TEXT", "BOOL"}, [][]any{{int64(0), int64(0), "42", true, "1109917696", true}}),
				fixture.Select("SELECT attname,attgenerated::text AS generation FROM pg_attribute WHERE attrelid='cast_epoch_generated'::regclass AND attname IN ('a','b') ORDER BY attname", []string{"attname", "generation"}, []string{"NAME", "TEXT"}, [][]any{{"a", "s"}, {"b", "s"}}),
				fixture.Select("SELECT conname,convalidated,connoinherit,conislocal,coninhcount FROM pg_constraint WHERE conrelid='cast_epoch_recursive_child'::regclass ORDER BY conname", []string{"conname", "convalidated", "connoinherit", "conislocal", "coninhcount"}, []string{"NAME", "BOOL", "BOOL", "BOOL", "INT2"}, [][]any{{"cast_epoch_recursive_new", true, false, false, int64(1)}, {"cast_epoch_recursive_old", true, false, false, int64(1)}}),
				fixture.Select("SELECT conname,convalidated,connoinherit,conislocal,coninhcount FROM pg_constraint WHERE conrelid='cast_epoch_recursive_parent'::regclass ORDER BY conname", []string{"conname", "convalidated", "connoinherit", "conislocal", "coninhcount"}, []string{"NAME", "BOOL", "BOOL", "BOOL", "INT2"}, [][]any{{"cast_epoch_recursive_new", true, false, true, int64(0)}, {"cast_epoch_recursive_old", true, false, true, int64(0)}}),
				fixture.Select("SELECT conname,convalidated FROM pg_constraint WHERE conname IN ('cast_epoch_old_check','cast_epoch_new_check','cast_epoch_domain_old','cast_epoch_domain_new','cast_epoch_domain_invalid') ORDER BY conname", []string{"conname", "convalidated"}, []string{"NAME", "BOOL"}, [][]any{{"cast_epoch_domain_invalid", false}, {"cast_epoch_domain_new", true}, {"cast_epoch_domain_old", true}, {"cast_epoch_new_check", true}, {"cast_epoch_old_check", true}}),
				fixture.Select("SELECT count(*) AS n FROM pg_cast WHERE castsource='cast_epoch_src'::regtype AND casttarget='integer'::regtype AND castfunc='cast_epoch_new(cast_epoch_src)'::regprocedure::oid AND castmethod::text='f' AND castcontext::text='i'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}),
				fixture.Select("SELECT count(*) AS n FROM pg_cast WHERE castsource='xid'::regtype AND casttarget='real'::regtype AND castmethod::text='i' AND castcontext::text='e' AND castfunc=0", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}),
			}
			for _, check := range checks {
				run(check)
			}
			castDefinitionReplaySQL(t, p, backend, checks, []fixture.Step{
				fixture.Exec("INSERT INTO cast_epoch_saved DEFAULT VALUES", 1),
				fixture.Select("SELECT array_lower(a,1) AS lo,a[0] AS old_value,a[1] AS old_null,b[0] AS new_value,b[1] AS new_null FROM cast_epoch_saved", []string{"lo", "old_value", "old_null", "new_value", "new_null"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(0), int64(41), int64(99), int64(77), int64(88)}}),
				fixture.Error("INSERT INTO cast_epoch_domain_saved VALUES (40)", "23514"),
				fixture.Error("INSERT INTO cast_epoch_domain_saved VALUES (80)", "23514"),
				fixture.Error("INSERT INTO cast_epoch_child VALUES ('[0:1]={77,88}','[0:1]={77,88}')", "23514"),
				fixture.Error("INSERT INTO cast_epoch_child VALUES ('[0:1]={41,99}','[0:1]={41,99}')", "23514"),
				fixture.Error("INSERT INTO cast_epoch_leaf VALUES ('[0:1]={77,88}','[0:1]={77,88}')", "23514"),
				fixture.Error("INSERT INTO cast_epoch_leaf VALUES ('[0:1]={41,99}','[0:1]={41,99}')", "23514"),
				fixture.Error("INSERT INTO cast_epoch_recursive_child VALUES ('[0:1]={77,88}','[0:1]={77,88}')", "23514"),
				fixture.Error("INSERT INTO cast_epoch_recursive_child VALUES ('[0:1]={41,99}','[0:1]={41,99}')", "23514"),
				fixture.Exec("INSERT INTO cast_epoch_generated(src) VALUES ('42')", 1),
				fixture.Select("SELECT count(*) AS n FROM cast_epoch_generated WHERE a[0]::xid::text='42' AND a[1] IS NULL AND b[0]::xid::text='1109917696' AND b[1] IS NULL", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}),
			}, pairs...)
		})
	}
}

// Check the actual native-produced script both through isolated native Restore
// and, when enabled, the real PostgreSQL connection. No replay surrogate SQL.
func castDefinitionReplaySQL(t *testing.T, p *fixture.Pair, backend fixture.Backend, checks, mutations []fixture.Step, pairs ...[2]string) {
	t.Helper()
	run := func(dbBackend fixture.Backend, steps []fixture.Step) {
		for _, step := range steps {
			fixture.RunStep(t, p.Ctx, dbBackend.DB, dbBackend.Recorder, step)
		}
	}
	if backend.PG {
		run(backend, mutations)
		return
	}
	var script bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &script); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(script.Bytes())); err != nil {
		t.Fatalf("native frozen recipe replay: %v\n%s", err, script.String())
	}
	run(backend, checks)
	if p.Postgres != nil {
		pg := fixture.Backend{DB: p.Postgres, Recorder: p.PostgresRecord, PG: true}
		castDefinitionCleanup(t, p, pg, pairs...)
		conn, err := p.Postgres.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		reset := func() {
			t.Helper()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			for _, q := range []string{"ROLLBACK", "DISCARD ALL", "DROP SCHEMA public CASCADE; CREATE SCHEMA public"} {
				if _, err := conn.ExecContext(ctx, q); err != nil {
					t.Errorf("reset PostgreSQL frozen replay %q: %v", q, err)
				}
			}
			for _, pair := range pairs {
				if _, err := conn.ExecContext(ctx, "DROP CAST IF EXISTS ("+pair[0]+" AS "+pair[1]+")"); err != nil {
					t.Errorf("reset PostgreSQL frozen replay cast %s to %s: %v", pair[0], pair[1], err)
				}
			}
		}
		reset()
		if t.Failed() {
			return
		}
		defer reset()
		if _, err := conn.ExecContext(p.Ctx, script.String()); err != nil {
			t.Fatalf("actual native frozen recipe SQL on PostgreSQL: %v\n%s", err, script.String())
		}
		for _, steps := range [][]fixture.Step{checks, mutations} {
			for _, step := range steps {
				fixture.RunStep(t, p.Ctx, conn, pg.Recorder, step)
			}
		}
	}
	bad := append(append([]byte(nil), script.Bytes()...), []byte("\nCREATE CAST (xid AS real) WITH FUNCTION missing_replay_function(xid);\n")...)
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(bad)); err == nil {
		t.Fatal("late bad cast installed a partial replay catalog")
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	err := stuntgres.Restore(ctx, p.Native, &fixture.EnumCancelReader{Reader: bytes.NewReader(script.Bytes()), Cancel: cancel})
	cancel()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled frozen recipe replay: %v", err)
	}
	run(backend, checks)
	run(backend, mutations)
}
