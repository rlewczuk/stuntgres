package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceSQLCursorMotionProjectionEffects(t *testing.T) {
	// The projected sequence is late for these routes. Expectations below come
	// from PostgreSQL's documented traversal and release-source receiver order;
	// the pinned oracle must establish the actual plan/cache boundaries first.
	for _, route := range []struct{ name, query string }{
		{"late", "SELECT i,nextval('motion_draw') AS n FROM cursor_rows ORDER BY i"},
		{"derived", "SELECT i,nextval('motion_draw') AS n FROM (SELECT i FROM cursor_rows ORDER BY i OFFSET 0) AS ordered"},
		{"view", "SELECT i,nextval('motion_draw') AS n FROM motion_view ORDER BY i"},
	} {
		t.Run(route.name, func(t *testing.T) {
			sqlCursorMotionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, step func(fixture.Step)) {
				sqlCursorSetup(t, p, b, c)
				step(fixture.Exec("CREATE VIEW motion_view AS SELECT i FROM cursor_rows", 0))
				step(fixture.Exec("CREATE SEQUENCE motion_draw START 10", 0))
				step(fixture.Exec("BEGIN", 0))
				step(fixture.Exec("DECLARE c SCROLL CURSOR FOR "+route.query, 0))
				state := func(n int64, called bool) {
					step(fixture.Select("SELECT last_value,is_called FROM motion_draw", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{n, called}}))
				}
				fetch := func(direction string, i, n int64) {
					step(fixture.Select("FETCH "+direction+" FROM c", []string{"i", "n"}, []string{"INT4", "INT8"}, [][]any{{i, n}}))
				}
				state(10, false)
				step(fixture.Exec("MOVE FORWARD 2 FROM c", 2))
				state(11, true)
				fetch("0", 2, 13)
				state(13, true)
				step(fixture.Exec("MOVE 0 FROM c", 1))
				state(13, true)
				fetch("PRIOR", 1, 14)
				state(14, true)
				fetch("ABSOLUTE 3", 3, 16)
				state(16, true)
				step(fixture.Exec("MOVE BACKWARD ALL FROM c", 2))
				state(16, true)
				fetch("RELATIVE 2", 2, 18)
				state(18, true)
				fetch("ABSOLUTE 1", 1, 19)
				state(19, true)
				fetch("ABSOLUTE -1", 3, 22)
				state(22, true)
			})
		})
	}
	for _, route := range []struct {
		name, query     string
		initial, rewind int64
	}{
		{"order_cache", "SELECT i,nextval('motion_draw') AS n FROM cursor_rows ORDER BY n", 12, 12},
		{"volatile_where", "SELECT i FROM cursor_rows WHERE nextval('motion_draw') > 0", 10, 11},
		{"volatile_values", "VALUES (nextval('motion_draw')),(nextval('motion_draw'))", 10, 11},
	} {
		t.Run(route.name, func(t *testing.T) {
			sqlCursorMotionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, step func(fixture.Step)) {
				sqlCursorSetup(t, p, b, c)
				step(fixture.Exec("CREATE SEQUENCE motion_draw START 10", 0))
				step(fixture.Exec("BEGIN", 0))
				step(fixture.Exec("DECLARE c SCROLL CURSOR FOR "+route.query, 0))
				state := func(n int64, called bool) {
					step(fixture.Select("SELECT last_value,is_called FROM motion_draw", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{n, called}}))
				}
				state(10, false)
				step(fixture.Exec("MOVE NEXT FROM c", 1))
				state(route.initial, true)
				step(fixture.Exec("MOVE ABSOLUTE 0 FROM c", 0))
				state(route.initial, true)
				step(fixture.Exec("MOVE NEXT FROM c", 1))
				state(route.rewind, true)
				step(fixture.Exec("MOVE BACKWARD ALL FROM c", 0))
				state(route.rewind, true)
				step(fixture.Exec("MOVE NEXT FROM c", 1))
				want := route.rewind
				if route.name != "order_cache" {
					want++
				}
				state(want, true)
				// Positive absolute chooses rewind when (target-1)<=position/2.
				step(fixture.Exec("MOVE ABSOLUTE 1 FROM c", 1))
				if route.name != "order_cache" {
					want++
				}
				state(want, true)
			})
		})
	}
	for _, motion := range []string{"MOVE FORWARD 2", "MOVE RELATIVE 2", "FETCH RELATIVE 2", "MOVE ABSOLUTE 2"} {
		t.Run(motion, func(t *testing.T) {
			sqlCursorMotionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, step func(fixture.Step)) {
				sqlCursorSetup(t, p, b, c)
				step(fixture.Exec("BEGIN", 0))
				step(fixture.Exec("DECLARE c SCROLL CURSOR FOR SELECT 10/(2-i) AS n FROM (SELECT i FROM cursor_rows ORDER BY i OFFSET 0) AS ordered", 0))
				step(fixture.Exec("SAVEPOINT projection_error", 0))
				step(fixture.ErrorDiagnostic(motion+" FROM c", sqlCursorDiagnostic("22012", "division by zero")))
				step(fixture.Exec("ROLLBACK TO projection_error", 0))
				step(fixture.Exec("RELEASE projection_error", 0))
				sqlCursorError(t, p, b, c, "FETCH NEXT FROM c", sqlCursorDiagnostic("55000", "portal \"c\" cannot be run"))
			})
		})
	}
	t.Run("current_notice_receiver", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				var notices []fixture.Diagnostic
				_, conn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
				defer conn.ExecContext(p.Ctx, "ROLLBACK")
				warning := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "TIMESTAMP(7) precision reduced to maximum allowed, 6"}
				run := func(s fixture.Step, count int) {
					notices = nil
					fixture.RegtypeRunStep(t, p.Ctx, conn, recorder, s)
					var want []fixture.Diagnostic
					for range count {
						want = append(want, warning)
					}
					fixture.RegtypeAssertNotices(t, notices, want)
				}
				run(fixture.Exec("CREATE TABLE motion_warnings(t TEXT)", 0), 0)
				run(fixture.Exec("INSERT INTO motion_warnings VALUES('timestamp(7)'),('timestamp(7)'),('timestamp(7)')", 3), 0)
				run(fixture.Exec("BEGIN", 0), 0)
				run(fixture.Exec("SET LOCAL client_min_messages TO warning", 0), 0)
				run(fixture.Exec("DECLARE c SCROLL CURSOR FOR SELECT t::REGTYPE AS r FROM motion_warnings", 0), 0)
				run(fixture.Select("FETCH 0 FROM c", []string{"r"}, []string{"REGTYPE"}, [][]any{}), 0)
				run(fixture.Prepared(fixture.Exec("MOVE FORWARD 2 FROM c", 2)), 2)
				run(fixture.Prepared(fixture.Exec("MOVE 0 FROM c", 1)), 0)
				run(fixture.Select("FETCH 0 FROM c", []string{"r"}, []string{"REGTYPE"}, [][]any{{"timestamp without time zone"}}), 2)
				run(fixture.Exec("SET LOCAL client_min_messages TO error", 0), 0)
				run(fixture.Prepared(fixture.Exec("MOVE PRIOR FROM c", 1)), 0)
				run(fixture.Exec("SET LOCAL client_min_messages TO warning", 0), 0)
				run(fixture.Exec("MOVE RELATIVE 2 FROM c", 1), 2)
				run(fixture.Exec("MOVE NEXT FROM c", 0), 0)
				run(fixture.Select("FETCH 0 FROM c", []string{"r"}, []string{"REGTYPE"}, [][]any{}), 0)
				run(fixture.Exec("MOVE BACKWARD ALL FROM c", 3), 0)
				run(fixture.Exec("MOVE NEXT FROM c", 1), 1)
			})
		}
	})
}

