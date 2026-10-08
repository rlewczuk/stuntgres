//go:build reference

package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceLikeControls(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(id INTEGER,v TEXT,c CHAR(3),b BYTEA,p TEXT COLLATE \"C\",CHECK(v ILIKE 'a%' ESCAPE '!'))", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1,'Ab','a','\\xc3a9','a%'),(2,NULL,NULL,NULL,NULL)", 2))
	p.step(t, referenceExec("CREATE VIEW matched AS SELECT id,v ILIKE p ESCAPE '!' AS matched,c LIKE 'a__' AS padded,b LIKE '\\x5f5f' AS octets FROM t", 0))
	s := referenceSelect("SELECT matched,padded,octets FROM matched ORDER BY id", []string{"matched", "padded", "octets"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}, {nil, nil, nil}})
	p.step(t, s)
	p.step(t, referencePrepared(s))
	// Replay depends on a proven populated native snapshot. Preserve failed
	// paired assertions, but do not replace PostgreSQL with a failed setup dump.
	if t.Failed() {
		t.FailNow()
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	p.step(t, s)
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
		t.Fatal("canonical redump", err)
	}
	// Replay the public native stream on PostgreSQL, then assert the same
	// independent physical and matching results, not pg_dump text identity.
	if _, err := p.postgres.ExecContext(p.ctx, "DROP VIEW matched; DROP TABLE t"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
		t.Fatal(err)
	}
	referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s)
	physical := referenceSelect("SELECT c,b FROM t ORDER BY id", []string{"c", "b"}, []string{"BPCHAR", "BYTEA"}, [][]any{{"a  ", "\xc3\xa9"}, {nil, nil}})
	p.step(t, physical)
	// A late replay failure must not publish the already-created replacement.
	bad := dump.String() + "INSERT INTO t VALUES(3,'b','b','\\x62','b%');"
	if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(bad)); err == nil {
		t.Fatal("bad CHECK replay accepted")
	}
	var retained bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &retained); err != nil || !bytes.Equal(redump.Bytes(), retained.Bytes()) {
		t.Fatal("failed replay published", err)
	}
}

func TestReferenceLikeCheckAtomicity(t *testing.T) {
	referenceCheckBackends(t, func(t *testing.T, run func(referenceStep)) {
		run(referenceExec("CREATE TABLE guarded(v TEXT CONSTRAINT starts_a CHECK(v LIKE 'a%' ESCAPE '!'))", 0))
		run(referenceExec("INSERT INTO guarded VALUES('a'),(NULL)", 2))
		want := referenceSelect("SELECT v FROM guarded ORDER BY v", []string{"v"}, []string{"TEXT"}, [][]any{{"a"}, {nil}})
		for _, q := range []string{"INSERT INTO guarded VALUES('ab'),('b')", "UPDATE guarded SET v='b'"} {
			s := referenceImplicitError(q, referenceCheckViolation("guarded", "starts_a", "b"))
			s.errorStage = "execute"
			run(s)
			run(want)
			run(referencePrepared(s))
			run(want)
		}
	})
}

func TestReferenceLikeOracleStoredDependencies(t *testing.T) {
	likeReferenceStoredDependencies(t, true)
}
func TestReferenceLikeStoredDependencies(t *testing.T) { likeReferenceStoredDependencies(t, false) }

