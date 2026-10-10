package reference_test

import (
	"testing"
)

func temporaryTableScenario(t *testing.T, body func(*tempReferenceSession)) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) { body(newTempReferenceSession(t, p, backend.db, backend.recorder, prepared)) })
			}
		})
	}
}

func temporaryTableRows(s *tempReferenceSession, c referenceExecutor, name string, values ...int64) {
	s.t.Helper()
	rows := make([][]any, 0, len(values))
	for _, v := range values {
		rows = append(rows, []any{v})
	}
	referenceRunStep(s.t, s.p.ctx, c, s.record, sequenceInt64Step(referenceStep{query: "SELECT v FROM " + name + " ORDER BY v", prepared: s.prepared, selectRows: true, columns: []string{"v"}, types: []string{"INT4"}, rows: rows}))
}

func TestReferenceTemporaryTableCreation(t *testing.T) {
	temporaryTableScenario(t, func(s *tempReferenceSession) {
		s.utility("CREATE TEMP TABLE creation(v INT NOT NULL DEFAULT 11 CHECK(v > 0))", nil)
		s.exec(s.c, "INSERT INTO creation DEFAULT VALUES", 1)
		temporaryTableRows(s, s.c, "creation", 11)
		s.utility("CREATE TEMPORARY TABLE copied(LIKE creation)", nil)
		s.exec(s.c, "INSERT INTO copied VALUES(22)", 1)
		temporaryTableRows(s, s.c, "copied", 22)
		temporaryTableRows(s, s.c, "creation", 11)
		s.utility("CREATE TEMP TABLE empty_shape()", nil)
		s.utility("DROP TABLE pg_temp.empty_shape", nil)
		s.utility("DROP TABLE copied,creation", nil)
	})
}

func temporaryTableIsolation(s *tempReferenceSession) {
	s.exec(s.c, "CREATE TABLE public.isolated(v INT)", 0)
	s.exec(s.c, "INSERT INTO public.isolated VALUES(7)", 1)
	s.utility("CREATE TEMP TABLE isolated(v INT)", nil)
	s.exec(s.c, "INSERT INTO isolated VALUES(11)", 1)
	temporaryTableRows(s, s.peer, "isolated", 7)
	s.exec(s.peer, "CREATE TEMPORARY TABLE isolated(v INT)", 0)
	s.exec(s.peer, "INSERT INTO isolated VALUES(22)", 1)
	temporaryTableRows(s, s.c, "isolated", 11)
	temporaryTableRows(s, s.peer, "isolated", 22)
	temporaryTableRows(s, s.c, "public.isolated", 7)
	temporaryTableRows(s, s.peer, "public.isolated", 7)
	temporaryTableRows(s, s.c, "pg_temp.isolated", 11)
}

func TestReferenceTemporaryTableIsolation(t *testing.T) {
	temporaryTableScenario(t, temporaryTableIsolation)
}

func TestReferenceTemporaryTableNamespace(t *testing.T) {
	temporaryTableScenario(t, func(s *tempReferenceSession) {
		temporaryTableIsolation(s)
		s.exec(s.c, "SET search_path TO public,pg_temp", 0)
		temporaryTableRows(s, s.c, "isolated", 7)
		temporaryTableRows(s, s.c, "pg_temp.isolated", 11)
		s.utility("CREATE TABLE pg_temp.alias(v INT)", nil)
		s.exec(s.c, "INSERT INTO pg_temp.alias VALUES(31)", 1)
		temporaryTableRows(s, s.c, "pg_temp.alias", 31)
		s.exec(s.c, "SET search_path TO pg_temp", 0)
		own := tempReferenceNamespace(s.t, s, false)
		s.utility("CREATE TABLE "+own+".actual(v INT)", nil)
		s.utility("CREATE TABLE path_owned(v INT)", nil)
		s.utility("DROP TABLE pg_temp.alias,pg_temp.actual,pg_temp.path_owned", nil)
	})
}

