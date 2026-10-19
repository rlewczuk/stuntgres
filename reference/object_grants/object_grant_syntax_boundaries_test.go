package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Independent source expectations: REL_18_0 gram.y func_arg/param_name,
// parse_func.c LookupFuncWithArgs, pg_proc.dat, and aclchk.c ExecuteGrantStmt.
// The selected pinned oracle must resolve these draft expectations before the
// temporary discovery filters are removed; no oracle run is claimed here.
func TestReferenceObjectGrantSyntaxDiagnostics(t *testing.T) {
	objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("SET TIME ZONE 'UTC'", 0))
		cases := []struct {
			label, canonical, declaration, query, typ string
			value                                     any
		}{
			{"mode-before-name", "pg_catalog.abs(integer)", "pg_catalog.abs(IN input INTEGER)", "SELECT abs(-7::integer) AS v", "INT4", int64(7)},
			{"mode-after-name", "pg_catalog.abs(integer)", "pg_catalog.abs(input IN INTEGER)", "SELECT abs(-7::integer) AS v", "INT4", int64(7)},
			{"out-excluded", "pg_catalog.abs(integer)", "pg_catalog.abs(OUT ignored TEXT, IN input INTEGER)", "SELECT abs(-7::integer) AS v", "INT4", int64(7)},
			{"out-after-name-excluded", "pg_catalog.abs(integer)", "pg_catalog.abs(ignored OUT TEXT, input IN INTEGER)", "SELECT abs(-7::integer) AS v", "INT4", int64(7)},
			{"inout-included", "pg_catalog.abs(integer)", "pg_catalog.abs(INOUT input INTEGER)", "SELECT abs(-7::integer) AS v", "INT4", int64(7)},
			{"in-out-included", "pg_catalog.abs(integer)", "pg_catalog.abs(input IN OUT INTEGER)", "SELECT abs(-7::integer) AS v", "INT4", int64(7)},
			{"quoted-mode-name", "pg_catalog.abs(integer)", `pg_catalog.abs("out" IN INTEGER)`, "SELECT abs(-7::integer) AS v", "INT4", int64(7)},
			{"type-function-keyword-name", "pg_catalog.abs(integer)", "pg_catalog.abs(authorization IN INTEGER)", "SELECT abs(-7::integer) AS v", "INT4", int64(7)},
			{"quoted-column-keyword-name", "pg_catalog.abs(integer)", `pg_catalog.abs("between" INTEGER)`, "SELECT abs(-7::integer) AS v", "INT4", int64(7)},
			{"qualified-quoted-type", "pg_catalog.abs(integer)", `pg_catalog.abs(IN "in" pg_catalog."int4")`, "SELECT abs(-7::integer) AS v", "INT4", int64(7)},
			{"compound-double", "pg_catalog.abs(double precision)", "pg_catalog.abs(input IN DOUBLE PRECISION)", "SELECT abs(-7::double precision) AS v", "FLOAT8", float64(7)},
			{"numeric-typmod", "pg_catalog.abs(numeric)", "pg_catalog.abs(IN input NUMERIC(9,2))", "SELECT abs(-7::numeric) AS v", "NUMERIC", "7"},
			{"character-typmod", "pg_catalog.length(character)", "pg_catalog.length(IN input CHARACTER(12))", "SELECT length('abc'::character(12)) AS v", "INT4", int64(3)},
			{"bit-typmod", "pg_catalog.bit_length(bit)", "pg_catalog.bit_length(input IN BIT(3))", "SELECT bit_length(B'101') AS v", "INT4", int64(3)},
			{"array-formal-variadic", "pg_catalog.json_object(text[])", `pg_catalog.json_object(VARIADIC "variadic" pg_catalog.text[])`, "SELECT json_typeof(json_object(ARRAY['a','1'])) AS v", "TEXT", "object"},
			{"array-bounds", "pg_catalog.json_object(text[])", "pg_catalog.json_object(input IN TEXT ARRAY[2])", "SELECT json_typeof(json_object(ARRAY['a','1'])) AS v", "TEXT", "object"},
			{"temporal-compound", "pg_catalog.timezone(text,timestamp without time zone)", "pg_catalog.timezone(zone IN TEXT, value IN TIMESTAMP(3) WITHOUT TIME ZONE)", "SELECT timezone('UTC',TIMESTAMP '2000-01-02 03:04:05')::text AS v", "TEXT", "2000-01-02 03:04:05+00"},
		}
		for _, tc := range cases {
			if !t.Run(tc.label, func(t *testing.T) {
				run := func(s fixture.Step) {
					s.Prepared = prepared
					if !t.Run(s.Query, func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }) {
						t.FailNow()
					}
				}
				defer objectGrantRoutineCleanup(t, p, c, []string{tc.canonical})
				want := fixture.Select(tc.query, []string{"v"}, []string{tc.typ}, [][]any{{tc.value}})
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+tc.canonical+" FROM PUBLIC", 0))
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					fixture.RoleGrantPermissionDenied(run, tc.query, "function", strings.Split(strings.TrimPrefix(tc.canonical, "pg_catalog."), "(")[0])
				})
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+tc.declaration+" TO og_object_reader", 0))
				fixture.RoleGrantActor(run, "og_object_reader", func() { run(want) })
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+tc.declaration+" FROM og_object_reader", 0))
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					fixture.RoleGrantPermissionDenied(run, tc.query, "function", strings.Split(strings.TrimPrefix(tc.canonical, "pg_catalog."), "(")[0])
				})
			}) {
				t.FailNow()
			}
		}

		// Each malformed late target has a real, valid earlier target. Failure
		// must publish no ACL for that prefix, including a complete-input suffix.
		defer objectGrantRoutineCleanup(t, p, c, []string{"pg_catalog.abs(integer)"})
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.abs(integer) FROM PUBLIC", 0))
		for _, tc := range []struct{ tail, token string }{
			{"pg_catalog.abs(OUT) TO og_object_reader", ")"},
			{"pg_catalog.abs(OUT OUT INTEGER) TO og_object_reader", "OUT"},
			{"pg_catalog.abs(IN x INTEGER,) TO og_object_reader", ")"},
			{"pg_catalog.abs(IN x INTEGER,,INTEGER) TO og_object_reader", ","},
			{"pg_catalog.abs(foo WITH) TO og_object_reader", "WITH"},
			{"pg_catalog.abs(between INTEGER) TO og_object_reader", "between"},
			{"pg_catalog.abs(IN x INTEGER) TO og_object_reader trailing", "trailing"},
		} {
			q := "GRANT EXECUTE ON FUNCTION pg_catalog.abs(integer)," + tc.tail
			// The doubled comma case points at the second comma, not a later
			// delimiter. All other token positions are the last occurrence.
			position := strings.LastIndex(q, tc.token) + 1
			if strings.Contains(tc.tail, ",,") {
				position = strings.Index(q, ",,") + 2
			}
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "` + tc.token + `"`, Position: fixture.Position(position)})
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			run(s)
			fixture.RoleGrantActor(run, "og_object_reader", func() { fixture.RoleGrantPermissionDenied(run, "SELECT abs(-7::integer) AS v", "function", "abs") })
		}
		fixture.RoleGrantError(run, "GRANT EXECUTE ON FUNCTION pg_catalog.abs(integer),pg_catalog.abs(og_missing_type) TO og_object_reader", "42704", `type "og_missing_type" does not exist`)
		fixture.RoleGrantActor(run, "og_object_reader", func() { fixture.RoleGrantPermissionDenied(run, "SELECT abs(-7::integer) AS v", "function", "abs") })
		// Valid compound types retain their identity; they are not silently
		// reduced to CHAR/BIT, TEXT, or a matching ABS numeric overload.
		for _, tc := range []struct{ declaration, identity string }{
			{"CHARACTER VARYING(12)", "character varying"},
			{"CHAR VARYING(12)", "character varying"},
			{"BIT VARYING(3)", "bit varying"},
		} {
			fixture.RoleGrantError(run, "GRANT EXECUTE ON FUNCTION pg_catalog.abs("+tc.declaration+") TO og_object_reader", "42883", "function pg_catalog.abs("+tc.identity+") does not exist")
		}
		// Omitted vs empty, missing and ambiguous identities, and wrong routine
		// kind are already permanent witnesses in ObjectGrantRoutinesScalar.
		fixture.RoleGrantError(run, "GRANT EXECUTE(x) ON FUNCTION pg_catalog.abs(integer) TO og_object_reader", "0LP01", "column privileges are only valid for relations")
		fixture.RoleGrantError(run, "GRANT EXECUTE ON FUNCTION pg_catalog.abs(integer) TO PUBLIC WITH GRANT OPTION", "0LP01", "grant options can only be granted to roles")
		fixture.RoleGrantActor(run, "og_object_reader", func() { fixture.RoleGrantPermissionDenied(run, "SELECT abs(-7::integer) AS v", "function", "abs") })
	})
}

func TestReferenceObjectGrantNativeBoundaries(t *testing.T) {
	objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		// Handlerless SQL/MED metadata is real on both backends. Language
		// execution and large-object storage retain their independent boundaries.
		defer func() {
			for _, q := range []string{
				"RESET SESSION AUTHORIZATION",
				"DROP SERVER IF EXISTS og_boundary_created CASCADE",
				"DROP SERVER IF EXISTS og_boundary_server CASCADE",
				"DROP FOREIGN DATA WRAPPER IF EXISTS og_boundary_fdw CASCADE",
			} {
				if _, err := c.ExecContext(p.Ctx, q); err != nil {
					t.Error("foreign metadata cleanup", q, err)
				}
			}
		}()
		run(fixture.Exec("CREATE FOREIGN DATA WRAPPER og_boundary_fdw NO HANDLER NO VALIDATOR", 0))
		run(fixture.Exec("CREATE SERVER og_boundary_server FOREIGN DATA WRAPPER og_boundary_fdw", 0))
		var lo uint32
		if b.PG {
			defer func() {
				queries := []string{
					"RESET SESSION AUTHORIZATION",
					"GRANT USAGE ON LANGUAGE plpgsql TO PUBLIC",
					"REVOKE USAGE ON LANGUAGE plpgsql FROM og_object_reader,og_object_other CASCADE",
					"DROP FUNCTION IF EXISTS public.og_boundary_lang()",
					"DROP FUNCTION IF EXISTS public.og_boundary_lang_denied()",
				}
				if lo != 0 {
					queries = append(queries, fmt.Sprintf("SELECT lo_unlink(%d)", lo))
				}
				for _, q := range queries {
					if _, err := c.ExecContext(p.Ctx, q); err != nil {
						t.Error("external facility cleanup", q, err)
					}
				}
			}()
			if err := c.QueryRowContext(p.Ctx, "SELECT lo_create(0)").Scan(&lo); err != nil {
				t.Fatal(err)
			}
			if _, err := c.ExecContext(p.Ctx, fmt.Sprintf("SELECT lo_put(%d,0,decode('6162','hex'))", lo)); err != nil {
				t.Fatal(err)
			}
		}
		loTarget := fmt.Sprint(lo)
		if !b.PG {
			loTarget = "987654321"
		}
		for _, tc := range []struct{ target, privilege, noun, unsupported string }{
			{"LANGUAGE plpgsql", "USAGE", "language", "LANGUAGE"},
			{"FOREIGN DATA WRAPPER og_boundary_fdw", "USAGE", "foreign-data wrapper", "FOREIGN DATA WRAPPER"},
			{"FOREIGN SERVER og_boundary_server", "USAGE", "foreign server", "FOREIGN SERVER"},
			{"LARGE OBJECT " + loTarget, "SELECT", "large object", "LARGE OBJECT"},
		} {
			for _, privilege := range []string{"TEMP", "TEMPORARY", "EXECUTE"} {
				canonical := privilege
				if canonical == "TEMPORARY" {
					canonical = "TEMP"
				}
				fixture.RoleGrantError(run, "GRANT "+privilege+" ON "+tc.target+" TO og_object_reader", "0LP01", "invalid privilege type "+canonical+" for "+tc.noun)
			}
			fixture.RoleGrantError(run, "GRANT invented ON "+tc.target+" TO og_object_reader", "42601", `unrecognized privilege type "invented"`)
			fixture.RoleGrantError(run, "GRANT "+tc.privilege+"(x) ON "+tc.target+" TO og_object_reader", "0LP01", "column privileges are only valid for relations")
			for _, privilege := range []string{tc.privilege, "ALL PRIVILEGES"} {
				q := "GRANT " + privilege + " ON " + tc.target + " TO og_object_reader"
				if b.PG || strings.HasPrefix(tc.target, "FOREIGN ") {
					run(fixture.Exec(q, 0))
				} else {
					fixture.RoleGrantError(run, q, "0A000", tc.unsupported+" privileges are unsupported in embedded mode")
				}
			}
			if !b.PG && !strings.HasPrefix(tc.target, "FOREIGN ") {
				fixture.RoleGrantError(run, "REVOKE "+tc.privilege+" ON "+tc.target+" FROM og_object_reader", "0A000", tc.unsupported+" privileges are unsupported in embedded mode")
				fixture.RoleGrantError(run, "REVOKE GRANT OPTION FOR "+tc.privilege+" ON "+tc.target+" FROM og_object_reader", "0A000", tc.unsupported+" privileges are unsupported in embedded mode")
				fixture.RoleGrantError(run, "REVOKE "+tc.privilege+" ON "+tc.target+" FROM og_object_reader CASCADE", "0A000", tc.unsupported+" privileges are unsupported in embedded mode")
			}
		}
		run(fixture.Exec("REVOKE USAGE ON FOREIGN DATA WRAPPER og_boundary_fdw FROM og_object_reader", 0))
		createServer := "CREATE SERVER og_boundary_created FOREIGN DATA WRAPPER og_boundary_fdw"
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			fixture.RoleGrantPermissionDenied(run, createServer, "foreign-data wrapper", "og_boundary_fdw")
		})
		run(fixture.Exec("GRANT USAGE ON FOREIGN DATA WRAPPER og_boundary_fdw TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() { run(fixture.Exec(createServer, 0)) })
		run(fixture.Exec("REVOKE USAGE ON FOREIGN SERVER og_boundary_server FROM og_object_reader", 0))
		createMapping := "CREATE USER MAPPING FOR CURRENT_USER SERVER og_boundary_server OPTIONS (user 'reader')"
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			fixture.RoleGrantPermissionDenied(run, createMapping, "foreign server", "og_boundary_server")
		})
		run(fixture.Exec("GRANT USAGE ON FOREIGN SERVER og_boundary_server TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() { run(fixture.Exec(createMapping, 0)) })
		if b.PG {
			run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO og_object_reader", 0))
			run(fixture.Exec("REVOKE USAGE ON LANGUAGE plpgsql FROM PUBLIC,og_object_reader", 0))
			createFunction := "CREATE FUNCTION public.og_boundary_lang() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 7; END$$"
			fixture.RoleGrantActor(run, "og_object_reader", func() { fixture.RoleGrantPermissionDenied(run, createFunction, "language", "plpgsql") })
			run(fixture.Exec("GRANT USAGE ON LANGUAGE plpgsql TO og_object_reader", 0))
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				run(fixture.Exec(createFunction, 0))
				run(fixture.Select("SELECT public.og_boundary_lang() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
			})
			run(fixture.Exec("REVOKE USAGE ON LANGUAGE plpgsql FROM og_object_reader", 0))
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				fixture.RoleGrantPermissionDenied(run, "CREATE FUNCTION public.og_boundary_lang_denied() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 7; END$$", "language", "plpgsql")
				run(fixture.Select("SELECT public.og_boundary_lang() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
			})

			run(fixture.Exec("REVOKE ALL ON LARGE OBJECT "+loTarget+" FROM og_object_reader", 0))
			read := "SELECT encode(lo_get(" + loTarget + "),'hex') AS v"
			write := "SELECT lo_put(" + loTarget + ",0,decode('6364','hex'))"
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				fixture.RoleGrantPermissionDenied(run, read, "large object", loTarget)
				fixture.RoleGrantPermissionDenied(run, write, "large object", loTarget)
			})
			run(fixture.Exec("GRANT SELECT ON LARGE OBJECT "+loTarget+" TO og_object_reader", 0))
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				run(fixture.Select(read, []string{"v"}, []string{"TEXT"}, [][]any{{"6162"}}))
				fixture.RoleGrantPermissionDenied(run, write, "large object", loTarget)
			})
			run(fixture.Exec("GRANT UPDATE ON LARGE OBJECT "+loTarget+" TO og_object_reader", 0))
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				run(fixture.Exec(write, 1))
				run(fixture.Select(read, []string{"v"}, []string{"TEXT"}, [][]any{{"6364"}}))
			})
			run(fixture.Exec("REVOKE SELECT ON LARGE OBJECT "+loTarget+" FROM og_object_reader", 0))
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				fixture.RoleGrantPermissionDenied(run, read, "large object", loTarget)
				// INV_WRITE also requests read permission (REL_18_0 inv_open).
				fixture.RoleGrantPermissionDenied(run, write, "large object", loTarget)
			})
			run(fixture.Exec("GRANT SELECT ON LARGE OBJECT "+loTarget+" TO og_object_reader", 0))
			run(fixture.Exec("REVOKE UPDATE ON LARGE OBJECT "+loTarget+" FROM og_object_reader", 0))
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				run(fixture.Select(read, []string{"v"}, []string{"TEXT"}, [][]any{{"6364"}}))
				fixture.RoleGrantPermissionDenied(run, write, "large object", loTarget)
			})
			run(fixture.Exec("REVOKE SELECT,UPDATE ON LARGE OBJECT "+loTarget+" FROM og_object_reader", 0))
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				fixture.RoleGrantPermissionDenied(run, read, "large object", loTarget)
				fixture.RoleGrantPermissionDenied(run, write, "large object", loTarget)
			})
			run(fixture.Select(read, []string{"v"}, []string{"TEXT"}, [][]any{{"6364"}}))
		}

		// Numeric series now has a genuine provider; the original route must
		// grant and execute rather than retain an obsolete unsupported error.
		numeric := "pg_catalog.generate_series(numeric,numeric)"
		defer objectGrantRoutineCleanup(t, p, c, []string{numeric})
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+numeric+" TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Select("SELECT generate_series(0.1::numeric,1.1::numeric) AS v", []string{"v"}, []string{"NUMERIC"}, [][]any{{"0.1"}, {"1.1"}}))
		})
		// Temporal series remains lookup-only. Preserve the independent
		// unsupported-target and late-target atomicity witnesses below.
		lookupOnly := "pg_catalog.generate_series(timestamp without time zone,timestamp without time zone,interval)"
		if b.PG {
			defer objectGrantRoutineCleanup(t, p, c, []string{lookupOnly})
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+lookupOnly+" TO og_object_reader", 0))
		} else {
			fixture.RoleGrantError(run, "GRANT EXECUTE ON FUNCTION "+lookupOnly+" TO og_object_reader", "0A000", "routine privileges are unsupported for unimplemented routines in embedded mode")
		}
		defer objectGrantRoutineCleanup(t, p, c, []string{"pg_catalog.abs(integer)"})
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.abs(integer) FROM PUBLIC", 0))
		late := "GRANT EXECUTE ON FUNCTION pg_catalog.abs(integer)," + lookupOnly + " TO og_object_reader"
		if b.PG {
			run(fixture.Exec(late, 0))
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				run(fixture.Select("SELECT abs(-7::integer) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
			})
			run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.abs(integer) FROM og_object_reader", 0))
		} else {
			fixture.RoleGrantError(run, late, "0A000", "routine privileges are unsupported for unimplemented routines in embedded mode")
			fixture.RoleGrantActor(run, "og_object_reader", func() { fixture.RoleGrantPermissionDenied(run, "SELECT abs(-7::integer) AS v", "function", "abs") })
		}

		// Read-only rejection precedes lookup, ACL work, and relation locking.
		// Each error gets a separate transaction: PostgreSQL aborts on error.
		for _, q := range []string{
			"GRANT EXECUTE ON FUNCTION pg_catalog.og_boundary_missing(integer) TO og_object_reader",
			"REVOKE EXECUTE ON FUNCTION pg_catalog.og_boundary_missing(integer) FROM og_object_reader",
		} {
			tx, err := c.BeginTx(p.Ctx, &sql.TxOptions{ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer tx.Rollback()
				command := strings.Fields(q)[0]
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "25006", Message: "cannot execute " + command + " in a read-only transaction"})
				s.Prepared, s.ErrorStage = prepared, "execute"
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, s)
			}()
		}
		for _, q := range []string{
			"GRANT EXECUTE ON FUNCTION pg_catalog.abs(integer) TO og_object_reader",
			"REVOKE EXECUTE ON FUNCTION pg_catalog.abs(integer) FROM og_object_reader",
		} {
			tx, err := c.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer tx.Rollback()
				s := fixture.Exec(q, 0)
				if !b.PG {
					s = fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "statement is unsupported in an explicit transaction"})
					s.ErrorStage = "execute"
				}
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, s)
			}()
		}

		// Plain pg_node_tree grouping is an existing private native comparison,
		// not a released callable equality routine. Recursive private grouping
		// support does not register a SQL array type or constructor for this leaf.
		run(fixture.Exec("CREATE TABLE og_boundary_tree(n integer DEFAULT 7)", 0))
		for _, expression := range []string{"adbin", "ARRAY[adbin]"} {
			q := "SELECT count(*) AS v FROM (SELECT DISTINCT " + expression + " FROM pg_attrdef a JOIN pg_class c ON c.oid=a.adrelid WHERE c.relname='og_boundary_tree') q"
			if expression != "adbin" {
				d := fixture.Diagnostic{Code: "42704", Message: "could not find array type for data type pg_node_tree", Position: fixture.Position(strings.Index(q, expression) + 1)}
				if !b.PG {
					// bindArray requires LookupArray's registered concrete type;
					// pg_node_tree deliberately has no such SQL array descriptor.
					d.Code, d.Message = "0A000", "unsupported array element type"
				}
				s := fixture.ImplicitError(q, d)
				s.SelectRows, s.ErrorStage = true, "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				run(s)
			} else {
				run(fixture.Select(q, []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}}))
			}
		}
		defer objectGrantRoutineCleanup(t, p, c, []string{"pg_catalog.texteq(text,text)", "pg_catalog.array_eq(anyarray,anyarray)"})
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.texteq(text,text),pg_catalog.array_eq(anyarray,anyarray) FROM PUBLIC", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			q := "SELECT count(*) AS v FROM (SELECT DISTINCT adbin FROM pg_attrdef a JOIN pg_class c ON c.oid=a.adrelid WHERE c.relname='og_boundary_tree') q"
			if b.PG {
				fixture.RoleGrantPermissionDenied(run, q, "function", "texteq")
			} else {
				run(fixture.Select(q, []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}}))
				q = "SELECT count(*) AS v FROM (SELECT DISTINCT ARRAY[adbin] FROM pg_attrdef a JOIN pg_class c ON c.oid=a.adrelid WHERE c.relname='og_boundary_tree') q"
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "unsupported array element type", Position: fixture.Position(strings.Index(q, "ARRAY[") + 1)})
				s.SelectRows, s.ErrorStage = true, "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				run(s)
			}
		})
		// JSON has no released equality operator, even when both operands are
		// typed NULL. This is analysis-time resolution, not a strict-call skip
		// or the native unhandled-scalar execution boundary.
		for _, q := range []string{
			"SELECT '{}'::json = '{}'::json AS v",
			"SELECT NULL::json = NULL::json AS v",
		} {
			s := fixture.ImplicitError(q, fixture.Diagnostic{
				Code: "42883", Message: "operator does not exist: json = json",
				Hint:     "No operator matches the given name and argument types. You might need to add explicit type casts.",
				Position: fixture.Position(strings.Index(q, "=") + 1),
			})
			s.SelectRows, s.ErrorStage = true, "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			run(s)
		}
		// Genuine JSONB comparison and valid zero results must survive bounded
		// rejection of unimplemented scalar comparisons; NULL remains SQL NULL.
		run(fixture.Select("SELECT '{}'::jsonb='{}'::jsonb AS equal,'{}'::jsonb<'{}'::jsonb AS less,NULL::jsonb='{}'::jsonb AS unknown", []string{"equal", "less", "unknown"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, false, nil}}))
	})
}