func likeReferenceStoredDependencies(t *testing.T, oracleOnly bool) {
	p := newReferencePair(t)
	run := func(t *testing.T, s referenceStep) {
		if oracleOnly {
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s)
		} else {
			p.step(t, s)
		}
	}
	run(t, referenceExec("CREATE TABLE t(id INT,p TEXT,e TEXT,CONSTRAINT pattern_only CHECK('ab' LIKE p),CONSTRAINT escape_only CHECK('a%b' LIKE 'a!%b' ESCAPE e))", 0))
	run(t, referenceExec("INSERT INTO t VALUES(1,'a%','!'),(2,NULL,NULL)", 2))
	run(t, referenceExec("CREATE VIEW pattern_view AS SELECT id,'ab' LIKE p AS matched FROM t", 0))
	run(t, referenceExec("CREATE VIEW escape_view AS SELECT id,'a%b' LIKE 'a!%b' ESCAPE e AS matched FROM t", 0))
	if t.Failed() {
		t.FailNow()
	}
	backends := []struct {
		db       referenceExecutor
		recorder *referenceRecorder
	}{{p.postgres, p.postgresRecord}}
	if !oracleOnly {
		backends = append(backends, struct {
			db       referenceExecutor
			recorder *referenceRecorder
		}{p.memory, p.memoryRecord})
	}
	var held []struct {
		db       referenceHeldStatement
		recorder *referenceRecorder
	}
	for _, backend := range backends {
		for _, query := range []string{"SELECT id,matched FROM pattern_view ORDER BY id", "SELECT id,matched FROM escape_view ORDER BY id"} {
			statement, err := backend.db.PrepareContext(p.ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer statement.Close()
			held = append(held, struct {
				db       referenceHeldStatement
				recorder *referenceRecorder
			}{referenceHeldStatement{backend.db, statement, query}, backend.recorder})
		}
	}
	views := func(t *testing.T) {
		t.Helper()
		for _, query := range []string{"SELECT id,matched FROM pattern_view ORDER BY id", "SELECT id,matched FROM escape_view ORDER BY id"} {
			run(t, referenceSelect(query, []string{"id", "matched"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), nil}}))
		}
		for _, statement := range held {
			referenceRunStep(t, p.ctx, statement.db, statement.recorder, referenceSelect(statement.db.query, []string{"id", "matched"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), nil}}))
		}
	}
	run(t, referenceExec("ALTER TABLE t RENAME COLUMN p TO pattern", 0))
	views(t)
	run(t, referenceExec("ALTER TABLE t RENAME COLUMN e TO escape_value", 0))
	views(t)
	checks := func(t *testing.T) {
		t.Helper()
		for _, tc := range []struct{ query, name, detail string }{
			{"INSERT INTO t VALUES(3,'b%','!')", "pattern_only", "3, b%, !"},
			{"INSERT INTO t VALUES(3,'a%','?')", "escape_only", "3, a%, ?"},
			{"UPDATE t SET pattern='b%' WHERE id=1", "pattern_only", "1, b%, !"},
			{"UPDATE t SET escape_value='?' WHERE id=1", "escape_only", "1, a%, ?"},
		} {
			t.Run(tc.query, func(t *testing.T) {
				s := referenceImplicitError(tc.query, referenceCheckViolation("t", tc.name, tc.detail))
				s.errorStage = "execute"
				run(t, s)
				run(t, referencePrepared(s))
			})
		}
		run(t, referenceSelect("SELECT id,pattern,escape_value FROM t ORDER BY id", []string{"id", "pattern", "escape_value"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "a%", "!"}, {int64(2), nil, nil}}))
	}
	views(t)
	checks(t)
	for _, tc := range []struct{ column, view string }{{"pattern", "pattern_view"}, {"escape_value", "escape_view"}} {
		t.Run(tc.column, func(t *testing.T) {
			q := "ALTER TABLE t DROP COLUMN " + tc.column + " RESTRICT"
			s := referenceImplicitError(q, referenceDiagnostic{Code: "2BP01", Message: "cannot drop column " + tc.column + " of table t because other objects depend on it", Detail: "view " + tc.view + " depends on column " + tc.column + " of table t", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
			s.errorStage = "execute"
			run(t, s)
			views(t)
			checks(t)
			run(t, referencePrepared(s))
			views(t)
			checks(t)
		})
	}
	// PostgreSQL auto-removes own-table CHECK edges on DROP COLUMN. A separate
	// table proves that distinction without adding native DROP VIEW support.
	run(t, referenceExec("CREATE TABLE check_t(id INT,p TEXT,e TEXT,CONSTRAINT pattern_only CHECK('ab' LIKE p),CONSTRAINT escape_only CHECK('a%b' LIKE 'a!%b' ESCAPE e))", 0))
	run(t, referenceExec("INSERT INTO check_t VALUES(1,'a%','!'),(2,NULL,NULL)", 2))
	run(t, referenceExec("ALTER TABLE check_t RENAME COLUMN p TO pattern", 0))
	run(t, referenceExec("ALTER TABLE check_t RENAME COLUMN e TO escape_value", 0))
	run(t, referenceExec("ALTER TABLE check_t DROP COLUMN pattern RESTRICT", 0))
	s := referenceImplicitError("INSERT INTO check_t VALUES(3,'?')", referenceCheckViolation("check_t", "escape_only", "3, ?"))
	s.errorStage = "execute"
	run(t, s)
	run(t, referencePrepared(s))
	run(t, referenceSelect("SELECT id,escape_value FROM check_t ORDER BY id", []string{"id", "escape_value"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "!"}, {int64(2), nil}}))
	run(t, referenceExec("ALTER TABLE check_t DROP COLUMN escape_value RESTRICT", 0))
	run(t, referenceExec("INSERT INTO check_t VALUES(3)", 1))
	run(t, referenceSelect("SELECT id FROM check_t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
}
