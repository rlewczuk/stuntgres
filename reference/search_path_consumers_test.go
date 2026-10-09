package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
)

func TestReferenceSearchPathQualifiedProviderConsumers(t *testing.T) {
	p := newReferenceScenario(t)
	schemaReferenceCleanup(t, "sp_consumer")
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			c, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, backend.recorder, s) }
			run(referenceExec("CREATE SCHEMA sp_consumer", 0))
			run(referenceExec("CREATE TABLE sp_consumer.t(id pg_catalog.int4)", 0))
			run(referenceExec("INSERT INTO sp_consumer.t VALUES(7)", 1))
			run(referenceExec("SET search_path TO sp_consumer", 0))
			for _, tc := range []struct {
				q    string
				want any
			}{{"SELECT pg_catalog.num_nulls(NULL,7)", int64(1)}, {"SELECT pg_catalog.current_schemas(false)", "{sp_consumer}"}, {"SELECT pg_catalog.current_schemas('false')", "{sp_consumer}"}, {"SELECT pg_catalog.current_setting('search_path')", "sp_consumer"}, {"SELECT CAST(id AS pg_catalog.int4) FROM sp_consumer.t", int64(7)}, {"SELECT id OPERATOR(pg_catalog.+) 1 FROM sp_consumer.t", int64(8)}, {"SELECT id OPERATOR(pg_catalog.=) 7 FROM sp_consumer.t", true}, {"SELECT id OPERATOR(pg_catalog.-) 2 + 3 FROM sp_consumer.t", int64(2)}, {"SELECT OPERATOR(pg_catalog.-) id + 3 FROM sp_consumer.t", int64(-10)}, {"SELECT id - 2 + 3 FROM sp_consumer.t", int64(8)}} {
				var got any
				if err := c.QueryRowContext(p.ctx, tc.q).Scan(&got); err != nil || !reflect.DeepEqual(referenceNormalize(got), tc.want) {
					t.Fatal(tc.q, got, tc.want, err)
				}
			}
		})
	}
}

