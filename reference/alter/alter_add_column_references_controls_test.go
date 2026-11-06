package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func columnReferencesControlRows(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
	t.Helper()
	run := columnReferencesRun(t, p, b, c, prepared)
	run(fixture.Select("SELECT id,pid FROM t ORDER BY id", []string{"id", "pid"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(1)}}))
	run(columnReferencesDefinitions("t", []any{"fk", "f", "FOREIGN KEY (pid) REFERENCES p(id) ON DELETE CASCADE"}))
	run(enforcementFlags("t", "fk", true, true))
	oid := enforcementIdentity(t, p, b, c, "t", "fk", "FOREIGN KEY (pid) REFERENCES p(id) ON DELETE CASCADE", true, true)
	enforcementTriggers(t, p, b, c, oid, true)
	run(fixture.Select("SELECT conkey::text,confkey::text,conindid=(SELECT conindid FROM pg_constraint WHERE conrelid='p'::regclass AND contype='p') AS provider FROM pg_constraint WHERE oid=$1::oid", []string{"conkey", "confkey", "provider"}, []string{"TEXT", "TEXT", "BOOL"}, [][]any{{"{2}", "{1}", true}}, oid))
	run(fixture.Select("SELECT deptype,refobjid::regclass::text AS refname,refobjsubid FROM pg_depend WHERE classid='pg_constraint'::regclass AND objid=$1::oid AND refclassid='pg_class'::regclass ORDER BY deptype,refname,refobjsubid", []string{"deptype", "refname", "refobjsubid"}, []string{"CHAR", "TEXT", "INT4"}, [][]any{{"a", "t", int64(2)}, {"n", "p", int64(1)}, {"n", "p_pkey", int64(0)}}, oid))
	run(columnReferencesDefinitions("deferred", []any{"deferred_fk", "f", "FOREIGN KEY (pid) REFERENCES p(id) DEFERRABLE INITIALLY DEFERRED"}))
	run(fixture.DeferredConstraintFlags("SELECT conname,condeferrable,condeferred FROM pg_constraint WHERE conrelid='deferred'::regclass AND contype='f'", []any{"deferred_fk", true, true}))
	run(fixture.Select("SELECT id,pid FROM deferred ORDER BY id", []string{"id", "pid"}, []string{"INT4", "INT4"}, [][]any{{int64(1), nil}}))
	run(columnReferencesDefinitions("loose", []any{"loose_fk", "f", "FOREIGN KEY (pid) REFERENCES p(id) NOT ENFORCED"}))
	oid = enforcementIdentity(t, p, b, c, "loose", "loose_fk", "FOREIGN KEY (pid) REFERENCES p(id) NOT ENFORCED", false, false)
	enforcementTriggers(t, p, b, c, oid, false)
	run(fixture.Select("SELECT id,pid FROM loose ORDER BY id", []string{"id", "pid"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(9)}}))
}

func columnReferencesControlEffects(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
	t.Helper()
	run := columnReferencesRun(t, p, b, c, prepared)
	run(fixture.ErrorDiagnostic("UPDATE t SET pid=99 WHERE id=1", addConstraintFK("t", "fk", "pid", "99", "p")))
	run(fixture.Exec("DELETE FROM p WHERE id=1", 1))
	run(fixture.Select("SELECT id,pid FROM t ORDER BY id", []string{"id", "pid"}, []string{"INT4", "INT4"}, nil))
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("INSERT INTO deferred VALUES(2,9)", 1))
	run(fixture.Exec("INSERT INTO p VALUES(9)", 1))
	run(fixture.Exec("COMMIT", 0))
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("INSERT INTO deferred VALUES(3,99)", 1))
	run(fixture.ErrorDiagnostic("COMMIT", addConstraintFK("deferred", "deferred_fk", "pid", "99", "p")))
	run(fixture.Select("SELECT id,pid FROM deferred ORDER BY id", []string{"id", "pid"}, []string{"INT4", "INT4"}, [][]any{{int64(1), nil}, {int64(2), int64(9)}}))
	run(fixture.Exec("INSERT INTO loose VALUES(2,99)", 1))
	run(fixture.Select("SELECT id,pid FROM loose ORDER BY id", []string{"id", "pid"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(9)}, {int64(2), int64(99)}}))
}

