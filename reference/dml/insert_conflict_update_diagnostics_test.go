package reference_test

import (
	"fmt"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func conflictUpdateDiagnostic(q, code, message, token, hint string) fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic(code, message)
	d.Hint = hint
	if token != "" {
		d.Position = strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, token)]) + 1)
	}
	return d
}
func TestReferenceInsertConflictUpdateBindingDiagnostics(t *testing.T) {
	for _, c := range []struct{ name, q, code, message, token, hint string }{
		{"ambiguous", "INSERT INTO t VALUES(1,2) ON CONFLICT(k) DO UPDATE SET v=v", "42702", "column reference \"v\" is ambiguous", "v=v", ""},
		{"utf8", "/*é*/ INSERT INTO t VALUES(1,2) ON CONFLICT(k) DO UPDATE SET v=missing.v", "42P01", "missing FROM-clause entry for table \"missing\"", "missing.v", ""},
		{"excluded_attribute", "INSERT INTO t VALUES(1,2) ON CONFLICT(k) DO UPDATE SET v=excluded.absent", "42703", "column excluded.absent does not exist", "excluded.absent", ""},
		{"where_integer", "INSERT INTO t VALUES(1,2) ON CONFLICT(k) DO UPDATE SET v=2 WHERE 1", "42804", "argument of WHERE must be type boolean, not type integer", "WHERE 1", ""},
		{"duplicate_assignment", "INSERT INTO t VALUES(1,2) ON CONFLICT(k) DO UPDATE SET v=2,v=3", "42601", "multiple assignments to same column \"v\"", "", ""},
		{"set_before_where", "INSERT INTO t VALUES(1,2) ON CONFLICT(k) DO UPDATE SET absent=2 WHERE missing.v>0", "42703", "column \"absent\" of relation \"t\" does not exist", "absent=2", ""},
		{"assignment_type", "INSERT INTO t VALUES(1,2) ON CONFLICT(k) DO UPDATE SET v=TRUE", "42804", "column \"v\" is of type integer but expression is of type boolean", "TRUE", "You will need to rewrite or cast the expression."},
		{"hidden_target", "INSERT INTO t AS dst VALUES(1,2) ON CONFLICT(k) DO UPDATE SET v=t.v", "42P01", "invalid reference to FROM-clause entry for table \"t\"", "t.v", "Perhaps you meant to reference the table alias \"dst\"."},
		{"source_scope", "INSERT INTO t SELECT 1,excluded.v ON CONFLICT(k) DO UPDATE SET v=excluded.v", "42P01", "missing FROM-clause entry for table \"excluded\"", "excluded.v", ""},
		{"arbiter_scope", "INSERT INTO t VALUES(1,2) ON CONFLICT((excluded.k)) DO UPDATE SET v=2", "42P01", "invalid reference to FROM-clause entry for table \"excluded\"", "excluded.k", ""},
		{"trailing_input", "INSERT INTO t VALUES(1,2) ON CONFLICT(k) DO UPDATE SET v=2 trailing", "42601", "syntax error at or near \"trailing\"", "trailing", ""},
		{"qualified_target", "INSERT INTO t VALUES(1,2) ON CONFLICT(k) DO UPDATE SET t.v=2", "42703", "column \"t\" of relation \"t\" does not exist", "t.v=2", "SET target columns cannot be qualified with the relation name."},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := conflictUpdateDiagnostic(c.q, c.code, c.message, c.token, c.hint)
			if c.name == "ambiguous" {
				d.Position = fixture.Position(strings.LastIndex(c.q, "v") + 1)
			}
			if c.name == "where_integer" {
				d.Position = fixture.Position(strings.LastIndex(c.q, "1") + 1)
			}
			if c.name == "arbiter_scope" {
				d.Detail = "There is an entry for table \"excluded\", but it cannot be referenced from this part of the query."
			}
			for _, prepared := range []bool{false, true} {
				insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE t(k INT UNIQUE,v INT)", 0))
					s := fixture.ErrorDiagnostic(c.q, d)
					s.Prepared = prepared
					s.ErrorStage = "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, conflictUpdateRows())
				})
			}
		})
	}
	q := "INSERT INTO excluded VALUES(1,2) ON CONFLICT(k) DO UPDATE SET v=excluded.v"
	d := conflictUpdateDiagnostic(q, "42P09", "table reference \"excluded\" is ambiguous", "excluded.v", "")
	for _, prepared := range []bool{false, true} {
		insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE excluded(k INT UNIQUE,v INT)", 0))
			s := fixture.ErrorDiagnostic(q, d)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		})
	}
}
func TestReferenceInsertConflictUpdateArbiterDiagnostics(t *testing.T) {
	for _, c := range []struct{ name, q, code, message, token, hint, stage string }{
		{"missing", "INSERT INTO t VALUES(1,2) ON CONFLICT DO UPDATE SET v=2", "42601", "ON CONFLICT DO UPDATE requires inference specification or constraint name", "ON CONFLICT", "For example, ON CONFLICT (column_name).", "prepare"},
		{"unicode_missing", "/*é*/ INSERT INTO t VALUES(1,2) ON CONFLICT DO UPDATE SET v=2", "42601", "ON CONFLICT DO UPDATE requires inference specification or constraint name", "ON CONFLICT", "For example, ON CONFLICT (column_name).", "prepare"},
		{"unmatched", "INSERT INTO t SELECT 1,2 WHERE FALSE ON CONFLICT(v) DO UPDATE SET v=2", "42P10", "there is no unique or exclusion constraint matching the ON CONFLICT specification", "", "", "execute"},
		{"missing_constraint", "INSERT INTO t VALUES(1,2) ON CONFLICT ON CONSTRAINT absent DO UPDATE SET v=2", "42704", "constraint \"absent\" for table \"t\" does not exist", "", "", "prepare"},
		{"check", "INSERT INTO t SELECT 1,2 WHERE FALSE ON CONFLICT ON CONSTRAINT positive DO UPDATE SET v=2", "42809", "constraint in ON CONFLICT clause has no associated index", "", "", "execute"},
		{"not_null", "INSERT INTO t SELECT 1,2 WHERE FALSE ON CONFLICT ON CONSTRAINT required DO UPDATE SET v=2", "42809", "constraint in ON CONFLICT clause has no associated index", "", "", "execute"},
	} {
		t.Run(c.name, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE t(k INT UNIQUE,v INT CONSTRAINT required NOT NULL,CONSTRAINT positive CHECK(v>0))", 0))
					s := fixture.ErrorDiagnostic(c.q, conflictUpdateDiagnostic(c.q, c.code, c.message, c.token, c.hint))
					s.Prepared = prepared
					s.ErrorStage = "execute"
					if prepared {
						s.ErrorStage = c.stage
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				})
			}
		})
	}
	for _, method := range []string{"btree", "hash"} {
		for _, deferred := range []string{"", "DEFERRABLE INITIALLY IMMEDIATE", "DEFERRABLE INITIALLY DEFERRED"} {
			t.Run(method+deferred, func(t *testing.T) {
				for _, source := range []string{"VALUES(1,2)", "SELECT 1,2 WHERE FALSE"} {
					conflictUpdateSteps(t, fixture.Exec("CREATE TABLE t(k INT,v INT,CONSTRAINT ex EXCLUDE USING "+method+"(k WITH =) "+deferred+")", 0),
						fixture.ErrorDiagnostic("INSERT INTO t "+source+" ON CONFLICT ON CONSTRAINT ex DO UPDATE SET v=2", fixture.ControlsReferenceDiagnostic("42809", "ON CONFLICT DO UPDATE not supported with exclusion constraints")),
						insertConflictNoMatch("INSERT INTO t "+source+" ON CONFLICT(k) DO UPDATE SET v=2"))
				}
			})
		}
	}
}
func TestReferenceInsertConflictUpdateDeferrableWitnesses(t *testing.T) {
	for _, kind := range []string{"UNIQUE", "PRIMARY KEY"} {
		for _, mode := range []string{"DEFERRABLE INITIALLY IMMEDIATE", "DEFERRABLE INITIALLY DEFERRED"} {
			t.Run(kind+mode, func(t *testing.T) {
				insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					ddl := "CREATE TABLE t(k INT CONSTRAINT u " + kind + " " + mode + ",v INT)"
					run(fixture.Exec(ddl, 0))
					d := fixture.ControlsReferenceDiagnostic("55000", "ON CONFLICT does not support deferrable unique constraints/exclusion constraints as arbiters")
					d.Schema = "public"
					d.Table = "t"
					d.Constraint = "u"
					for _, target := range []string{"(k)", "ON CONSTRAINT u"} {
						for _, prepared := range []bool{false, true} {
							for _, condition := range []string{"TRUE", "FALSE"} {
								q := "INSERT INTO t VALUES(1,2) ON CONFLICT " + target + " DO UPDATE SET v=2 WHERE " + condition
								s := fixture.ErrorDiagnostic(q, d)
								s.Prepared = prepared
								s.ErrorStage = "execute"
								run(s)
							}
							s := fixture.Exec("INSERT INTO t SELECT 1,2 WHERE FALSE ON CONFLICT "+target+" DO UPDATE SET v=2", 0)
							s.Prepared = prepared
							run(s)
						}
					}
					run(conflictUpdateRows())
				})
			})
		}
	}
}
func TestReferenceInsertConflictUpdateBoundaries(t *testing.T) {
	// Genuine native capabilities are permanent distinctions, not discovery skips.
	insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		parentDDL := "CREATE TABLE parent(k INT,v INT) PARTITION BY RANGE(k)"
		if b.PG {
			parentDDL = "CREATE TABLE parent(k INT PRIMARY KEY,v INT) PARTITION BY RANGE(k)"
		}
		run(fixture.Exec(parentDDL, 0))
		run(fixture.Exec("CREATE TABLE leaf PARTITION OF parent FOR VALUES FROM(0) TO(10)", 0))
		for _, table := range []string{"parent", "leaf"} {
			q := "INSERT INTO " + table + " VALUES(1,2) ON CONFLICT(k) DO UPDATE SET v=excluded.v"
			if !b.PG {
				run(fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("0A000", "ON CONFLICT on partition hierarchies is unsupported")))
			} else {
				run(fixture.Exec(q, 1))
			}
		}
		n := int64(0)
		if b.PG {
			n = 1
		}
		run(fixture.Select("SELECT count(*) AS n FROM parent", []string{"n"}, []string{"INT8"}, [][]any{{n}}))
	})
	t.Run("hierarchy_before_defaults", func(t *testing.T) {
		insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE SEQUENCE seq", 0))
			parentDDL := "CREATE TABLE parent(k INT,v INT DEFAULT nextval('seq')) PARTITION BY RANGE(k)"
			if b.PG {
				parentDDL = "CREATE TABLE parent(k INT PRIMARY KEY,v INT DEFAULT nextval('seq')) PARTITION BY RANGE(k)"
			}
			run(fixture.Exec(parentDDL, 0))
			run(fixture.Exec("CREATE TABLE leaf PARTITION OF parent FOR VALUES FROM(0) TO(10)", 0))
			for _, table := range []string{"parent", "leaf"} {
				q := "INSERT INTO " + table + "(k) VALUES(1) ON CONFLICT(k) DO UPDATE SET v=excluded.v"
				if b.PG {
					run(fixture.Exec(q, 1))
				} else {
					run(fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("0A000", "ON CONFLICT on partition hierarchies is unsupported")))
				}
			}
			n := int64(1)
			if b.PG {
				n = 3
			}
			run(fixture.Select("SELECT nextval('seq') AS n", []string{"n"}, []string{"INT8"}, [][]any{{n}}))
			rows := [][]any{}
			if b.PG {
				rows = [][]any{{int64(1), int64(2)}}
			}
			run(fixture.Select("SELECT k,v FROM parent", []string{"k", "v"}, []string{"INT4", "INT4"}, rows))
		})
	})
	for _, suffix := range []string{"FROM t", "RETURNING k"} {
		t.Run(fmt.Sprint(suffix), func(t *testing.T) {
			insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE t(k INT UNIQUE,v INT)", 0))
				q := "INSERT INTO t VALUES(1,2) ON CONFLICT(k) DO UPDATE SET v=2 " + suffix
				if suffix == "FROM t" {
					d := conflictUpdateDiagnostic(q, "42601", "syntax error at or near \"FROM\"", "FROM t", "")
					s := fixture.ErrorDiagnostic(q, d)
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
				if suffix == "RETURNING k" {
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select(q, []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
				}
			})
		})
	}
}
