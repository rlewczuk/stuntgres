package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTemporaryTableCreation(t *testing.T) {
	fixture.TemporaryTableScenario(t, func(s *fixture.TempReferenceSession) {
		s.Utility("CREATE TEMP TABLE creation(v INT NOT NULL DEFAULT 11 CHECK(v > 0))", nil)
		s.Exec(s.C, "INSERT INTO creation DEFAULT VALUES", 1)
		fixture.TemporaryTableRows(s, s.C, "creation", 11)
		s.Utility("CREATE TEMPORARY TABLE copied(LIKE creation)", nil)
		s.Exec(s.C, "INSERT INTO copied VALUES(22)", 1)
		fixture.TemporaryTableRows(s, s.C, "copied", 22)
		fixture.TemporaryTableRows(s, s.C, "creation", 11)
		s.Utility("CREATE TEMP TABLE empty_shape()", nil)
		s.Utility("DROP TABLE pg_temp.empty_shape", nil)
		s.Utility("DROP TABLE copied,creation", nil)
	})
}

func temporaryTableIsolation(s *fixture.TempReferenceSession) {
	s.Exec(s.C, "CREATE TABLE public.isolated(v INT)", 0)
	s.Exec(s.C, "INSERT INTO public.isolated VALUES(7)", 1)
	s.Utility("CREATE TEMP TABLE isolated(v INT)", nil)
	s.Exec(s.C, "INSERT INTO isolated VALUES(11)", 1)
	fixture.TemporaryTableRows(s, s.Peer, "isolated", 7)
	s.Exec(s.Peer, "CREATE TEMPORARY TABLE isolated(v INT)", 0)
	s.Exec(s.Peer, "INSERT INTO isolated VALUES(22)", 1)
	fixture.TemporaryTableRows(s, s.C, "isolated", 11)
	fixture.TemporaryTableRows(s, s.Peer, "isolated", 22)
	fixture.TemporaryTableRows(s, s.C, "public.isolated", 7)
	fixture.TemporaryTableRows(s, s.Peer, "public.isolated", 7)
	fixture.TemporaryTableRows(s, s.C, "pg_temp.isolated", 11)
}

func TestReferenceTemporaryTableIsolation(t *testing.T) {
	fixture.TemporaryTableScenario(t, temporaryTableIsolation)
}

func TestReferenceTemporaryTableNamespace(t *testing.T) {
	fixture.TemporaryTableScenario(t, func(s *fixture.TempReferenceSession) {
		temporaryTableIsolation(s)
		s.Exec(s.C, "SET search_path TO public,pg_temp", 0)
		fixture.TemporaryTableRows(s, s.C, "isolated", 7)
		fixture.TemporaryTableRows(s, s.C, "pg_temp.isolated", 11)
		s.Utility("CREATE TABLE pg_temp.alias(v INT)", nil)
		s.Exec(s.C, "INSERT INTO pg_temp.alias VALUES(31)", 1)
		fixture.TemporaryTableRows(s, s.C, "pg_temp.alias", 31)
		s.Exec(s.C, "SET search_path TO pg_temp", 0)
		own := fixture.TempReferenceNamespace(s.T, s, false)
		s.Utility("CREATE TABLE "+own+".actual(v INT)", nil)
		s.Utility("CREATE TABLE path_owned(v INT)", nil)
		s.Utility("DROP TABLE pg_temp.alias,pg_temp.actual,pg_temp.path_owned", nil)
	})
}

func temporaryTableCommit(s *fixture.TempReferenceSession) {
	s.Utility("CREATE TEMP TABLE preserved(v INT) ON COMMIT PRESERVE ROWS", nil)
	s.Exec(s.C, "INSERT INTO preserved VALUES(11)", 1)
	s.Exec(s.C, "BEGIN", 0)
	s.Exec(s.C, "INSERT INTO preserved VALUES(12)", 1)
	fixture.TemporaryTableRows(s, s.C, "preserved", 11, 12)
	s.Exec(s.C, "COMMIT", 0)
	fixture.TemporaryTableRows(s, s.C, "preserved", 11, 12)
	s.Exec(s.C, "BEGIN", 0)
	s.Exec(s.C, "INSERT INTO preserved VALUES(13)", 1)
	s.Exec(s.C, "ROLLBACK", 0)
	fixture.TemporaryTableRows(s, s.C, "preserved", 11, 12)
	s.Utility("CREATE TEMP TABLE cleared(v INT UNIQUE) ON COMMIT DELETE ROWS", nil)
	s.Exec(s.C, "INSERT INTO cleared VALUES(9)", 1)
	fixture.TemporaryTableRows(s, s.C, "cleared")
	s.Exec(s.C, "BEGIN", 0)
	s.Exec(s.C, "INSERT INTO cleared VALUES(1),(2)", 2)
	fixture.TemporaryTableRows(s, s.C, "cleared", 1, 2)
	s.Exec(s.C, "COMMIT", 0)
	fixture.TemporaryTableRows(s, s.C, "cleared")
	s.Exec(s.C, "BEGIN", 0)
	s.Exec(s.C, "INSERT INTO cleared VALUES(1)", 1)
	s.Exec(s.C, "COMMIT AND CHAIN", 0)
	fixture.TemporaryTableRows(s, s.C, "cleared")
	s.Exec(s.C, "ROLLBACK", 0)
	s.Utility("CREATE TEMP TABLE instant(v INT) ON COMMIT DROP", nil)
	s.Missing(s.C, "instant", "v", "15")
	s.Exec(s.C, "BEGIN", 0)
	s.Utility("CREATE TEMP TABLE dropped(v INT) ON COMMIT DROP", nil)
	s.Exec(s.C, "INSERT INTO dropped VALUES(1)", 1)
	fixture.TemporaryTableRows(s, s.C, "dropped", 1)
	s.Exec(s.C, "COMMIT", 0)
	s.Missing(s.C, "dropped", "v", "15")
	tx, err := s.C.BeginTx(s.P.Ctx, nil)
	if err != nil {
		s.T.Fatal(err)
	}
	s.Exec(tx, "INSERT INTO preserved VALUES(14)", 1)
	if err := tx.Commit(); err != nil {
		s.T.Fatal(err)
	}
	fixture.TemporaryTableRows(s, s.C, "preserved", 11, 12, 14)
}

