package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Capture identity and the ACTUAL original labels/owner before mutation or replay.
// This intentionally does not assume that the fixture login owns system routines.
func routineAlterBuiltinCleanup(t *testing.T, p *fixture.Pair, c *sql.Conn, signature string) func() {
	t.Helper()
	var oid int64
	var schema, name, owner string
	if err := c.QueryRowContext(p.Ctx, "SELECT p.oid,n.nspname,p.proname,r.rolname FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_roles r ON r.oid=p.proowner WHERE p.oid=$1::regprocedure", signature).Scan(&oid, &schema, &name, &owner); err != nil {
		t.Fatal(err)
	}
	args := signature[strings.IndexByte(signature, '('):]
	quote := func(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
	return func() {
		t.Helper()
		if _, err := c.ExecContext(p.Ctx, "SET SESSION AUTHORIZATION DEFAULT"); err != nil {
			t.Error("restore builtin authority", err)
			return
		}
		var currentSchema, currentName string
		if err := c.QueryRowContext(p.Ctx, "SELECT n.nspname,p.proname FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE p.oid=$1::oid", oid).Scan(&currentSchema, &currentName); err != nil {
			t.Error("locate original builtin identity", err)
			return
		}
		qualified := quote(currentSchema) + "." + quote(currentName) + args
		if _, err := c.ExecContext(p.Ctx, "ALTER FUNCTION "+qualified+" OWNER TO "+quote(owner)); err != nil {
			t.Error("restore builtin owner", err)
			return
		}
		if currentSchema != schema {
			if _, err := c.ExecContext(p.Ctx, "ALTER FUNCTION "+qualified+" SET SCHEMA "+quote(schema)); err != nil {
				t.Error("restore builtin schema", err)
				return
			}
			qualified = quote(schema) + "." + quote(currentName) + args
		}
		if currentName != name {
			if _, err := c.ExecContext(p.Ctx, "ALTER FUNCTION "+qualified+" RENAME TO "+quote(name)); err != nil {
				t.Error("restore builtin name", err)
			}
		}
	}
}

type routineBuiltinCaptureCase struct {
	name, signature, source, body, typ string
	rows                               [][]any
}

func routineBuiltinCaptureCases() []routineBuiltinCaptureCase {
	return []routineBuiltinCaptureCase{
		{"window", "pg_catalog.row_number()", "SELECT pg_catalog.row_number() OVER(ORDER BY x) AS v FROM(VALUES(2),(3)) s(x)", "RETURN (SELECT n FROM (SELECT pg_catalog.row_number() OVER(ORDER BY x) AS n FROM(VALUES(2),(3)) s(x)) q WHERE n=$1)", "INT8", [][]any{{int64(1)}, {int64(2)}}},
		{"sum", "pg_catalog.sum(integer)", "SELECT pg_catalog.sum(x) AS v FROM(VALUES(2),(3)) s(x)", "RETURN (SELECT pg_catalog.sum(x) FROM(VALUES(2),(3)) s(x))", "INT8", [][]any{{int64(5)}}},
		// Both COUNT declarations retain independent authority in one consumer.
		// Their different argument vectors need not share computation.
		{"count_input", `pg_catalog.count("any")`, "SELECT pg_catalog.count(*)+pg_catalog.count(1) AS v FROM(VALUES(2),(3)) s(x)", "RETURN (SELECT pg_catalog.count(*)+pg_catalog.count(1) FROM(VALUES(2),(3)) s(x))", "INT8", [][]any{{int64(4)}}},
		{"count_star", "pg_catalog.count()", "SELECT pg_catalog.count(1)+pg_catalog.count(*) AS v FROM(VALUES(2),(3)) s(x)", "RETURN (SELECT pg_catalog.count(1)+pg_catalog.count(*) FROM(VALUES(2),(3)) s(x))", "INT8", [][]any{{int64(4)}}},
		{"every_alias", "pg_catalog.every(boolean)", "SELECT pg_catalog.bool_and(x) AND pg_catalog.every(x) AS v FROM(VALUES(true),(false)) s(x)", "RETURN (SELECT pg_catalog.bool_and(x) AND pg_catalog.every(x) FROM(VALUES(true),(false)) s(x))", "BOOL", [][]any{{false}}},
		{"bool_and_alias", "pg_catalog.bool_and(boolean)", "SELECT pg_catalog.every(x) AND pg_catalog.bool_and(x) AS v FROM(VALUES(true),(false)) s(x)", "RETURN (SELECT pg_catalog.every(x) AND pg_catalog.bool_and(x) FROM(VALUES(true),(false)) s(x))", "BOOL", [][]any{{false}}},
	}
}

func routineBuiltinCaptureScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func routineBuiltinCaptureCreate(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, tc routineBuiltinCaptureCase, kind, prefix string) string {
	t.Helper()
	if kind == "view" {
		routineExec(t, p, b, c, "CREATE VIEW "+prefix+" AS "+tc.source)
		return "SELECT v FROM " + prefix + " ORDER BY v"
	}
	routineExec(t, p, b, c, "CREATE FUNCTION "+prefix+"(bigint) RETURNS "+tc.typ+" "+tc.body)
	if tc.name == "window" {
		return "SELECT " + prefix + "(x::bigint) AS v FROM(VALUES(1),(2)) s(x) ORDER BY v"
	}
	return "SELECT " + prefix + "(1::bigint) AS v"
}

func routineBuiltinCaptureCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, tc routineBuiltinCaptureCase) {
	t.Helper()
	// The ordinary shared runner asserts declared names/types and exact ordered
	// rows on both direct and prepared routes, not only presentation validation.
	routineAlterRows(t, p, b, c, q, []string{"v"}, []string{tc.typ}, tc.rows)
}

