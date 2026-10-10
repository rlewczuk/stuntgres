package reference_test

import "testing"

func TestReferenceInheritanceSessionCleanup(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.b.db.SetMaxIdleConns(0)
		owner, err := s.b.db.Conn(s.p.ctx)
		if err != nil {
			s.t.Fatal(err)
		}
		defer owner.Close()
		for _, q := range []string{"CREATE TEMP TABLE session_parent(a INT)", "CREATE TEMP TABLE session_child() INHERITS(session_parent)"} {
			step := referenceExec(q, 0)
			step.prepared = s.prepared
			referenceRunStep(s.t, s.p.ctx, owner, s.b.recorder, step)
		}
		step := referenceExec("INSERT INTO session_child VALUES(7)", 1)
		step.prepared = s.prepared
		referenceRunStep(s.t, s.p.ctx, owner, s.b.recorder, step)
		s.values("SELECT count(*) AS objects FROM pg_class WHERE relname='session_parent' OR relname='session_child'", []string{"objects"}, []string{"INT8"}, [][]any{{int64(2)}})
		if err = owner.Close(); err != nil {
			s.t.Fatal(err)
		}
		if s.b.pg {
			// pq sends Terminate without waiting for server teardown. Wait for
			// the actual catalog cleanup under the fixture's bounded context.
			for {
				var count int64
				if err = s.c.QueryRowContext(s.p.ctx, "SELECT count(*) FROM pg_class WHERE relname='session_parent' OR relname='session_child'").Scan(&count); err != nil {
					s.t.Fatal(err)
				}
				if count == 0 {
					break
				}
			}
		}
		s.values("SELECT count(*) AS objects FROM pg_class WHERE relname='session_parent' OR relname='session_child'", []string{"objects"}, []string{"INT8"}, [][]any{{int64(0)}})
	})
}

func TestReferenceInheritanceBoundaryNoEffects(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE SEQUENCE seed START 31", 0)
		s.exec("CREATE TABLE p(a INT)", 0)
		s.exec("CREATE TABLE c() INHERITS(p)", 0)
		s.exec("INSERT INTO p VALUES(1)", 1)
		s.exec("INSERT INTO c VALUES(2)", 1)
		if s.b.pg {
			s.values("SELECT nextval('seed') FROM p ORDER BY a", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(31)}, {int64(32)}})
			s.values("SELECT last_value,is_called FROM seed", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(32), true}})
			s.exec("TRUNCATE p", 0)
			s.values("SELECT * FROM c", []string{"a"}, []string{"INT4"}, nil)
		} else {
			step := referenceImplicitError("SELECT nextval('seed') FROM p WHERE false", referenceDiagnostic{Code: "0A000", Message: "recursive table inheritance is unsupported"})
			step.selectRows = true
			s.run(step)
			s.values("SELECT last_value,is_called FROM seed", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(31), false}})
			s.fail("TRUNCATE p", referenceDiagnostic{Code: "0A000", Message: "statement is unsupported", Position: "1"})
			s.values("SELECT * FROM c", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}})
		}
	})
}

func TestReferenceInheritanceOnCommitDeletePhysical(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		s.exec("CREATE TEMP TABLE commit_parent(a INT) ON COMMIT DELETE ROWS", 0)
		s.exec("CREATE TEMP TABLE commit_child() INHERITS(commit_parent) ON COMMIT PRESERVE ROWS", 0)
		s.exec("BEGIN", 0)
		s.exec("INSERT INTO commit_parent VALUES(1)", 1)
		s.exec("INSERT INTO commit_child VALUES(2)", 1)
		s.exec("COMMIT", 0)
		s.values("SELECT * FROM commit_child", []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}})
		if s.b.pg {
			s.values("SELECT * FROM ONLY commit_parent", []string{"a"}, []string{"INT4"}, nil)
		} else {
			step := referenceImplicitError("SELECT * FROM commit_parent", referenceDiagnostic{Code: "0A000", Message: "recursive table inheritance is unsupported"})
			step.selectRows = true
			s.run(step)
		}
	})
}
