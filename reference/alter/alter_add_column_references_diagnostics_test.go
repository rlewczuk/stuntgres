package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAlterAddColumnReferencesAtomicity(t *testing.T) {
	columnReferencesRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		incompatible := fixture.ControlsReferenceDiagnostic("42804", `foreign key constraint "bad_fk" cannot be implemented`)
		incompatible.Detail = `Key columns "pid" of the referencing table and "id" of the referenced table are of incompatible types: integer and text.`
		for _, tc := range []struct {
			name, parent, column, fix string
			want                      fixture.Diagnostic
		}{
			{"missing", "CREATE TABLE p(id INT PRIMARY KEY)", "pid INT CONSTRAINT bad_fk REFERENCES missing(id)", "CREATE TABLE missing(id INT PRIMARY KEY)", fixture.ControlsReferenceDiagnostic("42P01", `relation "missing" does not exist`)},
			{"nonunique", "CREATE TABLE p(id INT)", "pid INT CONSTRAINT bad_fk REFERENCES p(id)", "ALTER TABLE p ADD UNIQUE(id)", fixture.ControlsReferenceDiagnostic("42830", `there is no unique constraint matching given keys for referenced table "p"`)},
			{"no_primary", "CREATE TABLE p(id INT UNIQUE)", "pid INT CONSTRAINT bad_fk REFERENCES p", "ALTER TABLE p ADD PRIMARY KEY(id)", fixture.ControlsReferenceDiagnostic("42704", `there is no primary key for referenced table "p"`)},
			{"incompatible", "CREATE TABLE p(id TEXT PRIMARY KEY)", "pid INT CONSTRAINT bad_fk REFERENCES p(id)", "CREATE TABLE p(id INT PRIMARY KEY)", incompatible},
			{"default", "CREATE TABLE p(id INT PRIMARY KEY)", "pid INT DEFAULT 99 CONSTRAINT bad_fk REFERENCES p(id)", "INSERT INTO p VALUES(99)", addConstraintFK("t", "bad_fk", "pid", "99", "p")},
		} {
			for _, notEnforced := range []bool{false, true} {
				if notEnforced && tc.name == "default" {
					continue // NOT ENFORCED intentionally admits invalid history.
				}
				for _, form := range []string{"single", "extra_first", "extra_last", "savepoint"} {
					t.Run(fmt.Sprintf("%s/not_enforced_%t/%s", tc.name, notEnforced, form), func(t *testing.T) {
						columnReferencesClean(t, p, c, "t", "missing", "p")
						run := columnReferencesRun(t, p, b, c, prepared)
						run(fixture.Exec(tc.parent, 0))
						run(fixture.Exec("CREATE TABLE t(id INT,note TEXT)", 0))
						run(fixture.Exec("INSERT INTO t VALUES(1,'keep')", 1))
						if form == "savepoint" {
							run(fixture.Exec("BEGIN", 0))
							t.Cleanup(func() { _, _ = c.ExecContext(p.Ctx, "ROLLBACK") })
							run(fixture.Exec("INSERT INTO t VALUES(2,'private')", 1))
							run(fixture.Exec("SAVEPOINT before_add", 0))
						}
						before, attrs := enforcementSnapshot(t, p, b, c), columnReferencesAttributes(t, p, c)
						column := tc.column
						if notEnforced {
							column += " NOT ENFORCED"
						}
						q := "ALTER TABLE t ADD " + column
						if form == "extra_first" || form == "savepoint" {
							q = "ALTER TABLE t ADD extra INT DEFAULT 7 NOT NULL CONSTRAINT extra_check CHECK(extra>0), ADD " + column
						} else if form == "extra_last" {
							q += ", ADD extra INT DEFAULT 7 NOT NULL CONSTRAINT extra_check CHECK(extra>0)"
						}
						addConstraintError(t, p, c, q, tc.want, prepared, "execute")
						if form == "savepoint" {
							run(fixture.Exec("ROLLBACK TO before_add", 0))
						}
						columnReferencesUnchanged(t, p, b, c, before, attrs)
						rows := [][]any{{int64(1), "keep"}}
						if form == "savepoint" {
							rows = append(rows, []any{int64(2), "private"})
						}
						run(fixture.Select("SELECT id,note FROM t ORDER BY id", []string{"id", "note"}, []string{"INT4", "TEXT"}, rows))
						fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, prepared, "SELECT * FROM t ORDER BY id", []string{"id", "note"}, []string{"INT4", "TEXT"}, []bool{true, true}, len(rows))
						// Repair only the independent cause, then retry the exact SQL:
						// failed column/constraint/index names must remain reusable.
						if tc.name == "incompatible" {
							run(fixture.Exec("DROP TABLE p", 0))
							run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
						} else {
							affected := int64(0)
							if tc.name == "default" {
								affected = 1
							}
							run(fixture.Exec(tc.fix, affected))
						}
						run(fixture.Exec(q, 0))
						def := "FOREIGN KEY (pid) REFERENCES p(id)"
						if tc.name == "missing" {
							def = "FOREIGN KEY (pid) REFERENCES missing(id)"
						}
						if notEnforced {
							def += " NOT ENFORCED"
						}
						run(columnReferencesDefinitions("t", []any{"bad_fk", "f", def}))
						if form != "single" {
							run(fixture.Select("SELECT extra FROM t ORDER BY id", []string{"extra"}, []string{"INT4"}, func() [][]any {
								values := [][]any{{int64(7)}}
								if form == "savepoint" {
									values = append(values, []any{int64(7)})
								}
								return values
							}()))
						}
						if form == "savepoint" {
							run(fixture.Exec("COMMIT", 0))
						}
					})
				}
			}
		}
	})
}

