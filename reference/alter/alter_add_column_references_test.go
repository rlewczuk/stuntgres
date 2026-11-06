package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Backend is outermost so oracle discovery can select /postgres without changing
// the shared scenario. Each route owns fresh objects and genuine Go preparation.
func columnReferencesRoutes(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, bool)) {
	t.Helper()
	addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, prepared := range []bool{false, true} {
			name := "direct"
			if prepared {
				name = "prepared"
			}
			t.Run(name, func(t *testing.T) { body(t, p, b, c, prepared) })
		}
	})
}

func columnReferencesClean(t *testing.T, p *fixture.Pair, c *sql.Conn, tables ...string) {
	t.Helper()
	t.Cleanup(func() {
		if _, err := c.ExecContext(p.Ctx, "DROP TABLE IF EXISTS "+strings.Join(tables, ",")+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
}

func columnReferencesRun(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) func(fixture.Step) {
	t.Helper()
	return func(s fixture.Step) {
		t.Helper()
		if prepared {
			s = fixture.Prepared(s)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func columnReferencesDefinitions(table string, rows ...[]any) fixture.Step {
	return fixture.Select("SELECT conname,contype,pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid=$1::regclass::oid AND contype='f' ORDER BY conname", []string{"conname", "contype", "pg_get_constraintdef"}, []string{"NAME", "CHAR", "TEXT"}, rows, table)
}

func columnReferencesAttributes(t *testing.T, p *fixture.Pair, c *sql.Conn) [][]any {
	t.Helper()
	return enforcementQuery(t, p, c, "SELECT attnum,attname,atttypid,attnotnull,attisdropped,atthasdef FROM pg_attribute WHERE attrelid='t'::regclass AND attnum>0 ORDER BY attnum")
}

func columnReferencesUnchanged(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, state enforcementState, attrs [][]any) {
	t.Helper()
	enforcementUnchanged(t, p, b, c, state)
	if after := columnReferencesAttributes(t, p, c); !reflect.DeepEqual(attrs, after) {
		t.Fatalf("failed ADD changed attributes: %#v -> %#v", attrs, after)
	}
}

func TestReferenceAlterAddColumnReferences(t *testing.T) {
	columnReferencesRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		columnReferencesClean(t, p, c, "ideas", "users")
		run := columnReferencesRun(t, p, b, c, prepared)
		run(fixture.Exec("CREATE TABLE users(id INT PRIMARY KEY)", 0))
		run(fixture.Exec("INSERT INTO users VALUES(1)", 1))
		run(fixture.Exec("CREATE TABLE ideas(id INT PRIMARY KEY)", 0))
		run(fixture.Exec("INSERT INTO ideas VALUES(1)", 1))
		run(fixture.Exec("ALTER TABLE ideas ADD user_id int REFERENCES users (id)", 0))
		run(fixture.Exec("ALTER TABLE ideas ADD COLUMN original_id INT NULL REFERENCES ideas(id)", 0))
		run(columnReferencesDefinitions("ideas",
			[]any{"ideas_original_id_fkey", "f", "FOREIGN KEY (original_id) REFERENCES ideas(id)"},
			[]any{"ideas_user_id_fkey", "f", "FOREIGN KEY (user_id) REFERENCES users(id)"}))
		q := "SELECT id,user_id,original_id FROM ideas ORDER BY id"
		run(fixture.Select(q, []string{"id", "user_id", "original_id"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), nil, nil}}))
		fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, prepared, q, []string{"id", "user_id", "original_id"}, []string{"INT4", "INT4", "INT4"}, []bool{false, true, true}, 1)
		run(fixture.Exec("UPDATE ideas SET user_id=1,original_id=1", 1))
		run(fixture.ErrorDiagnostic("UPDATE ideas SET user_id = 99", addConstraintFK("ideas", "ideas_user_id_fkey", "user_id", "99", "users")))
		run(fixture.ErrorDiagnostic("DELETE FROM users WHERE id=1", fixture.ForeignKeyParent("users", "ideas", "ideas_user_id_fkey", `Key (id)=(1) is still referenced from table "ideas".`)))
		run(fixture.Select(q, []string{"id", "user_id", "original_id"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(1), int64(1)}}))
	})
}

