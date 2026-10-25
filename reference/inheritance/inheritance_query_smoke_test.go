package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInheritanceQueryFeatureSmoke(t *testing.T) {
	inheritanceScenario(t, func(s *inheritanceCase) {
		// Freeze both scan modes before the diamond exists.
		s.exec("CREATE TABLE p(a INT,x TEXT)", 0)
		s.exec("CREATE VIEW default_view AS SELECT * FROM p", 0)
		s.exec("CREATE VIEW only_view AS SELECT * FROM ONLY p", 0)
		s.exec("CREATE VIEW only_alias AS SELECT r.x AS label FROM ONLY(p) r", 0)
		s.exec("CREATE TABLE q(y BOOL,a INT)", 0)
		s.exec("CREATE TABLE l() INHERITS(p)", 0)
		s.exec("CREATE TABLE r() INHERITS(p)", 0)
		s.exec("CREATE TABLE c(z BIGINT) INHERITS(q,l,r)", 0)
		s.exec("INSERT INTO p VALUES(7,'same'),(7,'same')", 2)
		s.exec("INSERT INTO l VALUES(7,'same')", 1)
		s.exec("INSERT INTO c VALUES(true,7,'same',9),(true,7,'same',10)", 2)
		for _, check := range inheritanceQueryControlChecks() {
			s.run(check)
		}
		star := fixture.Select("SELECT * FROM p*", []string{"a", "x"}, []string{"INT4", "TEXT"}, fixture.InheritanceQueryFive())
		star.Bag = true
		s.run(star)
		peer, err := s.b.DB.Conn(s.p.Ctx)
		if err != nil {
			s.t.Fatal(err)
		}
		defer peer.Close()
		s.exec("BEGIN", 0)
		defer s.c.ExecContext(s.p.Ctx, "ROLLBACK")
		s.values("SELECT a FROM p WHERE x='same' FOR UPDATE", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}, {int64(7)}, {int64(7)}, {int64(7)}})
		fixture.QueryLockNowait(s.t, s.p, s.b, peer, "SELECT a AS id FROM c WHERE z=9 FOR UPDATE NOWAIT", s.prepared, "c")
		s.exec("ROLLBACK", 0)
		// Explicit ACLs remain an honest native Dump boundary even after REVOKE.
		// Prove parent authority on a disposable real hierarchy, then retire it;
		// the original diamond/data/views remain unchanged for genuine controls.
		s.exec("CREATE TABLE auth_parent(a INT,x TEXT)", 0)
		s.exec("CREATE TABLE auth_child(z BIGINT) INHERITS(q,auth_parent)", 0)
		s.exec("INSERT INTO auth_parent VALUES(7,'same'),(7,'same')", 2)
		s.exec("INSERT INTO auth_child VALUES(true,7,'same',9),(true,7,'same',9),(true,7,'same',9)", 3)
		authority := fixture.Select("SELECT * FROM auth_parent*", []string{"a", "x"}, []string{"INT4", "TEXT"}, fixture.InheritanceQueryFive())
		authority.Bag = true
		s.exec("CREATE ROLE inherit_query_smoke NOSUPERUSER NOBYPASSRLS", 0)
		alive := true
		s.t.Cleanup(func() {
			if alive && s.b.PG {
				fixture.Admin.ExecContext(s.p.Ctx, "DROP OWNED BY inherit_query_smoke CASCADE")
				fixture.Admin.ExecContext(s.p.Ctx, "DROP ROLE inherit_query_smoke")
			}
		})
		if s.b.PG {
			s.exec("GRANT USAGE ON SCHEMA public TO inherit_query_smoke", 0)
		}
		s.exec("GRANT SELECT ON auth_parent TO inherit_query_smoke", 0)
		s.exec("SET ROLE inherit_query_smoke", 0)
		s.run(authority)
		inheritanceQueryDenied(s, "SELECT a FROM auth_child", "auth_child")
		s.exec("SET ROLE NONE", 0)
		s.exec("ALTER TABLE auth_parent ENABLE ROW LEVEL SECURITY", 0)
		s.exec("SET ROLE inherit_query_smoke", 0)
		s.values("SELECT * FROM auth_parent", []string{"a", "x"}, []string{"INT4", "TEXT"}, nil)
		s.exec("SET ROLE NONE", 0)
		s.exec("ALTER TABLE auth_parent DISABLE ROW LEVEL SECURITY", 0)
		s.exec("REVOKE SELECT ON auth_parent FROM inherit_query_smoke", 0)
		s.exec("DROP TABLE auth_parent CASCADE", 0)
		if s.b.PG {
			s.exec("REVOKE USAGE ON SCHEMA public FROM inherit_query_smoke", 0)
		}
		s.exec("DROP ROLE inherit_query_smoke", 0)
		alive = false
		inheritanceQueryControlRoundTrip(s)
	})
}