func TestReferenceAlterAddColumnReferencesBoundaries(t *testing.T) {
	columnReferencesRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		columnReferencesClean(t, p, c, "prefix", "t", "p")
		run := columnReferencesRun(t, p, b, c, prepared)
		run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
		run(fixture.Exec("CREATE TABLE t(id INT)", 0))
		q := "ALTER TABLE t ADD COLUMN key INT PRIMARY KEY"
		if b.PG {
			run(fixture.Exec(q, 0))
			run(fixture.Exec("INSERT INTO t VALUES(1,1)", 1))
			run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(2,1)", fixture.UniqueReferenceDuplicate("t", "t_pkey", "Key (key)=(1) already exists.")))
			run(fixture.Exec("ALTER TABLE t DROP key", 0))
		} else {
			d := fixture.ControlsReferenceDiagnostic("0A000", "ALTER TABLE action is unsupported")
			d.Position = enforcementPosition(q, "ADD", false)
			phase := "execute"
			if prepared {
				phase = "prepare"
			}
			addConstraintError(t, p, c, q, d, prepared, phase)
		}
		for _, tc := range []struct{ clause, code, message, token string }{
			{"REFERENCES p MATCH PARTIAL", "0A000", "MATCH PARTIAL not yet implemented", "MATCH"},
			{"REFERENCES p ON UPDATE SET NULL (pid)", "0A000", "a column list with SET NULL is only supported for ON DELETE actions", "ON UPDATE"},
			{"REFERENCES p ON UPDATE SET DEFAULT (pid)", "0A000", "a column list with SET DEFAULT is only supported for ON DELETE actions", "ON UPDATE"},
			{"REFERENCES p ON DELETE SET NULL (missing)", "42703", `column "missing" referenced in foreign key constraint does not exist`, ""},
			{"REFERENCES p ON DELETE SET DEFAULT (id)", "42P10", `column "id" referenced in ON DELETE SET action must be part of foreign key`, ""},
			{"REFERENCES p(id", "42601", "syntax error at end of input", "end"},
			{"REFERENCES", "42601", "syntax error at end of input", "end"},
			{"REFERENCES p(id) trailing", "42601", `syntax error at or near "trailing"`, "trailing"},
		} {
			t.Run(tc.clause, func(t *testing.T) {
				q := "ALTER TABLE t ADD pid INT " + tc.clause
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				phase := "execute"
				if tc.token != "" {
					if tc.token == "end" {
						d.Position = fixture.Position(len([]rune(q)) + 1)
					} else {
						d.Position = enforcementPosition(q, tc.token, true)
					}
					if prepared {
						phase = "prepare"
					}
				}
				before, attrs := enforcementSnapshot(t, p, b, c), columnReferencesAttributes(t, p, c)
				addConstraintError(t, p, c, q, d, prepared, phase)
				columnReferencesUnchanged(t, p, b, c, before, attrs)
			})
		}
		q = "CREATE TABLE prefix(id INT); ALTER TABLE t ADD pid INT REFERENCES p("
		d := fixture.ControlsReferenceDiagnostic("42601", "syntax error at end of input")
		d.Position = fixture.Position(len([]rune(q)) + 1)
		phase := "execute"
		if prepared {
			phase = "prepare"
		}
		addConstraintError(t, p, c, q, d, prepared, phase)
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='prefix'", []string{"relname"}, []string{"NAME"}, nil))
		run(fixture.Exec("ALTER TABLE t ADD pid INT", 0))
		q = "ALTER TABLE t ADD IF NOT EXISTS pid INT REFERENCES p("
		d.Position = fixture.Position(len([]rune(q)) + 1)
		addConstraintError(t, p, c, q, d, prepared, phase)
		run(columnReferencesDefinitions("t"))
	})
}
