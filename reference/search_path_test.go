package reference_test

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestReferenceSearchPathQualifiedProviders(t *testing.T) {
	p := newReferenceScenario(t)
	schemaReferenceCleanup(t, "sp1")
	for _, q := range []string{"CREATE SCHEMA sp1", "CREATE TABLE sp1.t(id pg_catalog.int4)", "INSERT INTO sp1.t VALUES(7)"} {
		n := int64(0)
		if strings.HasPrefix(q, "INSERT") {
			n = 1
		}
		p.step(t, referenceExec(q, n))
	}
	for _, tc := range []struct {
		q, label, typ string
		want          any
	}{
		{"SELECT pg_catalog.num_nulls(NULL,7)", "num_nulls", "INT4", int64(1)},
		{"SELECT pg_catalog.current_schemas('false')", "current_schemas", "_NAME", "{public}"},
		{"SELECT CAST(id AS pg_catalog.int4) FROM sp1.t", "id", "INT4", int64(7)},
		{"SELECT pg_catalog.int4(id) FROM sp1.t", "int4", "INT4", int64(7)},
		{"SELECT pg_catalog.count(*) FROM sp1.t", "count", "INT8", int64(1)},
		{"SELECT id OPERATOR(pg_catalog.+) 1 FROM sp1.t", "?column?", "INT4", int64(8)},
		{"SELECT id OPERATOR(pg_catalog.=) 7 FROM sp1.t", "?column?", "BOOL", true},
		{"SELECT id OPERATOR(pg_catalog.-) 2 + 3 FROM sp1.t", "?column?", "INT4", int64(2)},
		{"SELECT id - 2 + 3 FROM sp1.t", "?column?", "INT4", int64(8)},
		{"SELECT OPERATOR(pg_catalog.-) id + 3 FROM sp1.t", "?column?", "INT4", int64(-10)},
	} {
		s := referenceSelect(tc.q, []string{tc.label}, []string{tc.typ}, [][]any{{tc.want}})
		p.step(t, s)
		p.step(t, referencePrepared(s))
	}
	p.step(t, referenceExec("CREATE SEQUENCE sp1.seq", 0))
	p.step(t, referenceSelect("SELECT pg_catalog.nextval('sp1.seq')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(1)}}))
	p.step(t, referencePrepared(referenceSelect("SELECT pg_catalog.currval('sp1.seq')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(1)}})))
	p.step(t, referenceSelect("SELECT pg_catalog.set_config('search_path','sp1',false)", []string{"set_config"}, []string{"TEXT"}, [][]any{{"sp1"}}))
	p.step(t, referencePrepared(referenceSelect("SELECT pg_catalog.current_setting('search_path')", []string{"current_setting"}, []string{"TEXT"}, [][]any{{"sp1"}})))
}

func TestReferenceSearchPathDiagnostics(t *testing.T) {
	for _, sourceConsumer := range []bool{false, true} {
		t.Run(map[bool]string{false: "recorded", true: "source"}[sourceConsumer], func(t *testing.T) {
			p := newReferenceScenario(t)
			schemaReferenceCleanup(t, "sp1")
			if sourceConsumer {
				for _, s := range []referenceStep{referenceExec("CREATE SCHEMA sp1", 0), referenceExec("CREATE TABLE sp1.t(id INT)", 0), referenceExec("INSERT INTO sp1.t VALUES(7)", 1)} {
					s.nonContext = true
					p.step(t, s)
				}
			} else {
				p.step(t, referenceExec("CREATE SCHEMA sp1 CREATE TABLE t(id INT)", 0))
				p.step(t, referenceExec("INSERT INTO sp1.t VALUES(7)", 1))
			}
			const fh = "No function matches the given name and argument types. You might need to add explicit type casts."
			const oh = "No operator matches the given name and argument types. You might need to add explicit type casts."
			for _, tc := range []struct{ q, code, message, hint, pos string }{
				{"SELECT current_schemas()", "42883", "function current_schemas() does not exist", fh, "8"},
				{"SELECT current_schemas(1)", "42883", "function current_schemas(integer) does not exist", fh, "8"},
				{"SELECT public.current_schemas(false)", "42883", "function public.current_schemas(boolean) does not exist", fh, "8"},
				{"SELECT public.current_setting('search_path')", "42883", "function public.current_setting(unknown) does not exist", fh, "8"},
				{"SELECT public.num_nulls(1)", "42883", "function public.num_nulls(integer) does not exist", fh, "8"},
				{"SELECT 1 OPERATOR(public.+) 2 FROM sp1.t", "42883", "operator does not exist: integer public.+ integer", oh, "10"},
				{"SELECT 1::public.int4 FROM sp1.t", "42704", `type "public.int4" does not exist`, "", "11"},
				{"SELECT missing_path_ns.num_nulls(1)", "3F000", `schema "missing_path_ns" does not exist`, "", "8"},
				{"SELECT pg_temp.num_nulls(1)", "3F000", `schema "pg_temp" does not exist`, "", "8"},
				{"SELECT 1 OPERATOR(missing_path_ns.+) 2 FROM sp1.t", "3F000", `schema "missing_path_ns" does not exist`, "", "10"},
				{"SELECT 1::missing_path_ns.int4 FROM sp1.t", "3F000", `schema "missing_path_ns" does not exist`, "", "11"},
			} {
				s := referenceErrorDiagnostic(tc.q, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Hint: tc.hint, Position: tc.pos})
				if sourceConsumer {
					s.selectRows = true
					s.nonContext = true
					p.step(t, s)
					continue
				}
				p.step(t, s)
				query := s
				query.selectRows = true
				p.step(t, query)
				s = referencePrepared(s)
				s.errorStage = "prepare"
				p.step(t, s)
			}
		})
	}
}

func TestReferenceSearchPathPendingTempBoundary(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			c, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if _, err := c.ExecContext(p.ctx, "SET search_path TO pg_temp,public"); err != nil {
				t.Fatal(err)
			}
			var got string
			if err := c.QueryRowContext(p.ctx, "SELECT current_schemas(false)").Scan(&got); err != nil {
				t.Fatal(err)
			}
			if !backend.pg {
				if got != "{public}" {
					t.Fatal(got)
				}
			} else {
				var temp string
				if err := c.QueryRowContext(p.ctx, "SELECT current_schema()").Scan(&temp); err != nil || !strings.HasPrefix(temp, "pg_temp_") {
					t.Fatal(temp, err)
				}
				if got != "{"+temp+",public}" {
					t.Fatal(got, temp)
				}
			}
		})
	}
}

func TestReferenceSearchPathCurrentUser(t *testing.T) {
	roleReferenceCleanup(t, []string{"path_role_a", "path_role_b"})
	p := newReferenceScenario(t)
	schemaReferenceCleanup(t, "path_role_a", "path_role_b")
	for _, q := range []string{"CREATE USER path_role_a", "CREATE USER path_role_b", "CREATE SCHEMA path_role_a AUTHORIZATION path_role_a", "CREATE SCHEMA path_role_b AUTHORIZATION path_role_b"} {
		p.step(t, referenceExec(q, 0))
	}
	// PostgreSQL-only fixture setup: match the native public-USAGE baseline
	// for these two roles; native does not expose SQL GRANT.
	if p.postgres != nil {
		if _, err := p.postgres.ExecContext(p.ctx, "GRANT USAGE ON SCHEMA public TO path_role_a,path_role_b"); err != nil {
			t.Fatal(err)
		}
	}
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			c, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer c.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
			if _, err := c.ExecContext(p.ctx, `SET search_path TO "$user",public`); err != nil {
				t.Fatal(err)
			}
			held, err := c.PrepareContext(p.ctx, "SELECT current_schemas($1)")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			for _, role := range []string{"path_role_a", "path_role_b"} {
				if _, err := c.ExecContext(p.ctx, "SET SESSION AUTHORIZATION "+role); err != nil {
					t.Fatal(err)
				}
				for _, tc := range []struct {
					flag bool
					want string
				}{{false, "{" + role + ",public}"}, {true, "{pg_catalog," + role + ",public}"}} {
					rows, err := held.QueryContext(p.ctx, tc.flag)
					if err != nil {
						t.Fatal(err)
					}
					schemaReferenceRow(t, rows, "current_schemas", "_NAME", tc.want)
				}
			}
		})
	}
}

