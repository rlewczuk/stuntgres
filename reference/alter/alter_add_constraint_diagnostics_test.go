package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterAddConstraintDiagnostics(t *testing.T) {
	addConstraintAuthority(t)
	addConstraintHierarchy(t)
	for _, tc := range []struct{ clause, message, position string }{
		{"CONSTRAINT uq UNIQUE(a) NOT VALID", "UNIQUE constraints cannot be marked NOT VALID", "45"},
		{"CONSTRAINT pk PRIMARY KEY(a) NOT VALID", "PRIMARY KEY constraints cannot be marked NOT VALID", "50"},
		{"CONSTRAINT ex EXCLUDE(a WITH =) NOT VALID", "EXCLUDE constraints cannot be marked NOT VALID", "53"},
	} {
		for _, name := range []string{"t", "é"} {
			for _, prepared := range []bool{false, true} {
				t.Run(tc.clause+name+fmt.Sprint(prepared), func(t *testing.T) {
					addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
						run(fixture.Exec(`CREATE TABLE "`+name+`"(a INT)`, 0))
						run(fixture.Exec(`INSERT INTO "`+name+`" VALUES(1)`, 1))
						q := `ALTER TABLE "` + name + `" ADD ` + tc.clause
						want := fixture.ControlsReferenceDiagnostic("0A000", tc.message)
						want.Position = tc.position
						phase := "execute"
						if prepared {
							phase = "prepare"
						}
						addConstraintError(t, p, c, q, want, prepared, phase)
						run(fixture.Select(`SELECT a FROM "`+name+`"`, []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
						run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid=$1::regclass::oid", []string{"conname"}, []string{"NAME"}, nil, name))
					})
				})
			}
		}
	}
	for _, old := range []any{int64(1), nil} {
		t.Run(fmt.Sprintf("repeated_invalid_nn_%v", old), func(t *testing.T) {
			addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := addConstraintSetup(t, p, b, c)
				run(fixture.Exec("INSERT INTO t VALUES($1,'old')", 1, old))
				run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT nn NOT NULL a NOT VALID", 0))
				oid := addConstraintInvalid(t, p, b, c, "t", "nn", "NOT NULL a NOT VALID")
				for _, prepared := range []bool{false, true} {
					d := fixture.ControlsReferenceDiagnostic("55000", `incompatible NOT VALID constraint "nn" on relation "t"`)
					d.Hint = "You might need to validate it using ALTER TABLE ... VALIDATE CONSTRAINT."
					addConstraintError(t, p, c, "ALTER TABLE t ADD CONSTRAINT nn_new NOT NULL a", d, prepared, "execute")
					if got := addConstraintInvalid(t, p, b, c, "t", "nn", "NOT NULL a NOT VALID"); got != oid {
						t.Fatal(oid, got)
					}
					run(fixture.Prepared(fixture.Exec("ALTER TABLE t ADD CONSTRAINT nn_other NOT NULL a NOT VALID", 0)))
					run(fixture.Prepared(fixture.Exec("ALTER TABLE t ADD CONSTRAINT nn NOT NULL a NOT VALID", 0)))
					if got := addConstraintInvalid(t, p, b, c, "t", "nn", "NOT NULL a NOT VALID"); got != oid {
						t.Fatal("compatible invalid ADD changed identity", oid, got)
					}
				}
				run(fixture.Exec("UPDATE t SET a=1", 1))
				run(fixture.Exec("CREATE UNIQUE INDEX key_index ON t(a)", 0))
				for i, q := range []string{"ALTER TABLE t ADD CONSTRAINT pk PRIMARY KEY(a)", "ALTER TABLE t ADD CONSTRAINT key_index PRIMARY KEY USING INDEX key_index"} {
					for _, prepared := range []bool{false, true} {
						d := fixture.ControlsReferenceDiagnostic("55000", `cannot create primary key on column "a"`)
						d.Detail = `The constraint "nn" on column "a" of table "t", marked NOT VALID, is incompatible with a primary key.`
						if i == 1 {
							// Index adoption takes the ordinary NOT NULL compatibility path.
							d = fixture.ControlsReferenceDiagnostic("55000", `incompatible NOT VALID constraint "nn" on relation "t"`)
						}
						d.Hint = "You might need to validate it using ALTER TABLE ... VALIDATE CONSTRAINT."
						addConstraintError(t, p, c, q, d, prepared, "execute")
						if got := addConstraintInvalid(t, p, b, c, "t", "nn", "NOT NULL a NOT VALID"); got != oid {
							t.Fatal(oid, got)
						}
					}
				}
				run(fixture.Exec("ALTER TABLE t ALTER a SET NOT NULL", 0))
				run(fixture.Select("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname='nn' AND conrelid='t'::regclass::oid", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{"NOT NULL a"}}))
				run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT key_index PRIMARY KEY USING INDEX key_index", 0))
			})
		})
	}
	t.Run("compatible_nn_name_precedence", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(a INT CONSTRAINT original NOT NULL,b INT,CONSTRAINT occupied CHECK(a>0))", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1,2)", 1))
			before := addConstraintCatalog(t, p, c)
			for _, q := range []string{"ALTER TABLE t ADD CONSTRAINT fresh NOT NULL a", "ALTER TABLE t ADD CONSTRAINT occupied NOT NULL a NOT VALID", "ALTER TABLE t ADD CONSTRAINT fresh NOT NULL a NOT VALID"} {
				run(fixture.Prepared(fixture.Exec(q, 0)))
				if got := addConstraintCatalog(t, p, c); fmt.Sprint(got) != fmt.Sprint(before) {
					t.Fatal("compatible NN changed name/OID/validity", before, got)
				}
			}
			d := fixture.ControlsReferenceDiagnostic("42710", `constraint "occupied" for relation "t" already exists`)
			for _, prepared := range []bool{false, true} {
				addConstraintError(t, p, c, "ALTER TABLE t ADD CONSTRAINT occupied NOT NULL b NOT VALID", d, prepared, "execute")
			}
			if b.PG {
				run(fixture.Select("SELECT convalidated FROM pg_constraint WHERE conrelid='t'::regclass AND conname='original'", []string{"convalidated"}, []string{"BOOL"}, [][]any{{true}}))
			}
		})
	})
	t.Run("set_invalid_nn_validation", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := addConstraintSetup(t, p, b, c)
			run(fixture.Exec("INSERT INTO t VALUES(NULL,'old')", 1))
			run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT nn NOT NULL a NOT VALID", 0))
			oid := addConstraintInvalid(t, p, b, c, "t", "nn", "NOT NULL a NOT VALID")
			d := fixture.ControlsReferenceDiagnostic("23502", `column "a" of relation "t" contains null values`)
			d.Schema, d.Table, d.Column = "public", "t", "a"
			run(fixture.Prepared(fixture.ErrorDiagnostic("ALTER TABLE t ALTER a SET NOT NULL", d)))
			if got := addConstraintInvalid(t, p, b, c, "t", "nn", "NOT NULL a NOT VALID"); got != oid {
				t.Fatal(oid, got)
			}
			run(fixture.Exec("UPDATE t SET a=1", 1))
			run(fixture.Exec("ALTER TABLE t ALTER a SET NOT NULL", 0))
			var got int64
			if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_constraint WHERE conname='nn' AND conrelid='t'::regclass::oid").Scan(&got); err != nil || got != oid {
				t.Fatal(oid, got, err)
			}
		})
	})
	t.Run("full_input_and_quoted_records", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec(`CREATE TABLE "t""é"("a""b\\tail" INT)`, 0))
			run(fixture.Exec(`INSERT INTO "t""é" VALUES(0)`, 1))
			run(fixture.Exec("CREATE TABLE keeper(v INT)", 0))
			run(fixture.Exec("INSERT INTO keeper VALUES(91)", 1))
			d := fixture.ControlsReferenceDiagnostic("23514", `check constraint "c"k" of relation "t"é" is violated by some row`)
			d.Schema, d.Table, d.Constraint = "public", `t"é`, `c"k`
			for _, prepared := range []bool{false, true} {
				addConstraintError(t, p, c, `ALTER TABLE "t""é" ADD CONSTRAINT "c""k" CHECK("a""b\\tail">0)`, d, prepared, "execute")
				q := `ALTER TABLE "t""é" ADD CHECK("a""b\\tail">=0) garbage`
				syntax := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "garbage"`)
				syntax.Position = "47"
				phase := "execute"
				if prepared {
					phase = "prepare"
				}
				addConstraintError(t, p, c, q, syntax, prepared, phase)
				prefix := `ALTER TABLE "t""é" ADD CHECK("a""b\\tail">=0); INSERT INTO keeper VALUES(92); nonsense`
				syntax = fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "nonsense"`)
				syntax.Position = "79"
				addConstraintError(t, p, c, prefix, syntax, prepared, phase)
				run(fixture.Select("SELECT v FROM keeper", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
				run(fixture.Select(`SELECT conname FROM pg_constraint WHERE conrelid='"t""é"'::regclass::oid`, []string{"conname"}, []string{"NAME"}, nil))
			}
			run(fixture.Select(`SELECT "a""b\\tail" FROM "t""é"`, []string{`a"b\\tail`}, []string{"INT4"}, [][]any{{int64(0)}}))
		})
	})
	t.Run("all_kind_name_collisions", func(t *testing.T) {
		for _, existing := range addConstraintCases() {
			for _, added := range addConstraintCases() {
				t.Run(existing.name+"/"+added.name, func(t *testing.T) {
					addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						run := addConstraintSetup(t, p, b, c)
						run(fixture.Exec("INSERT INTO t VALUES(1,'old')", 1))
						// Rewrite only the named constraint token, not expressions/keys.
						clause := existing.clause
						for i, ch := range clause {
							if ch == ' ' && i > len("CONSTRAINT ") {
								clause = "CONSTRAINT occupied" + clause[i:]
								break
							}
						}
						run(fixture.Exec("ALTER TABLE t ADD "+clause, 0))
						clause = added.clause
						for i, ch := range clause {
							if ch == ' ' && i > len("CONSTRAINT ") {
								clause = "CONSTRAINT occupied" + clause[i:]
								break
							}
						}
						if added.kind == "n" {
							clause = "CONSTRAINT occupied NOT NULL note"
						}
						d := fixture.ControlsReferenceDiagnostic("42710", `constraint "occupied" for relation "t" already exists`)
						if existing.kind == "p" && added.kind == "p" {
							d = fixture.ControlsReferenceDiagnostic("42P16", `multiple primary keys for table "t" are not allowed`)
						} else if (existing.kind == "u" || existing.kind == "p" || existing.kind == "x") && (added.kind == "u" || added.kind == "p" || added.kind == "x") {
							d = fixture.ControlsReferenceDiagnostic("42P07", `relation "occupied" already exists`)
						}
						addConstraintError(t, p, c, "ALTER TABLE t ADD "+clause, d, false, "execute")
						run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='t'::regclass::oid AND conname='occupied'", []string{"conname"}, []string{"NAME"}, [][]any{{"occupied"}}))
					})
				})
			}
		}
	})
}