func TestReferenceTemporaryTableCommit(t *testing.T) {
	fixture.TemporaryTableScenario(t, temporaryTableCommit)
}

func TestReferenceTemporaryTableRollback(t *testing.T) {
	fixture.TemporaryTableScenario(t, func(s *fixture.TempReferenceSession) {
		s.Exec(s.C, "BEGIN", 0)
		s.Utility("CREATE TEMP TABLE undone(v INT)", nil)
		s.Exec(s.C, "INSERT INTO undone VALUES(1)", 1)
		s.Exec(s.C, "ROLLBACK", 0)
		s.Missing(s.C, "undone", "v", "15")
		s.Utility("CREATE TEMP TABLE kept(v INT NOT NULL)", nil)
		s.Exec(s.C, "INSERT INTO kept VALUES(7)", 1)
		s.Exec(s.C, "BEGIN", 0)
		s.Exec(s.C, "SAVEPOINT mark", 0)
		s.Utility("DROP TABLE kept", nil)
		s.Utility("CREATE TEMP TABLE after_mark(v INT) ON COMMIT DROP", nil)
		s.Exec(s.C, "ROLLBACK TO mark", 0)
		fixture.TemporaryTableRows(s, s.C, "kept", 7)
		s.Missing(s.C, "after_mark", "v", "15")
		s.Exec(s.C, "ROLLBACK TO mark", 0)
		s.Exec(s.C, "COMMIT", 0)
		fixture.TemporaryTableRows(s, s.C, "kept", 7)
		d := fixture.TempReferenceDiagnostic("23502", `null value in column "v" of relation "kept" violates not-null constraint`, "Failing row contains (null).", "")
		d.Schema, d.Table, d.Column = "pg_temp", "kept", "v"
		// Namespace identity is generated; discover it rather than pinning allocator output.
		s.Exec(s.C, "SET search_path TO pg_temp", 0)
		d.Schema = fixture.TempReferenceNamespace(s.T, s, false)
		fixture.RunStep(s.T, s.P.Ctx, s.C, s.Record, fixture.Step{Query: "INSERT INTO kept VALUES(8),(NULL)", State: d.Code, Diagnostic: d, ErrorStage: "execute"})
		fixture.TemporaryTableRows(s, s.C, "kept", 7)
	})
}

func TestReferenceTemporaryTableClose(t *testing.T) {
	fixture.TemporaryTableScenario(t, func(s *fixture.TempReferenceSession) {
		temporaryTableIsolation(s)
		if err := s.C.Close(); err != nil {
			s.T.Fatal(err)
		}
		borrowed, err := s.DB.Conn(s.P.Ctx)
		if err != nil {
			s.T.Fatal(err)
		}
		s.C = borrowed
		defer borrowed.Close()
		fixture.TemporaryTableRows(s, s.C, "isolated", 11)
		s.Exec(s.C, "BEGIN", 0)
		s.Utility("CREATE TEMP TABLE private_close(v INT)", nil)
		s.Exec(s.C, "INSERT INTO isolated VALUES(12)", 1)
		s.Retire()
		fixture.TemporaryTableRows(s, s.Peer, "isolated", 22)
		fixture.TemporaryTableRows(s, s.Peer, "public.isolated", 7)
		c, err := s.DB.Conn(s.P.Ctx)
		if err != nil {
			s.T.Fatal(err)
		}
		defer c.Close()
		fixture.TemporaryTableRows(s, c, "isolated", 7)
		s.Missing(c, "private_close", "v", "15")
	})
}