func TestReferenceSearchPathQualifiedMathConsumers(t *testing.T) {
	for _, sourceSetup := range []bool{false, true} {
		t.Run(map[bool]string{false: "recorded", true: "source setup"}[sourceSetup], func(t *testing.T) {
			p := newReferenceScenario(t)
			schemaReferenceCleanup(t, "math_consumer")
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					c, err := backend.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, backend.recorder, s) }
					for _, s := range []referenceStep{referenceExec("CREATE SCHEMA math_consumer", 0), referenceExec("CREATE TABLE math_consumer.source(id INT)", 0), referenceExec("INSERT INTO math_consumer.source VALUES(7)", 1)} {
						if sourceSetup {
							s.nonContext = true
							referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
						} else {
							run(s)
						}
					}
					for _, tc := range []struct {
						name, args, typ, signature string
						want                       any
					}{{"min_scale", "8.4100", "INT4", "numeric", int64(2)}, {"scale", "8.4100", "INT4", "numeric", int64(4)}, {"trim_scale", "8.4100", "NUMERIC", "numeric", "8.41"}, {"round", "1.25,1", "NUMERIC", "numeric, integer", "1.3"}, {"trunc", "-1.25,1", "NUMERIC", "numeric, integer", "-1.2"}, {"sign", "-13.000", "NUMERIC", "numeric", "-1"}, {"mod", "-9,4", "INT4", "integer, integer", int64(-1)}} {
						run(referenceExec("SET search_path TO public", 0))
						q := "SELECT pg_catalog." + tc.name + "(" + tc.args + ")"
						held, err := c.PrepareContext(p.ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						check := func(rows *sql.Rows) {
							t.Helper()
							defer rows.Close()
							metadata, err := rows.ColumnTypes()
							scan := reflect.TypeOf(tc.want)
							if backend.pg {
								if tc.typ == "NUMERIC" {
									scan = reflect.TypeFor[any]()
								} else {
									scan = reflect.TypeFor[int32]()
								}
							}
							if err != nil || len(metadata) != 1 || metadata[0].Name() != tc.name || metadata[0].DatabaseTypeName() != tc.typ || metadata[0].ScanType() != scan {
								t.Fatal(metadata, scan, err)
							}
							if !rows.Next() {
								t.Fatal(rows.Err())
							}
							var got any
							if err := rows.Scan(&got); err != nil {
								t.Fatal(err)
							}
							if backend.pg && tc.typ == "NUMERIC" {
								if _, ok := got.([]byte); !ok {
									t.Fatal("pq raw numeric", got)
								}
							} else {
								if reflect.TypeOf(got) != reflect.TypeOf(tc.want) {
									t.Fatal("raw scalar", got)
								}
							}
							if !reflect.DeepEqual(referenceNormalize(got), tc.want) {
								t.Fatal(got, tc.want)
							}
							if rows.Next() || rows.Err() != nil {
								t.Fatal(rows.Err())
							}
							if err := rows.Close(); err != nil {
								t.Fatal(err)
							}
						}
						rows, err := c.QueryContext(p.ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						check(rows)
						for _, path := range []string{"public", "math_consumer"} {
							run(referenceExec("SET search_path TO "+path, 0))
							rows, err := held.QueryContext(p.ctx)
							if err != nil {
								held.Close()
								t.Fatal(err)
							}
							check(rows)
						}
						held.Close()
						rows, err = c.QueryContext(p.ctx, "SELECT "+tc.name+"("+tc.args+")")
						if err != nil {
							t.Fatal(err)
						}
						check(rows)
						want := controlsReferenceDiagnostic("42883", "function public."+tc.name+"("+tc.signature+") does not exist")
						want.Position = "8"
						want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
						for _, prepared := range []bool{false, true} {
							s := referenceErrorDiagnostic("SELECT public."+tc.name+"("+tc.args+")", want)
							s.selectRows = true
							s.prepared = prepared
							if prepared {
								s.errorStage = "prepare"
							}
							run(s)
						}
					}
					want := controlsReferenceDiagnostic("3F000", `schema "missing_path_ns" does not exist`)
					want.Position = "8"
					for _, prepared := range []bool{false, true} {
						s := referenceErrorDiagnostic("SELECT missing_path_ns.round(1.25,1)", want)
						s.selectRows = true
						s.prepared = prepared
						if prepared {
							s.errorStage = "prepare"
						}
						run(s)
					}
					var path string
					if err := c.QueryRowContext(p.ctx, "SHOW search_path").Scan(&path); err != nil || path != "math_consumer" {
						t.Fatal(path, err)
					}
					var id int64
					if err := c.QueryRowContext(p.ctx, "SELECT id FROM math_consumer.source").Scan(&id); err != nil || id != 7 {
						t.Fatal(id, err)
					}
				})
			}
		})
	}
}

func TestReferenceSearchPathQualifiedBitwiseConsumers(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE public.path_bits(id INT,mask INT DEFAULT (7 OPERATOR(pg_catalog.&) 3))", 0))
	p.step(t, referenceExec("INSERT INTO public.path_bits(id) VALUES(7)", 1))
	for _, backend := range p.backends() {
		for _, tc := range []struct {
			expression string
			want       int64
		}{{"id OPERATOR(pg_catalog.&) 3", 3}, {"id OPERATOR(pg_catalog.|) 8", 15}, {"id OPERATOR(pg_catalog.#) 3", 4}, {"id OPERATOR(pg_catalog.<<) 1", 14}, {"id OPERATOR(pg_catalog.>>) 1", 3}, {"OPERATOR(pg_catalog.~) id", -8}, {"CAST(id OPERATOR(pg_catalog.&) 3 AS pg_catalog.int2)", 3}, {"mask", 3}} {
			q := "SELECT " + tc.expression + " FROM public.path_bits"
			var got int64
			if err := backend.db.QueryRowContext(p.ctx, q).Scan(&got); err != nil || got != tc.want {
				t.Fatal(q, got, err)
			}
			held, err := backend.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			err = held.QueryRowContext(p.ctx).Scan(&got)
			held.Close()
			if err != nil || got != tc.want {
				t.Fatal(q, got, err)
			}
		}
	}
	want := controlsReferenceDiagnostic("42883", "operator does not exist: integer public.& integer")
	want.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
	want.Position = "11"
	s := referenceErrorDiagnostic("SELECT id OPERATOR(public.&) 3 FROM public.path_bits", want)
	s.selectRows = true
	p.step(t, s)
}