func TestReferenceRoutineAlterBuiltinCaptures(t *testing.T) {
	for _, tc := range routineBuiltinCaptureCases() {
		for _, kind := range []string{"view", "standard"} {
			for _, transition := range []string{"rename", "move", "owner"} {
				t.Run(tc.name+"/"+kind+"/"+transition, func(t *testing.T) {
					fixture.RoleReferenceCleanup(t, []string{"ra_capture_owner", "ra_capture_reader"})
					fixture.SchemaReferenceCleanup(t, "ra_capture")
					routineBuiltinCaptureScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						restore := routineAlterBuiltinCleanup(t, p, c, tc.signature)
						defer restore()
						routineExec(t, p, b, c, "CREATE ROLE ra_capture_owner", "CREATE ROLE ra_capture_reader", "CREATE SCHEMA ra_capture AUTHORIZATION ra_capture_owner", "GRANT USAGE ON SCHEMA ra_capture TO ra_capture_reader")
						routineExec(t, p, b, c, "GRANT USAGE ON SCHEMA public TO ra_capture_reader")
						q := routineBuiltinCaptureCreate(t, p, b, c, tc, kind, "ra_capture_saved")
						routineBuiltinCaptureCheck(t, p, b, c, q, tc)
						if kind == "view" {
							routineExec(t, p, b, c, "GRANT SELECT ON ra_capture_saved TO ra_capture_reader")
						}
						args := tc.signature[strings.IndexByte(tc.signature, '('):]
						current := tc.signature
						switch transition {
						case "rename":
							routineExec(t, p, b, c, "ALTER FUNCTION "+current+" RENAME TO ra_capture_builtin")
							current = "pg_catalog.ra_capture_builtin" + args
						case "move":
							routineExec(t, p, b, c, "ALTER FUNCTION "+current+" SET SCHEMA ra_capture")
							current = "ra_capture." + strings.TrimPrefix(current, "pg_catalog.")
						case "owner":
							routineExec(t, p, b, c, "ALTER FUNCTION "+current+" OWNER TO ra_capture_owner")
						}
						// Probe the genuinely pre-transition frozen root immediately.
						routineBuiltinCaptureCheck(t, p, b, c, q, tc)
						if transition != "owner" {
							// Exact utility identity is absent even when ordinary SUM
							// source can legitimately select another coercible overload.
							if tc.name == "sum" {
								routineAlterError(t, p, b, c, "ALTER FUNCTION "+tc.signature+" RENAME TO ra_capture_absent", "42883", "function pg_catalog.sum(integer) does not exist")
							} else {
								head := tc.signature[:strings.IndexByte(tc.signature, '(')]
								input := tc.signature[strings.IndexByte(tc.signature, '('):]
								occurrence := head
								if tc.name == "count_input" {
									input, occurrence = "(integer)", head+"(1)"
								} else if tc.name == "count_star" {
									occurrence = head + "(*)"
								}
								want := fixture.ControlsReferenceDiagnostic("42883", "function "+head+input+" does not exist")
								want.Position = fixture.Position(strings.Index(tc.source, occurrence) + 1)
								want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
								for _, prepared := range []bool{false, true} {
									s := fixture.ErrorDiagnostic(tc.source, want)
									s.Prepared, s.SelectRows, s.ErrorStage = prepared, true, "execute"
									if prepared {
										s.ErrorStage = "prepare"
									}
									fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
								}
							}
						}
						// Current EXECUTE is a live obligation even for frozen recipes.
						defer func() {
							for _, sql := range []string{"SET SESSION AUTHORIZATION DEFAULT", "GRANT EXECUTE ON FUNCTION " + current + " TO PUBLIC", "REVOKE EXECUTE ON FUNCTION " + current + " FROM ra_capture_reader"} {
								if _, err := c.ExecContext(p.Ctx, sql); err != nil {
									t.Error("restore builtin EXECUTE", err)
								}
							}
						}()
						routineExec(t, p, b, c, "REVOKE EXECUTE ON FUNCTION "+current+" FROM PUBLIC", "SET ROLE ra_capture_reader")
						label := current[:strings.IndexByte(current, '(')]
						label = label[strings.LastIndexByte(label, '.')+1:]
						noun := "aggregate"
						if tc.name == "window" {
							noun = "function"
						}
						want := fixture.ControlsReferenceDiagnostic("42501", "permission denied for "+noun+" "+label)
						if kind == "standard" {
							want.Context = "SQL function \"ra_capture_saved\" statement 1"
						}
						denied := fixture.ErrorDiagnostic(q, want)
						denied.SelectRows, denied.ErrorStage = true, "execute"
						fixture.RunStep(t, p.Ctx, c, b.Recorder, denied)
						routineExec(t, p, b, c, "RESET ROLE", "GRANT EXECUTE ON FUNCTION "+current+" TO ra_capture_reader", "SET ROLE ra_capture_reader")
						routineBuiltinCaptureCheck(t, p, b, c, q, tc)
						routineExec(t, p, b, c, "RESET ROLE", "REVOKE EXECUTE ON FUNCTION "+current+" FROM ra_capture_reader", "GRANT EXECUTE ON FUNCTION "+current+" TO PUBLIC")
					})
				})
			}
		}
	}
}