func TestReferenceSearchPathQualifiedRandomProviders(t *testing.T) {
	p := newReferenceScenario(t)
	for _, tc := range []struct {
		sql, label, typ string
		want            any
	}{
		{"SELECT pg_catalog.random(7,7)", "random", "INT4", int64(7)},
		{"SELECT pg_catalog.random_normal(7,0)", "random_normal", "FLOAT8", float64(7)},
		{"SELECT pg_catalog.setseed(0)", "setseed", "VOID", ""},
	} {
		s := referenceSelect(tc.sql, []string{tc.label}, []string{tc.typ}, [][]any{{tc.want}})
		p.step(t, s)
		p.step(t, referencePrepared(s))
	}
	s := referenceErrorDiagnostic("SELECT public.random()", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "function public.random() does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."})
	p.step(t, s)
	s = referencePrepared(s)
	s.errorStage = "prepare"
	p.step(t, s)
}

func TestReferenceSearchPathQualifiedMathProviders(t *testing.T) {
	p := newReferenceScenario(t)
	schemaReferenceCleanup(t, "math_path")
	p.step(t, referenceExec("CREATE SCHEMA math_path", 0))
	p.step(t, referenceExec("CREATE TABLE math_path.source(id INT)", 0))
	p.step(t, referenceExec("INSERT INTO math_path.source VALUES(7)", 1))
	for _, tc := range []struct {
		name, args, typ, signature string
		want                       any
	}{
		{"min_scale", "8.4100", "INT4", "numeric", int64(2)},
		{"scale", "8.4100", "INT4", "numeric", int64(4)},
		{"trim_scale", "8.4100", "NUMERIC", "numeric", "8.41"},
		{"round", "1.25,1", "NUMERIC", "numeric, integer", "1.3"},
		{"trunc", "-1.25,1", "NUMERIC", "numeric, integer", "-1.2"},
		{"sign", "-13.000", "NUMERIC", "numeric", "-1"},
		{"mod", "-9,4", "INT4", "integer, integer", int64(-1)},
	} {
		q := "SELECT pg_catalog." + tc.name + "(" + tc.args + ")"
		s := referenceSelect(q, []string{tc.name}, []string{tc.typ}, [][]any{{tc.want}})
		p.step(t, s)
		p.step(t, referencePrepared(s))
		s = referenceErrorDiagnostic("SELECT public."+tc.name+"("+tc.args+")", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "function public." + tc.name + "(" + tc.signature + ") does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."})
		p.step(t, s)
		s = referencePrepared(s)
		s.errorStage = "prepare"
		p.step(t, s)
	}
	s := referenceErrorDiagnostic("SELECT missing_path_ns.round(1.25,1)", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "3F000", Message: `schema "missing_path_ns" does not exist`, Position: "8"})
	p.step(t, s)
	s = referencePrepared(s)
	s.errorStage = "prepare"
	p.step(t, s)
	for _, backend := range p.backends() {
		t.Run(backend.name+"/held-path", func(t *testing.T) {
			c, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(step referenceStep) { referenceRunStep(t, p.ctx, c, backend.recorder, step) }
			run(referenceExec("SET search_path TO public", 0))
			held, err := c.PrepareContext(p.ctx, "SELECT pg_catalog.round(1.25,1)")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			for _, path := range []string{"public", "math_path"} {
				run(referenceExec("SET search_path TO "+path, 0))
				rows, err := held.QueryContext(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				func() {
					defer rows.Close()
					metadata, err := rows.ColumnTypes()
					scan := reflect.TypeFor[string]()
					if backend.pg {
						scan = reflect.TypeFor[any]()
					}
					if err != nil || len(metadata) != 1 || metadata[0].Name() != "round" || metadata[0].DatabaseTypeName() != "NUMERIC" || metadata[0].ScanType() != scan {
						t.Fatal("held math metadata", metadata, err, scan)
					}
					if !rows.Next() {
						t.Fatal("missing held math row", rows.Err())
					}
					var raw any
					if err := rows.Scan(&raw); err != nil {
						t.Fatal(err)
					}
					if backend.pg {
						bytes, ok := raw.([]byte)
						if !ok || string(bytes) != "1.3" {
							t.Fatal("pq math export", raw)
						}
					} else if raw != "1.3" {
						t.Fatal("native math export", raw)
					}
					if rows.Next() || rows.Err() != nil {
						t.Fatal("extra held math row", rows.Err())
					}
				}()
				run(referenceSelect("SELECT round(1.25,1)", []string{"round"}, []string{"NUMERIC"}, [][]any{{"1.3"}}))
			}
			run(referenceErrorDiagnostic("SELECT missing_path_ns.round(1.25,1)", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "3F000", Message: `schema "missing_path_ns" does not exist`, Position: "8"}))
			run(referenceSelect("SHOW search_path", []string{"search_path"}, []string{"TEXT"}, [][]any{{"math_path"}}))
			run(referenceSelect("SELECT id FROM math_path.source", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		})
	}
}

func TestReferenceSearchPathQualifiedBitwiseProviders(t *testing.T) {
	p := newReferenceScenario(t)
	schemaReferenceCleanup(t, "bit_path")
	p.step(t, referenceExec("CREATE SCHEMA bit_path", 0))
	p.step(t, referenceExec("CREATE TABLE bit_path.t(id INT)", 0))
	p.step(t, referenceExec("INSERT INTO bit_path.t VALUES(7)", 1))
	for _, tc := range []struct {
		expression, typ string
		want            int64
	}{
		{"id OPERATOR(pg_catalog.&) 3", "INT4", 3},
		{"id OPERATOR(pg_catalog.|) 8", "INT4", 15},
		{"id OPERATOR(pg_catalog.#) 3", "INT4", 4},
		{"id OPERATOR(pg_catalog.<<) 1", "INT4", 14},
		{"id OPERATOR(pg_catalog.>>) 1", "INT4", 3},
		{"OPERATOR(pg_catalog.~) id", "INT4", -8},
		{"CAST(id OPERATOR(pg_catalog.&) 3 AS pg_catalog.int2)", "INT2", 3},
	} {
		s := referenceSelect("SELECT "+tc.expression+" AS v FROM bit_path.t", []string{"v"}, []string{tc.typ}, [][]any{{tc.want}})
		p.step(t, s)
		p.step(t, referencePrepared(s))
	}
	s := referenceErrorDiagnostic("SELECT id OPERATOR(public.&) 3 FROM bit_path.t", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "operator does not exist: integer public.& integer", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: "11"})
	p.step(t, s)
	s = referencePrepared(s)
	s.errorStage = "prepare"
	p.step(t, s)
}