func TestReferenceAlterAddColumnReferencesControls(t *testing.T) {
	columnReferencesRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		columnReferencesClean(t, p, c, "t", "deferred", "loose", "sentinel", "p")
		run := columnReferencesRun(t, p, b, c, prepared)
		run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
		run(fixture.Exec("INSERT INTO p VALUES(1),(2)", 2))
		run(fixture.Exec("CREATE TABLE t(id INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
		insert := addConstraintHeld(t, p, b, c, "INSERT INTO t(id) VALUES($1)")
		selectID := addConstraintHeld(t, p, b, c, "SELECT id FROM t ORDER BY id")
		selectStar := addConstraintHeld(t, p, b, c, "SELECT * FROM t ORDER BY id")
		fixture.RunStep(t, p.Ctx, selectStar, b.Recorder, fixture.Select(selectStar.SQL, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		q := "ALTER TABLE t ADD pid INT DEFAULT 1 CONSTRAINT fk REFERENCES p ON DELETE CASCADE"
		alter := addConstraintHeld(t, p, b, c, q)
		// Holding a utility is effect-free; both schema and old rows remain old.
		run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		fixture.RunStep(t, p.Ctx, alter, b.Recorder, fixture.Exec(q, 0))
		fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.Exec(insert.SQL, 1, 2))
		fixture.RunStep(t, p.Ctx, selectID, b.Recorder, fixture.Select(selectID.SQL, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		d := fixture.ControlsReferenceDiagnostic("0A000", "cached plan must not change result type")
		failure := fixture.ErrorDiagnostic(selectStar.SQL, d)
		failure.SelectRows, failure.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, selectStar, b.Recorder, failure)
		fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, prepared, "SELECT * FROM t ORDER BY id", []string{"id", "pid"}, []string{"INT4", "INT4"}, []bool{true, true}, 2)
		run(fixture.Exec("CREATE TABLE deferred(id INT)", 0))
		run(fixture.Exec("INSERT INTO deferred VALUES(1)", 1))
		run(fixture.Exec("ALTER TABLE deferred ADD pid INT CONSTRAINT deferred_fk REFERENCES p DEFERRABLE INITIALLY DEFERRED", 0))
		run(fixture.Exec("CREATE TABLE loose(id INT)", 0))
		run(fixture.Exec("INSERT INTO loose VALUES(1)", 1))
		run(fixture.Exec("ALTER TABLE loose ADD pid INT DEFAULT 9 CONSTRAINT loose_fk REFERENCES p NOT ENFORCED", 0))
		columnReferencesControlRows(t, p, b, c, prepared)
		if !b.PG {
			dump := addConstraintDump(t, p)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			missing := fixture.ControlsReferenceDiagnostic("42P01", `relation "t" does not exist`)
			missing.Position = "15"
			run(fixture.ErrorDiagnostic("SELECT * FROM t", missing))
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			columnReferencesControlRows(t, p, b, c, prepared)
			enforcementSameDump(t, p, dump)
			// Replay the captured acyclic SQL unchanged on the same pinned oracle.
			// This phase neither reconstructs a schema nor depends on native OIDs.
			if p.Postgres != nil {
				pgc, err := p.Postgres.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				func() {
					defer pgc.Close()
					defer func() {
						if _, err := pgc.ExecContext(p.Ctx, "DROP TABLE IF EXISTS t,deferred,loose,p CASCADE"); err != nil {
							t.Error(err)
						}
					}()
					if _, err := pgc.ExecContext(p.Ctx, string(dump)); err != nil {
						t.Fatal("PostgreSQL captured-dump replay", err)
					}
					pg := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
					columnReferencesControlRows(t, p, pg, pgc, prepared)
					columnReferencesControlEffects(t, p, pg, pgc, prepared)
				}()
			}
			run(fixture.Exec("CREATE TABLE sentinel(id INT)", 0))
			run(fixture.Exec("INSERT INTO sentinel VALUES(7)", 1))
			before, attrs := enforcementSnapshot(t, p, b, c), columnReferencesAttributes(t, p, c)
			bad := "CREATE TABLE broken(id INT PRIMARY KEY); CREATE TABLE child(pid INT REFERENCES broken); INSERT INTO child VALUES(99);"
			err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad))
			want := addConstraintFK("child", "child_pid_fkey", "pid", "99", "broken")
			fixture.AssertDiagnostic(t, err, &want)
			columnReferencesUnchanged(t, p, b, c, before, attrs)
			run(fixture.Select("SELECT id FROM sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			columnReferencesControlRows(t, p, b, c, prepared)
		}
		columnReferencesControlEffects(t, p, b, c, prepared)
	})
}

func TestReferenceAlterAddColumnReferencesFeatureSmoke(t *testing.T) {
	columnReferencesRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		columnReferencesClean(t, p, c, "ideas", "cascade_child", "users")
		run := columnReferencesRun(t, p, b, c, prepared)
		run(fixture.Exec("CREATE TABLE users(id INT PRIMARY KEY)", 0))
		run(fixture.Exec("INSERT INTO users VALUES(1),(2)", 2))
		run(fixture.Exec("CREATE TABLE ideas(id INT PRIMARY KEY)", 0))
		run(fixture.Exec("INSERT INTO ideas VALUES(1)", 1))
		run(fixture.Exec("ALTER TABLE ideas ADD user_id int REFERENCES users (id)", 0))
		run(fixture.Exec("ALTER TABLE ideas ADD COLUMN original_id INT NULL REFERENCES ideas(id)", 0))
		rows := fixture.Select("SELECT id,user_id,original_id FROM ideas ORDER BY id", []string{"id", "user_id", "original_id"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), nil, nil}})
		definitions := columnReferencesDefinitions("ideas", []any{"ideas_original_id_fkey", "f", "FOREIGN KEY (original_id) REFERENCES ideas(id)"}, []any{"ideas_user_id_fkey", "f", "FOREIGN KEY (user_id) REFERENCES users(id)"})
		run(rows)
		run(definitions)
		run(fixture.ErrorDiagnostic("UPDATE ideas SET user_id = 99", addConstraintFK("ideas", "ideas_user_id_fkey", "user_id", "99", "users")))
		run(rows)
		run(fixture.Exec("CREATE TABLE cascade_child(id INT)", 0))
		run(fixture.Exec("INSERT INTO cascade_child VALUES(10)", 1))
		run(fixture.Exec("ALTER TABLE cascade_child ADD user_id INT DEFAULT 2 CONSTRAINT cascade_fk REFERENCES users ON DELETE CASCADE", 0))
		if !b.PG {
			dump := addConstraintDump(t, p)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			run(rows)
			run(definitions)
			run(fixture.ErrorDiagnostic("INSERT INTO ideas(id,user_id) VALUES(2,99)", addConstraintFK("ideas", "ideas_user_id_fkey", "user_id", "99", "users")))
			run(fixture.ErrorDiagnostic("UPDATE ideas SET original_id=99", addConstraintFK("ideas", "ideas_original_id_fkey", "original_id", "99", "ideas")))
		}
		run(fixture.Exec("DELETE FROM users WHERE id=2", 1))
		run(fixture.Select("SELECT id,user_id FROM cascade_child ORDER BY id", []string{"id", "user_id"}, []string{"INT4", "INT4"}, nil))
		run(rows)
	})
}