func TestReferenceSQLCursorNoScrollMotionExceptions(t *testing.T) {
	forward := sqlCursorDiagnostic("55000", "cursor can only scan forward")
	forward.Hint = "Declare it with SCROLL option to enable backward scan."
	for _, probe := range []struct {
		name, initial, motion string
		count                 int64
		failure               bool
		empty                 bool
	}{
		{"pristine_absolute_zero", "", "MOVE ABSOLUTE 0", 0, false, false},
		{"pristine_backward_all", "", "MOVE BACKWARD ALL", 0, false, false},
		{"pristine_first", "", "MOVE FIRST", 1, false, false},
		{"backward_negative", "", "MOVE BACKWARD -2", 2, false, false},
		{"relative_forward", "MOVE NEXT FROM c", "MOVE RELATIVE 1", 1, false, false},
		{"absolute_forward", "MOVE NEXT FROM c", "MOVE ABSOLUTE 3", 1, false, false},
		{"zero_onrow", "MOVE NEXT FROM c", "MOVE 0", 1, false, false},
		{"backward_zero_onrow", "MOVE NEXT FROM c", "MOVE BACKWARD 0", 1, false, false},
		{"relative_zero_onrow", "MOVE NEXT FROM c", "MOVE RELATIVE 0", 1, false, false},
		{"backward_pristine", "", "FETCH BACKWARD 0", 0, true, false},
		{"fetch_zero_onrow", "MOVE NEXT FROM c", "FETCH 0", 0, true, false},
		{"rewind_onrow", "MOVE NEXT FROM c", "MOVE ABSOLUTE 0", 0, true, false},
		{"backward_all_onrow", "MOVE NEXT FROM c", "MOVE BACKWARD ALL", 0, true, false},
		{"rewind_after_end", "MOVE ALL FROM c", "MOVE ABSOLUTE 0", 0, true, false},
		{"rewind_empty_end", "MOVE ALL FROM c", "MOVE ABSOLUTE 0", 0, true, true},
		{"backward_empty_end", "MOVE ALL FROM c", "FETCH BACKWARD 0", 0, true, true},
	} {
		t.Run(probe.name, func(t *testing.T) {
			sqlCursorMotionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, step func(fixture.Step)) {
				sqlCursorSetup(t, p, b, c)
				step(fixture.Exec("BEGIN", 0))
				query := "DECLARE c NO SCROLL CURSOR FOR SELECT i,t FROM cursor_rows"
				if probe.empty {
					query += " WHERE FALSE"
				} else {
					query += " ORDER BY i"
				}
				step(fixture.Exec(query, 0))
				if probe.initial != "" {
					n := int64(1)
					if probe.initial == "MOVE ALL FROM c" {
						n = 3
					}
					if probe.empty {
						n = 0
					}
					step(fixture.Exec(probe.initial, n))
				}
				if probe.failure {
					step(fixture.Exec("SAVEPOINT motion_error", 0))
					step(fixture.ErrorDiagnostic(probe.motion+" FROM c", forward))
					step(fixture.Exec("ROLLBACK TO motion_error", 0))
					step(fixture.Exec("RELEASE motion_error", 0))
					sqlCursorError(t, p, b, c, "MOVE NEXT FROM c", sqlCursorDiagnostic("55000", "portal \"c\" cannot be run"))
				} else {
					step(fixture.Exec(probe.motion+" FROM c", probe.count))
					// MOVE zero is legal even though FETCH zero on this row is not.
					if probe.motion == "MOVE BACKWARD -2" {
						step(sqlCursorBatch("FETCH NEXT FROM c", sqlCursorMotionRows(3), b.PG))
					}
				}
				sqlCursorError(t, p, b, c, "MOVE BACKWARD FROM absent", sqlCursorMissing("absent"))
			})
		})
	}
}

