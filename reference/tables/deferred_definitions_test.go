package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDeferredConstraintDefinitions(t *testing.T) {
	for _, tc := range []struct {
		definition        string
		deferred, initial bool
	}{
		{"v INT CONSTRAINT u UNIQUE", false, false},
		{"v INT CONSTRAINT u UNIQUE NOT DEFERRABLE INITIALLY IMMEDIATE", false, false},
		{"v INT CONSTRAINT u UNIQUE DEFERRABLE", true, false},
		{"v INT CONSTRAINT u PRIMARY KEY INITIALLY DEFERRED", true, true},
		{"v INT,CONSTRAINT u UNIQUE(v) INITIALLY IMMEDIATE DEFERRABLE", true, false},
		{"v INT,CONSTRAINT u UNIQUE(v) INITIALLY DEFERRED DEFERRABLE", true, true},
		{"v INT,CONSTRAINT u PRIMARY KEY(v) DEFERRABLE INITIALLY DEFERRED", true, true},
		{"v INT CONSTRAINT u REFERENCES p INITIALLY DEFERRED", true, true},
		{"v INT,CONSTRAINT u FOREIGN KEY(v) REFERENCES p DEFERRABLE INITIALLY IMMEDIATE", true, false},
		{"v INT,CONSTRAINT u EXCLUDE(v WITH =) INITIALLY DEFERRED", true, true},
		{"v INT,CONSTRAINT u UNIQUE(v) DEFERRABLE DEFERRABLE", true, false},
	} {
		t.Run(tc.definition, func(t *testing.T) {
			fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE p(v INT PRIMARY KEY)", 0))
				run(fixture.Prepared(fixture.Exec("CREATE TABLE t("+tc.definition+")", 0)))
				run(fixture.DeferredConstraintFlags("SELECT conname,condeferrable,condeferred FROM pg_constraint WHERE conrelid='t'::regclass AND conname='u'", []any{"u", tc.deferred, tc.initial}))
			})
		})
	}
	for _, clause := range []string{"UNIQUE(v)", "PRIMARY KEY(v)", "FOREIGN KEY(v) REFERENCES p"} {
		t.Run("add/"+clause, func(t *testing.T) {
			fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE p(v INT PRIMARY KEY)", 0))
				run(fixture.Exec("CREATE TABLE t(v INT)", 0))
				run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT u "+clause+" INITIALLY DEFERRED", 0))
				run(fixture.DeferredConstraintFlags("SELECT conname,condeferrable,condeferred FROM pg_constraint WHERE conname='u'", []any{"u", true, true}))
			})
		})
	}
	for _, kind := range []string{"UNIQUE", "PRIMARY KEY"} {
		t.Run("using/"+kind, func(t *testing.T) {
			fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE t(v INT NOT NULL)", 0))
				run(fixture.Exec("CREATE UNIQUE INDEX u ON t(v)", 0))
				run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT u "+kind+" USING INDEX u INITIALLY DEFERRED", 0))
				run(fixture.DeferredConstraintFlags("SELECT conname,condeferrable,condeferred FROM pg_constraint WHERE conname='u'", []any{"u", true, true}))
			})
		})
	}
}

func TestReferenceDeferredConstraintDiagnostics(t *testing.T) {
	for _, tc := range []struct{ body, code, message, position string }{
		{"v INT,CONSTRAINT ck CHECK(v>0) DEFERRABLE", "0A000", "CHECK constraints cannot be marked DEFERRABLE", "DEFERRABLE"},
		{"v INT,CONSTRAINT nn NOT NULL v DEFERRABLE", "0A000", "NOT NULL constraints cannot be marked DEFERRABLE", "DEFERRABLE"},
		{"v INT CHECK(v>0) DEFERRABLE", "42601", "misplaced DEFERRABLE clause", "DEFERRABLE"},
		{"v INT NOT NULL DEFERRABLE", "42601", "misplaced DEFERRABLE clause", "DEFERRABLE"},
		{"v INT UNIQUE DEFERRABLE DEFERRABLE", "42601", "multiple DEFERRABLE/NOT DEFERRABLE clauses not allowed", "DEFERRABLE)"},
		{"v INT UNIQUE INITIALLY IMMEDIATE INITIALLY IMMEDIATE", "42601", "multiple INITIALLY IMMEDIATE/DEFERRED clauses not allowed", "INITIALLY IMMEDIATE)"},
		{"v INT,CONSTRAINT u UNIQUE(v) DEFERRABLE NOT DEFERRABLE", "42601", "conflicting constraint properties", "NOT DEFERRABLE"},
		{"v INT,CONSTRAINT u UNIQUE(v) NOT DEFERRABLE INITIALLY DEFERRED", "42601", "constraint declared INITIALLY DEFERRED must be DEFERRABLE", "INITIALLY DEFERRED"},
	} {
		t.Run(tc.body, func(t *testing.T) {
			fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				q := "CREATE TABLE t(" + tc.body + ")"
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				if tc.position != "" {
					d.Position = fixture.Position(strings.Index(q, tc.position) + 1)
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(q, d)
					s.Prepared = prepared
					if prepared {
						s.ErrorStage = "prepare"
						if !strings.Contains(tc.body, ",CONSTRAINT") {
							s.ErrorStage = "execute"
						}
					}
					run(s)
				}
				run(fixture.Select("SELECT relname FROM pg_class WHERE relname='t'", []string{"relname"}, []string{"NAME"}, nil))
			})
		})
	}
}