func TestReferenceSearchPathQualifiedTemporalPrecision(t *testing.T) {
	p := newReferenceScenario(t)
	schemaReferenceCleanup(t, "sp1")
	p.step(t, referenceExec("CREATE SCHEMA sp1", 0))
	p.step(t, referencePrepared(referenceExec(`CREATE TABLE sp1.temporal(t pg_catalog.time(3),ts pg_catalog.timestamp(3),tq pg_catalog."time"(3),tsq pg_catalog."timestamp"(3))`, 0)))
	p.step(t, referenceExec("INSERT INTO sp1.temporal VALUES('12:34:56.123789','2000-01-01 12:34:56.123789','12:34:56.123789','2000-01-01 12:34:56.123789')", 1))
	p.step(t, referencePrepared(referenceExec("INSERT INTO sp1.temporal VALUES(TIME(3) '12:34:56.123789',TIMESTAMP(3) '2000-01-01 12:34:56.123789',TIME(3) '12:34:56.123789',TIMESTAMP(3) '2000-01-01 12:34:56.123789')", 1)))
	labels := []string{"t", "ts", "tq", "tsq"}
	types := []string{"TIME", "TIMESTAMP", "TIME", "TIMESTAMP"}
	want := [][]any{
		{"12:34:56.124", time.Date(2000, 1, 1, 12, 34, 56, 124000000, time.UTC), "12:34:56.124", time.Date(2000, 1, 1, 12, 34, 56, 124000000, time.UTC)},
		{"12:34:56.124", time.Date(2000, 1, 1, 12, 34, 56, 124000000, time.UTC), "12:34:56.124", time.Date(2000, 1, 1, 12, 34, 56, 124000000, time.UTC)},
	}
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			c, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			const query = "SELECT t,ts,tq,tsq FROM sp1.temporal"
			expected := referenceTimeRows(t, backend.pg, types, want)
			step := referenceSelect(query, labels, types, expected)
			held, err := c.PrepareContext(p.ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			for _, path := range []string{"sp1", "pg_catalog"} {
				referenceRunStep(t, p.ctx, c, backend.recorder, referenceExec("SET search_path TO "+path, 0))
				referenceRunStep(t, p.ctx, c, backend.recorder, step)
				rows, err := held.QueryContext(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				func() {
					defer rows.Close()
					metadata, err := rows.ColumnTypes()
					if err != nil || len(metadata) != len(types) {
						t.Fatal(metadata, err)
					}
					for i, typ := range types {
						scan := reflect.TypeFor[time.Time]()
						if typ == "TIME" && !backend.pg {
							scan = reflect.TypeFor[string]()
						}
						if metadata[i].Name() != labels[i] || metadata[i].DatabaseTypeName() != typ || metadata[i].ScanType() != scan {
							t.Fatal("held temporal metadata", metadata[i], scan)
						}
					}
					var got [][]any
					for rows.Next() {
						var values [4]any
						if err := rows.Scan(&values[0], &values[1], &values[2], &values[3]); err != nil {
							t.Fatal(err)
						}
						for i := range values {
							values[i] = referenceNormalize(values[i])
						}
						got = append(got, values[:])
					}
					if err := rows.Err(); err != nil || !reflect.DeepEqual(got, expected) {
						t.Fatal("held temporal values", got, expected, err)
					}
				}()
			}
		})
	}
}