func temporaryTableCommit(s *tempReferenceSession) {
	s.utility("CREATE TEMP TABLE preserved(v INT) ON COMMIT PRESERVE ROWS", nil)
	s.exec(s.c, "INSERT INTO preserved VALUES(11)", 1)
	s.exec(s.c, "BEGIN", 0)
	s.exec(s.c, "INSERT INTO preserved VALUES(12)", 1)
	temporaryTableRows(s, s.c, "preserved", 11, 12)
	s.exec(s.c, "COMMIT", 0)
	temporaryTableRows(s, s.c, "preserved", 11, 12)
	s.exec(s.c, "BEGIN", 0)
	s.exec(s.c, "INSERT INTO preserved VALUES(13)", 1)
	s.exec(s.c, "ROLLBACK", 0)
	temporaryTableRows(s, s.c, "preserved", 11, 12)
	s.utility("CREATE TEMP TABLE cleared(v INT UNIQUE) ON COMMIT DELETE ROWS", nil)
	s.exec(s.c, "INSERT INTO cleared VALUES(9)", 1)
	temporaryTableRows(s, s.c, "cleared")
	s.exec(s.c, "BEGIN", 0)
	s.exec(s.c, "INSERT INTO cleared VALUES(1),(2)", 2)
	temporaryTableRows(s, s.c, "cleared", 1, 2)
	s.exec(s.c, "COMMIT", 0)
	temporaryTableRows(s, s.c, "cleared")
	s.exec(s.c, "BEGIN", 0)
	s.exec(s.c, "INSERT INTO cleared VALUES(1)", 1)
	s.exec(s.c, "COMMIT AND CHAIN", 0)
	temporaryTableRows(s, s.c, "cleared")
	s.exec(s.c, "ROLLBACK", 0)
	s.utility("CREATE TEMP TABLE instant(v INT) ON COMMIT DROP", nil)
	s.missing(s.c, "instant", "v", "15")
	s.exec(s.c, "BEGIN", 0)
	s.utility("CREATE TEMP TABLE dropped(v INT) ON COMMIT DROP", nil)
	s.exec(s.c, "INSERT INTO dropped VALUES(1)", 1)
	temporaryTableRows(s, s.c, "dropped", 1)
	s.exec(s.c, "COMMIT", 0)
	s.missing(s.c, "dropped", "v", "15")
	tx, err := s.c.BeginTx(s.p.ctx, nil)
	if err != nil {
		s.t.Fatal(err)
	}
	s.exec(tx, "INSERT INTO preserved VALUES(14)", 1)
	if err := tx.Commit(); err != nil {
		s.t.Fatal(err)
	}
	temporaryTableRows(s, s.c, "preserved", 11, 12, 14)
}

func TestReferenceTemporaryTableCommit(t *testing.T) {
	temporaryTableScenario(t, temporaryTableCommit)
}

func TestReferenceTemporaryTableRollback(t *testing.T) {
	temporaryTableScenario(t, func(s *tempReferenceSession) {
		s.exec(s.c, "BEGIN", 0)
		s.utility("CREATE TEMP TABLE undone(v INT)", nil)
		s.exec(s.c, "INSERT INTO undone VALUES(1)", 1)
		s.exec(s.c, "ROLLBACK", 0)
		s.missing(s.c, "undone", "v", "15")
		s.utility("CREATE TEMP TABLE kept(v INT NOT NULL)", nil)
		s.exec(s.c, "INSERT INTO kept VALUES(7)", 1)
		s.exec(s.c, "BEGIN", 0)
		s.exec(s.c, "SAVEPOINT mark", 0)
		s.utility("DROP TABLE kept", nil)
		s.utility("CREATE TEMP TABLE after_mark(v INT) ON COMMIT DROP", nil)
		s.exec(s.c, "ROLLBACK TO mark", 0)
		temporaryTableRows(s, s.c, "kept", 7)
		s.missing(s.c, "after_mark", "v", "15")
		s.exec(s.c, "ROLLBACK TO mark", 0)
		s.exec(s.c, "COMMIT", 0)
		temporaryTableRows(s, s.c, "kept", 7)
		d := tempReferenceDiagnostic("23502", `null value in column "v" of relation "kept" violates not-null constraint`, "Failing row contains (null).", "")
		d.Schema, d.Table, d.Column = "pg_temp", "kept", "v"
		// Namespace identity is generated; discover it rather than pinning allocator output.
		s.exec(s.c, "SET search_path TO pg_temp", 0)
		d.Schema = tempReferenceNamespace(s.t, s, false)
		referenceRunStep(s.t, s.p.ctx, s.c, s.record, referenceStep{query: "INSERT INTO kept VALUES(8),(NULL)", state: d.Code, diagnostic: d, errorStage: "execute"})
		temporaryTableRows(s, s.c, "kept", 7)
	})
}

func TestReferenceTemporaryTableClose(t *testing.T) {
	temporaryTableScenario(t, func(s *tempReferenceSession) {
		temporaryTableIsolation(s)
		if err := s.c.Close(); err != nil {
			s.t.Fatal(err)
		}
		borrowed, err := s.db.Conn(s.p.ctx)
		if err != nil {
			s.t.Fatal(err)
		}
		s.c = borrowed
		defer borrowed.Close()
		temporaryTableRows(s, s.c, "isolated", 11)
		s.exec(s.c, "BEGIN", 0)
		s.utility("CREATE TEMP TABLE private_close(v INT)", nil)
		s.exec(s.c, "INSERT INTO isolated VALUES(12)", 1)
		s.retire()
		temporaryTableRows(s, s.peer, "isolated", 22)
		temporaryTableRows(s, s.peer, "public.isolated", 7)
		c, err := s.db.Conn(s.p.ctx)
		if err != nil {
			s.t.Fatal(err)
		}
		defer c.Close()
		temporaryTableRows(s, c, "isolated", 7)
		s.missing(c, "private_close", "v", "15")
	})
}
