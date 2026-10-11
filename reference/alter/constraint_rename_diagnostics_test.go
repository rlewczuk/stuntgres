package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceConstraintRenameGrammarAndNames(t *testing.T) {
	constraintRenameScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		var notices []fixture.Diagnostic
		_, noticeConn, record := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		for _, prepared := range []bool{false, true} {
			notices = nil
			s := fixture.Exec("ALTER TABLE IF EXISTS missing RENAME CONSTRAINT old TO new", 0)
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, noticeConn, record, s)
			want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `relation "missing" does not exist, skipping`}
			if len(notices) != 1 || notices[0] != want {
				t.Fatal("IF EXISTS notices", notices, want)
			}
		}
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE t(k INT CONSTRAINT old UNIQUE)", 0))
		for _, target := range []string{"ONLY t", "ONLY (t)", "public.t", "t *", "IF EXISTS t"} {
			run(fixture.Exec("ALTER TABLE "+target+" RENAME CONSTRAINT old TO new", 0))
			run(fixture.Exec("ALTER TABLE t RENAME CONSTRAINT new TO old", 0))
		}
		for _, suffix := range []string{", DROP CONSTRAINT old", " nonsense"} {
			q := "ALTER TABLE t RENAME CONSTRAINT old TO new" + suffix
			token := "nonsense"
			if suffix[0] == ',' {
				token = ","
			}
			d := constraintRenameError("42601", `syntax error at or near "`+token+`"`)
			d.Position = fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, token)]) + 1)
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(q, d)
				s.Prepared = prepared
				if prepared {
					s.ErrorStage = "prepare"
				}
				run(s)
			}
			run(fixture.Select("SELECT conname FROM pg_constraint WHERE conname='old'", []string{"conname"}, []string{"NAME"}, [][]any{{"old"}}))
		}
		run(fixture.Exec(`ALTER TABLE t RENAME CONSTRAINT old TO "MiX"`, 0))
		run(fixture.Exec(`ALTER TABLE t RENAME CONSTRAINT "MiX" TO "q""u\z"`, 0))
		run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
		run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(1)", fixture.UniqueReferenceDuplicate("t", `q"u\z`, "Key (k)=(1) already exists.")))
		run(fixture.ErrorDiagnostic(`ALTER TABLE t RENAME CONSTRAINT "abs""ent\z" TO new`, constraintRenameMissing(`abs"ent\z`)))
		long := strings.Repeat("é", 40)
		clipped := strings.Repeat("é", 31)
		run(fixture.Exec(`ALTER TABLE t RENAME CONSTRAINT "q""u\z" TO "`+long+`"`, 0))
		run(fixture.Select("SELECT conname FROM pg_constraint WHERE contype='u' AND conrelid='t'::regclass::oid", []string{"conname"}, []string{"NAME"}, [][]any{{clipped}}))
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname=$1", []string{"relname"}, []string{"NAME"}, [][]any{{clipped}}, clipped))
		run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(1)", fixture.UniqueReferenceDuplicate("t", clipped, "Key (k)=(1) already exists.")))
	})
}

func TestReferenceConstraintRenameCollisionsAndDiagnostics(t *testing.T) {
	for _, k := range constraintRenameKinds {
		t.Run(k.kind, func(t *testing.T) {
			constraintRenameScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE t("+k.declaration+",CONSTRAINT occupied CHECK(k<10))", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,'keep')", 1))
				run(fixture.Exec("CREATE TABLE taken(v INT)", 0))
				before := constraintRenameCapture(t, p, c, "old", k.indexed)
				cases := []struct{ dest, code, message string }{
					{"occupied", "42710", `constraint "occupied" for relation "t" already exists`},
					{"old", "42710", `constraint "old" for relation "t" already exists`},
				}
				if k.indexed {
					cases[1].code = "42P07"
					cases[1].message = `relation "old" already exists`
					cases = append(cases, struct{ dest, code, message string }{"taken", "42P07", `relation "taken" already exists`})
				}
				for _, tc := range cases {
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic("ALTER TABLE t RENAME CONSTRAINT old TO "+tc.dest, constraintRenameError(tc.code, tc.message))
						s.Prepared = prepared
						s.ErrorStage = "execute"
						run(s)
						if constraintRenameCapture(t, p, c, "old", k.indexed) != before {
							t.Fatal("failed rename changed catalog")
						}
						run(fixture.Select("SELECT k,payload FROM t", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "keep"}}))
					}
				}
				run(fixture.Exec("CREATE TABLE occupied(v INT)", 0))
				if k.indexed {
					run(fixture.ErrorDiagnostic("ALTER TABLE t RENAME CONSTRAINT old TO occupied", constraintRenameError("42P07", `relation "occupied" already exists`)))
				}
				for _, q := range []string{"ALTER TABLE t RENAME CONSTRAINT absent TO new", "ALTER TABLE IF EXISTS t RENAME CONSTRAINT absent TO new"} {
					s := fixture.Prepared(fixture.ErrorDiagnostic(q, constraintRenameMissing("absent")))
					s.ErrorStage = "execute"
					run(s)
				}
				run(fixture.ErrorDiagnostic("ALTER TABLE missing RENAME CONSTRAINT old TO taken", constraintRenameError("42P01", `relation "missing" does not exist`)))
				dest := "new"
				if !k.indexed {
					dest = "taken"
				}
				run(fixture.Exec("ALTER TABLE t RENAME CONSTRAINT old TO "+dest, 0))
				if constraintRenameCapture(t, p, c, dest, k.indexed) != before {
					t.Fatal("retry changed identity")
				}
			})
		})
	}
}