func TestReferenceAlterAddColumnReferencesSkipped(t *testing.T) {
	columnReferencesRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, original *sql.Conn, prepared bool) {
		var notices []fixture.Diagnostic
		_, c, record := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		columnReferencesClean(t, p, c, "t", "p", "q", "r")
		b.Recorder = record
		run := columnReferencesRun(t, p, b, c, prepared)
		run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
		run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
		run(fixture.Exec("CREATE TABLE t(id INT,pid INT DEFAULT 1 CONSTRAINT original_fk REFERENCES p)", 0))
		run(fixture.Exec("INSERT INTO t(id) VALUES(1)", 1))
		before, attrs := enforcementSnapshot(t, p, b, c), columnReferencesAttributes(t, p, c)
		notice := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42701", Message: `column "pid" of relation "t" already exists, skipping`}
		assertNotices := func(want ...fixture.Diagnostic) {
			t.Helper()
			if !reflect.DeepEqual(notices, want) {
				t.Fatalf("notices %#v, want %#v", notices, want)
			}
			notices = nil
		}
		run(fixture.Exec("ALTER TABLE t ADD COLUMN IF NOT EXISTS pid TEXT REFERENCES missing(id)", 0))
		assertNotices(notice)
		columnReferencesUnchanged(t, p, b, c, before, attrs)
		run(fixture.Select("SELECT id,pid FROM t", []string{"id", "pid"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}}))
		run(fixture.Exec("ALTER TABLE t DROP pid", 0))
		q := "ALTER TABLE t ADD IF NOT EXISTS pid INT REFERENCES p(id)"
		held := addConstraintHeld(t, p, b, c, q)
		assertNotices()
		fixture.RunStep(t, p.Ctx, held, record, fixture.Exec(q, 0))
		assertNotices()
		fixture.RunStep(t, p.Ctx, held, record, fixture.Exec(q, 0))
		assertNotices(notice)
		run(columnReferencesDefinitions("t", []any{"t_pid_fkey", "f", "FOREIGN KEY (pid) REFERENCES p(id)"}))
		for _, addFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("drop_readd/add_first_%t", addFirst), func(t *testing.T) {
				q := "ALTER TABLE t DROP pid, ADD IF NOT EXISTS pid INT REFERENCES p(id)"
				if addFirst {
					q = "ALTER TABLE t ADD IF NOT EXISTS pid INT REFERENCES p(id), DROP pid"
				}
				run(fixture.Exec(q+", ADD IF NOT EXISTS pid INT REFERENCES missing(id)", 0))
				assertNotices(notice)
				run(columnReferencesDefinitions("t", []any{"t_pid_fkey", "f", "FOREIGN KEY (pid) REFERENCES p(id)"}))
				run(fixture.Select("SELECT id,pid FROM t", []string{"id", "pid"}, []string{"INT4", "INT4"}, [][]any{{int64(1), nil}}))
			})
		}
		run(fixture.Exec("ALTER TABLE t DROP pid", 0))
		run(fixture.Exec("CREATE TABLE q(id INT PRIMARY KEY)", 0))
		run(fixture.Exec("CREATE TABLE r(id INT PRIMARY KEY)", 0))
		run(fixture.Exec("ALTER TABLE t ADD IF NOT EXISTS pid INT CONSTRAINT first_fk REFERENCES p, ADD IF NOT EXISTS pid INT CONSTRAINT skipped_fk REFERENCES missing(id), ADD qid INT CONSTRAINT second_fk REFERENCES q, ADD rid INT CONSTRAINT third_fk REFERENCES r", 0))
		assertNotices(notice)
		run(columnReferencesDefinitions("t", []any{"first_fk", "f", "FOREIGN KEY (pid) REFERENCES p(id)"}, []any{"second_fk", "f", "FOREIGN KEY (qid) REFERENCES q(id)"}, []any{"third_fk", "f", "FOREIGN KEY (rid) REFERENCES r(id)"}))
		before, attrs = enforcementSnapshot(t, p, b, c), columnReferencesAttributes(t, p, c)
		addConstraintError(t, p, c, "ALTER TABLE t ADD extra INT REFERENCES p, ADD extra INT REFERENCES p", fixture.ControlsReferenceDiagnostic("42701", `column "extra" of relation "t" already exists`), prepared, "execute")
		columnReferencesUnchanged(t, p, b, c, before, attrs)
	})
}