func TestReferenceDeferredConstraintKeyRestrictions(t *testing.T) {
	for _, kind := range []string{"UNIQUE", "PRIMARY KEY"} {
		for _, target := range []string{"p(v)", "p"} {
			if kind == "UNIQUE" && target == "p" {
				continue
			}
			t.Run(kind+target, func(t *testing.T) {
				fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
					run(fixture.Exec("CREATE TABLE p(v INT,CONSTRAINT u "+kind+"(v) DEFERRABLE)", 0))
					message := `cannot use a deferrable unique constraint for referenced table "p"`
					if target == "p" {
						message = `cannot use a deferrable primary key for referenced table "p"`
					}
					for _, prepared := range []bool{false, true} {
						run(fixture.ForeignKeyFail("CREATE TABLE c(v INT REFERENCES "+target+")", fixture.ControlsReferenceDiagnostic("55000", message), prepared))
					}
				})
			})
		}
		t.Run("arbiter/"+kind, func(t *testing.T) {
			fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE t(v INT,k INT UNIQUE,CONSTRAINT u "+kind+"(v) DEFERRABLE)", 0))
				run(fixture.Exec("INSERT INTO t SELECT 1,1 WHERE FALSE ON CONFLICT ON CONSTRAINT u DO NOTHING", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,1) ON CONFLICT(k) DO NOTHING", 1))
				d := fixture.ControlsReferenceDiagnostic("55000", "ON CONFLICT does not support deferrable unique constraints/exclusion constraints as arbiters")
				d.Schema = "public"
				d.Table = "t"
				d.Constraint = "u"
				run(fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO t VALUES(2,2) ON CONFLICT ON CONSTRAINT u DO NOTHING", d)))
			})
		})
	}
	t.Run("standalone", func(t *testing.T) {
		fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
			run(fixture.Exec("CREATE TABLE p(v INT)", 0))
			run(fixture.Exec("CREATE UNIQUE INDEX u ON p(v)", 0))
			run(fixture.Exec("CREATE TABLE c(v INT REFERENCES p(v) INITIALLY DEFERRED)", 0))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("INSERT INTO c VALUES(1)", 1))
			run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
			run(fixture.Exec("COMMIT", 0))
		})
	})
}

func TestReferenceDeferredConstraintMetadata(t *testing.T) {
	fixture.DeferredConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
		run(fixture.Exec("CREATE TABLE t(v INT,CONSTRAINT u UNIQUE(v) DEFERRABLE,CONSTRAINT fk FOREIGN KEY(v) REFERENCES p INITIALLY DEFERRED)", 0))
		q := "SELECT conname,condeferrable,condeferred FROM pg_constraint WHERE conrelid='t'::regclass ORDER BY conname"
		want := fixture.DeferredConstraintFlags(q, []any{"fk", true, true}, []any{"u", true, false})
		held, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		run(want)
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, want)
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SET CONSTRAINTS ALL IMMEDIATE", 0))
		run(want)
		run(fixture.Select("SELECT indimmediate FROM pg_index WHERE indexrelid='u'::regclass", []string{"indimmediate"}, []string{"BOOL"}, [][]any{{false}}))
		run(fixture.Select("SELECT constraint_name,is_deferrable,initially_deferred FROM information_schema.table_constraints WHERE table_schema='public' AND table_name='t' ORDER BY constraint_name", []string{"constraint_name", "is_deferrable", "initially_deferred"}, []string{"NAME", "VARCHAR", "VARCHAR"}, [][]any{{"fk", "YES", "YES"}, {"u", "YES", "NO"}}))
		run(fixture.Select("SELECT tgdeferrable,tginitdeferred FROM pg_trigger WHERE tgconstraint=(SELECT oid FROM pg_constraint WHERE conname='fk') ORDER BY tgname", []string{"tgdeferrable", "tginitdeferred"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}, {true, true}, {true, true}, {true, true}}))
		run(fixture.Exec("ROLLBACK", 0))
	})
}