func TestReferenceRoutineAlterBuiltinCapturePersistence(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ra_capture_owner"})
	fixture.SchemaReferenceCleanup(t, "ra_capture")
	routineBuiltinCaptureScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		cases := routineBuiltinCaptureCases()
		for _, tc := range cases {
			restore := routineAlterBuiltinCleanup(t, p, c, tc.signature)
			defer restore()
		}
		routineExec(t, p, b, c, "CREATE ROLE ra_capture_owner", "CREATE SCHEMA ra_capture AUTHORIZATION ra_capture_owner")
		type witness struct {
			query string
			tc    routineBuiltinCaptureCase
		}
		var witnesses []witness
		for _, tc := range cases {
			for _, kind := range []string{"view", "standard"} {
				q := routineBuiltinCaptureCreate(t, p, b, c, tc, kind, "ra_capture."+tc.name+"_"+kind)
				witnesses = append(witnesses, witness{q, tc})
			}
		}
		var temporaries []witness
		cleanupTemporary := func() {
			for _, w := range temporaries {
				if _, err := c.ExecContext(p.Ctx, "DROP VIEW IF EXISTS pg_temp.ra_capture_temp_"+w.tc.name); err != nil {
					t.Error("drop retained capture witness", err)
				}
			}
			temporaries = nil
		}
		defer cleanupTemporary()
		for _, tc := range cases {
			name := "ra_capture_temp_" + tc.name
			routineExec(t, p, b, c, "CREATE TEMP VIEW "+name+" AS "+tc.source)
			temporaries = append(temporaries, witness{"SELECT v FROM " + name + " ORDER BY v", tc})
		}
		for _, tc := range cases {
			args := tc.signature[strings.IndexByte(tc.signature, '('):]
			name := "ra_" + tc.name
			routineExec(t, p, b, c, "ALTER FUNCTION "+tc.signature+" RENAME TO "+name, "ALTER FUNCTION pg_catalog."+name+args+" SET SCHEMA ra_capture", "ALTER FUNCTION ra_capture."+name+args+" OWNER TO ra_capture_owner")
		}
		check := func(conn *sql.Conn, backend fixture.Backend) {
			for _, w := range witnesses {
				routineBuiltinCaptureCheck(t, p, backend, conn, w.query, w.tc)
			}
		}
		check(c, b)
		checkTemporary := func() {
			for _, w := range temporaries {
				routineBuiltinCaptureCheck(t, p, b, c, w.query, w.tc)
			}
		}
		checkTemporary()
		if b.PG {
			return
		}
		var retained bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &retained); err != nil {
			t.Fatal(err)
		}
		// Restore without Reset must retain this session's original captures,
		// separately from the imported persistent declarations and role domain.
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(retained.Bytes())); err != nil {
			t.Fatal(err)
		}
		check(c, b)
		checkTemporary()
		cleanupTemporary()
		for range 2 {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			check(c, b)
			connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/fresh")
			if err != nil {
				t.Fatal(err)
			}
			fresh := sql.OpenDB(connector)
			func() {
				defer fresh.Close()
				if _, err := fresh.ExecContext(p.Ctx, "CREATE ROLE ra_capture_owner"); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				recorded := sql.OpenDB(fixture.MigrationRecordedConnector(t, connector))
				defer recorded.Close()
				recorder := &fixture.Recorder{RawBackend: b.Recorder.RawBackend}
				for _, w := range witnesses {
					for _, nonContext := range []bool{false, true} {
						s := fixture.Select(w.query, []string{"v"}, []string{w.tc.typ}, w.tc.rows)
						s.NonContext = nonContext
						fixture.RunStep(t, p.Ctx, recorded, recorder, s)
						fixture.RunStep(t, p.Ctx, recorded, recorder, fixture.Prepared(s))
					}
				}
			}()
			if p.Postgres != nil {
				func() {
					pg, err := p.Postgres.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer pg.Close()
					var restores []func()
					for _, tc := range cases {
						restores = append(restores, routineAlterBuiltinCleanup(t, p, pg, tc.signature))
					}
					defer func() {
						for i := len(restores) - 1; i >= 0; i-- {
							restores[i]()
						}
						if _, err := pg.ExecContext(p.Ctx, "DROP SCHEMA IF EXISTS ra_capture CASCADE; DROP ROLE IF EXISTS ra_capture_owner"); err != nil {
							t.Error(err)
						}
					}()
					if _, err := pg.ExecContext(p.Ctx, "CREATE ROLE ra_capture_owner"); err != nil {
						t.Fatal(err)
					}
					if _, err := pg.ExecContext(p.Ctx, dump.String()); err != nil {
						t.Fatal("actual builtin capture stream replay", err)
					}
					check(pg, fixture.Backend{PG: true, Recorder: p.PostgresRecord})
				}()
			}
		}
	})
}