func TestReferenceAlterAddColumnReferencesNaming(t *testing.T) {
	columnReferencesRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		for _, tc := range []struct {
			name, setup, alter string
			want               [][]any
		}{
			{"multiple", "", "ADD pid INT REFERENCES p REFERENCES q", [][]any{{"t_pid_fkey", "f", "FOREIGN KEY (pid) REFERENCES p(id)"}, {"t_pid_fkey1", "f", "FOREIGN KEY (pid) REFERENCES q(id)"}}},
			{"namespace", "CREATE TABLE reserved(a INT CONSTRAINT t_pid_fkey REFERENCES p)", "ADD pid INT REFERENCES p", [][]any{{"t_pid_fkey1", "f", "FOREIGN KEY (pid) REFERENCES p(id)"}}},
			{"explicit_before", "", "ADD FOREIGN KEY(pid) REFERENCES q, ADD pid INT REFERENCES p", [][]any{{"t_pid_fkey", "f", "FOREIGN KEY (pid) REFERENCES p(id)"}, {"t_pid_fkey1", "f", "FOREIGN KEY (pid) REFERENCES q(id)"}}},
			{"explicit_after", "", "ADD pid INT REFERENCES p, ADD FOREIGN KEY(pid) REFERENCES q", [][]any{{"t_pid_fkey", "f", "FOREIGN KEY (pid) REFERENCES p(id)"}, {"t_pid_fkey1", "f", "FOREIGN KEY (pid) REFERENCES q(id)"}}},
			{"check_before", "", "ADD pid INT CONSTRAINT t_pid_fkey CHECK(pid>0) REFERENCES p", [][]any{{"t_pid_fkey1", "f", "FOREIGN KEY (pid) REFERENCES p(id)"}}},
			{"check_after", "", "ADD pid INT REFERENCES p CONSTRAINT t_pid_fkey CHECK(pid>0)", [][]any{{"t_pid_fkey1", "f", "FOREIGN KEY (pid) REFERENCES p(id)"}}},
			{"table_check_before", "", "ADD CONSTRAINT t_pid_fkey CHECK(pid>0), ADD pid INT REFERENCES p", nil},
			{"table_check_after", "", "ADD pid INT REFERENCES p, ADD CONSTRAINT t_pid_fkey CHECK(pid>0)", nil},
			{"unique_reserves_first", "", "ADD pid INT REFERENCES p CONSTRAINT t_pid_fkey UNIQUE", [][]any{{"t_pid_fkey1", "f", "FOREIGN KEY (pid) REFERENCES p(id)"}}},
			{"not_null_reserves_first", "", "ADD pid INT REFERENCES p CONSTRAINT t_pid_fkey NOT NULL", [][]any{{"t_pid_fkey1", "f", "FOREIGN KEY (pid) REFERENCES p(id)"}}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				columnReferencesClean(t, p, c, "t", "reserved", "p", "q")
				run := columnReferencesRun(t, p, b, c, prepared)
				run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
				run(fixture.Exec("CREATE TABLE q(id INT PRIMARY KEY)", 0))
				run(fixture.Exec("CREATE TABLE t(id INT)", 0))
				if tc.setup != "" {
					run(fixture.Exec(tc.setup, 0))
				}
				if tc.want == nil {
					before, attrs := enforcementSnapshot(t, p, b, c), columnReferencesAttributes(t, p, c)
					addConstraintError(t, p, c, "ALTER TABLE t "+tc.alter, fixture.ControlsReferenceDiagnostic("42710", `constraint "t_pid_fkey" for relation "t" already exists`), prepared, "execute")
					columnReferencesUnchanged(t, p, b, c, before, attrs)
					return
				}
				run(fixture.Exec("ALTER TABLE t "+tc.alter, 0))
				run(columnReferencesDefinitions("t", tc.want...))
			})
		}
		for _, dropColumn := range []bool{false, true} {
			t.Run(fmt.Sprintf("reuse/drop_column_%t", dropColumn), func(t *testing.T) {
				columnReferencesClean(t, p, c, "t", "p")
				run := columnReferencesRun(t, p, b, c, prepared)
				run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
				run(fixture.Exec("CREATE TABLE t(id INT,pid INT REFERENCES p)", 0))
				q := "ALTER TABLE t DROP CONSTRAINT t_pid_fkey, ADD next INT CONSTRAINT t_pid_fkey REFERENCES p"
				column := "next"
				if dropColumn {
					q = "ALTER TABLE t DROP pid, ADD pid INT REFERENCES p"
					column = "pid"
				}
				run(fixture.Exec(q, 0))
				run(columnReferencesDefinitions("t", []any{"t_pid_fkey", "f", "FOREIGN KEY (" + column + ") REFERENCES p(id)"}))
			})
		}
		for _, kind := range []string{"CHECK(pid>0)", "NOT NULL", "UNIQUE", "REFERENCES missing(id)"} {
			t.Run("skipped_reservation/"+kind, func(t *testing.T) {
				columnReferencesClean(t, p, c, "t", "p")
				run := columnReferencesRun(t, p, b, c, prepared)
				run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
				run(fixture.Exec("CREATE TABLE t(old INT,pid INT)", 0))
				run(fixture.Exec("ALTER TABLE t ADD IF NOT EXISTS pid INT CONSTRAINT t_id_fkey "+kind+", ADD id INT REFERENCES p", 0))
				run(columnReferencesDefinitions("t", []any{"t_id_fkey", "f", "FOREIGN KEY (id) REFERENCES p(id)"}))
			})
		}
		for _, checkFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("explicit_collision/check_first_%t", checkFirst), func(t *testing.T) {
				columnReferencesClean(t, p, c, "t", "p")
				run := columnReferencesRun(t, p, b, c, prepared)
				run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
				run(fixture.Exec("CREATE TABLE t(id INT)", 0))
				clause := "CONSTRAINT same REFERENCES p CONSTRAINT same CHECK(pid>0)"
				if checkFirst {
					clause = "CONSTRAINT same CHECK(pid>0) CONSTRAINT same REFERENCES p"
				}
				before, attrs := enforcementSnapshot(t, p, b, c), columnReferencesAttributes(t, p, c)
				addConstraintError(t, p, c, "ALTER TABLE t ADD pid INT "+clause, fixture.ControlsReferenceDiagnostic("42710", `constraint "same" for relation "t" already exists`), prepared, "execute")
				columnReferencesUnchanged(t, p, b, c, before, attrs)
			})
		}
		t.Run("domain_namespace_reservation", func(t *testing.T) {
			columnReferencesClean(t, p, c, "t", "p")
			t.Cleanup(func() {
				if _, err := c.ExecContext(p.Ctx, "DROP DOMAIN IF EXISTS reserved_domain"); err != nil {
					t.Error(err)
				}
			})
			run := columnReferencesRun(t, p, b, c, prepared)
			run(fixture.Exec("CREATE DOMAIN reserved_domain AS INT CONSTRAINT t_pid_fkey CHECK(VALUE>0)", 0))
			run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
			run(fixture.Exec("CREATE TABLE t(id INT)", 0))
			run(fixture.Exec("ALTER TABLE t ADD pid INT REFERENCES p", 0))
			run(columnReferencesDefinitions("t", []any{"t_pid_fkey1", "f", "FOREIGN KEY (pid) REFERENCES p(id)"}))
			run(fixture.Exec("ALTER TABLE t DROP pid, ADD pid INT REFERENCES p", 0))
			run(columnReferencesDefinitions("t", []any{"t_pid_fkey1", "f", "FOREIGN KEY (pid) REFERENCES p(id)"}))
		})
	})
}