func TestReferenceSQLCursorMotionSyntaxAndDiagnostics(t *testing.T) {
	for _, verb := range []string{"FETCH", "MOVE"} {
		for _, probe := range []struct{ tail, token string }{
			{"SIDEWAYS FROM c", "FROM"}, {"ABSOLUTE FROM c", "FROM"}, {"RELATIVE FROM c", "FROM"},
			{"ABSOLUTE ALL FROM c", "ALL"}, {"RELATIVE ALL FROM c", "ALL"},
			{"FORWARD 1.5 FROM c", "1.5"}, {"FORWARD 1e2 FROM c", "1e2"},
			{"FORWARD 1+1 FROM c", "+"}, {"FORWARD $1 FROM c", "$1"}, {"FORWARD '1' FROM c", "'1'"},
			{"2147483648 FROM c", "2147483648"}, {"-2147483648 FROM c", "2147483648"},
			{"1 FROM c trailing", "trailing"}, {"1 FROM \"é\" trailing", "trailing"},
		} {
			query := verb + " " + probe.tail
			t.Run(query, func(t *testing.T) {
				sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					sqlCursorSetup(t, p, b, c)
					sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
					sqlCursorStep(t, p, b, c, fixture.Exec("DECLARE c SCROLL CURSOR FOR SELECT i,t FROM cursor_rows ORDER BY i", 0))
					want := sqlCursorDiagnostic("42601", fmt.Sprintf("syntax error at or near %q", probe.token))
					want.Position = fmt.Sprint(utf8.RuneCountInString(query[:strings.Index(query, probe.token)]) + 1)
					for _, prepare := range []bool{false, true} {
						sqlCursorStep(t, p, b, c, fixture.Exec("SAVEPOINT syntax_error", 0))
						if prepare {
							stmt, err := c.PrepareContext(p.Ctx, query)
							if stmt != nil {
								stmt.Close()
								t.Fatal("invalid Prepare returned statement")
							}
							fixture.AssertDiagnostic(t, err, &want)
						} else {
							sqlCursorStep(t, p, b, c, fixture.ErrorDiagnostic(query, want))
						}
						sqlCursorStep(t, p, b, c, fixture.Exec("ROLLBACK TO syntax_error", 0))
						sqlCursorStep(t, p, b, c, fixture.Exec("RELEASE syntax_error", 0))
					}
					sqlCursorStep(t, p, b, c, sqlCursorBatch("FETCH NEXT FROM c", sqlCursorMotionRows(1), b.PG))
				})
			})
		}
	}
	for _, strength := range []string{"UPDATE", "NO KEY UPDATE", "SHARE", "KEY SHARE"} {
		for _, nested := range []bool{false, true} {
			t.Run(fmt.Sprintf("locking/%s/nested=%t", strength, nested), func(t *testing.T) {
				sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					sqlCursorSetup(t, p, b, c)
					sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
					query := "SELECT i FROM cursor_rows FOR " + strength
					if nested {
						query = "SELECT i FROM (SELECT i FROM cursor_rows WHERE i=1 FOR " + strength + ") AS locked"
					}
					query = "DECLARE locked SCROLL CURSOR FOR " + query
					if nested {
						// Pinned PG18 admits subquery row marks. They are not
						// top-level DECLARE SCROLL locking marks.
						for _, prepare := range []bool{false, true} {
							declare := fixture.Exec(query, 0)
							if prepare {
								declare = fixture.Prepared(declare)
							}
							sqlCursorStep(t, p, b, c, declare)
							fetch := fixture.Select("FETCH ALL FROM locked", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}})
							backward := fixture.Select("FETCH PRIOR FROM locked", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}})
							if prepare {
								fetch = fixture.Prepared(fetch)
								backward = fixture.Prepared(backward)
							}
							sqlCursorStep(t, p, b, c, fetch)
							sqlCursorStep(t, p, b, c, backward)
							peer, err := b.DB.Conn(p.Ctx)
							if err != nil {
								t.Fatal(err)
							}
							defer peer.Close()
							defer peer.ExecContext(p.Ctx, "ROLLBACK")
							sqlCursorStep(t, p, b, peer, fixture.Exec("BEGIN", 0))
							sqlCursorStep(t, p, b, peer, fixture.ErrorDiagnostic("SELECT i FROM cursor_rows WHERE i=1 FOR UPDATE NOWAIT", sqlCursorDiagnostic("55P03", "could not obtain lock on row in relation \"cursor_rows\"")))
							sqlCursorStep(t, p, b, peer, fixture.Exec("ROLLBACK", 0))
							if err := peer.Close(); err != nil {
								t.Fatal(err)
							}
							sqlCursorStep(t, p, b, c, fixture.Exec("CLOSE locked", 0))
						}
						return
					}
					want := sqlCursorDiagnostic("0A000", "DECLARE SCROLL CURSOR ... FOR "+strength+" is not supported")
					want.Detail = "Scrollable cursors must be READ ONLY."
					for _, prepare := range []bool{false, true} {
						sqlCursorStep(t, p, b, c, fixture.Exec("SAVEPOINT locking_error", 0))
						if prepare {
							stmt, err := c.PrepareContext(p.Ctx, query)
							if stmt != nil {
								stmt.Close()
								t.Fatal("locking SCROLL prepared successfully")
							}
							fixture.AssertDiagnostic(t, err, &want)
						} else {
							sqlCursorStep(t, p, b, c, fixture.ErrorDiagnostic(query, want))
						}
						sqlCursorStep(t, p, b, c, fixture.Exec("ROLLBACK TO locking_error", 0))
						sqlCursorStep(t, p, b, c, fixture.Exec("RELEASE locking_error", 0))
						sqlCursorError(t, p, b, c, "MOVE NEXT FROM locked", sqlCursorMissing("locked"))
					}
				})
			})
		}
	}
}