func TestReferenceSearchPathNameArrayOrderingBoundary(t *testing.T) {
	p := newReferenceScenario(t)
	schemaReferenceCleanup(t, "path_order")
	p.step(t, referenceExec("CREATE SCHEMA path_order", 0))
	p.step(t, referenceExec("CREATE TABLE path_order.source(id INT,flag BOOL)", 0))
	for _, populated := range []bool{false, true} {
		state := "/empty"
		if populated {
			p.step(t, referenceExec("INSERT INTO path_order.source VALUES(1,NULL)", 1))
			state = "/populated"
		}
		for _, backend := range p.backends() {
			t.Run(backend.name+state, func(t *testing.T) {
				c, err := backend.db.Conn(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, backend.recorder, s) }
				run(referenceExec("SET search_path TO path_order", 0))
				for _, tc := range []struct {
					q, label string
					value    any
				}{
					{"SELECT id FROM path_order.source ORDER BY current_schemas(false)", "id", int64(1)},
					{"SELECT current_schemas(false) AS paths FROM path_order.source ORDER BY paths", "paths", "{path_order}"},
					{"SELECT current_schemas(flag) FROM path_order.source ORDER BY 1", "current_schemas", nil},
				} {
					var step referenceStep
					if !backend.pg {
						step = referenceErrorDiagnostic(tc.q, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an ordering operator for type name[]", Hint: "Use an explicit ordering operator or modify the query.", Position: strconv.Itoa(strings.LastIndex(tc.q, "ORDER BY ") + len("ORDER BY ") + 1)})
					} else {
						// Released PostgreSQL array comparison is deliberately oracle-only,
						// not an engine comparator or a same-error parity assertion.
						want := [][]any{}
						if populated {
							want = [][]any{{tc.value}}
						}
						typ := "_NAME"
						if tc.label == "id" {
							typ = "INT4"
						}
						step = referenceSelect(tc.q, []string{tc.label}, []string{typ}, want)
					}
					run(step)
					if !backend.pg {
						query := step
						query.selectRows = true
						run(query)
					}
					step = referencePrepared(step)
					if !backend.pg {
						step.errorStage = "prepare"
					}
					run(step)
				}
				run(referenceSelect("SELECT current_schemas(false)", []string{"current_schemas"}, []string{"_NAME"}, [][]any{{"{path_order}"}}))
				if populated {
					run(referenceSelect("SELECT id FROM path_order.source", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
				} else {
					run(referenceSelect("SELECT id FROM path_order.source", []string{"id"}, []string{"INT4"}, [][]any{}))
				}
			})
		}
	}
}
