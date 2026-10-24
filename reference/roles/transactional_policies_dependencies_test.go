package reference_test

import (
	"context"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func (s *transactionalPolicies) edges(oid int64, provider string, present bool) {
	s.t.Helper()
	count := int64(0)
	if present {
		count = 1
	}
	s.run(fixture.Select(fmt.Sprintf("SELECT count(*) FROM pg_depend WHERE classid=3256 AND objid=%d AND refclassid=1259 AND refobjid='%s'::regclass AND deptype='n'", oid, provider), []string{"count"}, []string{"INT8"}, [][]any{{count}}))
	s.run(fixture.Select(fmt.Sprintf("SELECT count(*) FROM pg_depend WHERE classid=3256 AND objid=%d AND refclassid=1259 AND refobjid='t'::regclass AND deptype='a'", oid), []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
}

func TestReferenceTransactionalPoliciesDependencies(t *testing.T) {
	transactionalPolicyMatrix(t, []string{"sql", "go"}, func(s *transactionalPolicies, mode string) {
		s.exec("CREATE TABLE helper_a(id INT)")
		s.exec("CREATE TABLE helper_b(id INT)")
		s.run(fixture.Exec("INSERT INTO helper_a VALUES(1)", 1))
		s.run(fixture.Exec("INSERT INTO helper_b VALUES(2)", 1))
		s.exec("GRANT SELECT ON helper_a,helper_b TO tp_reader")
		s.exec("CREATE POLICY p ON t TO tp_reader USING(EXISTS(SELECT 1 FROM helper_a WHERE helper_a.id=t.id))")
		s.exec("CREATE POLICY p ON u TO tp_reader USING(TRUE)")
		oid, other := s.oid("t", "p"), s.oid("u", "p")
		if oid == other {
			s.t.Fatal("table-local policy identities aliased")
		}
		s.edges(oid, "helper_a", true)
		s.edges(oid, "helper_b", false)
		s.ids(1)
		finish := s.begin(mode)
		s.ddl("ALTER POLICY p ON t USING(EXISTS(SELECT 1 FROM helper_b WHERE helper_b.id=t.id))")
		s.ddl("ALTER POLICY p ON t RENAME TO renamed")
		s.sameOID("t", "renamed", oid)
		s.sameOID("u", "p", other)
		s.edges(oid, "helper_a", false)
		s.edges(oid, "helper_b", true)
		s.ids(2)
		s.exec("SAVEPOINT renamed_name")
		s.failure("ALTER POLICY p ON t", "42704", `policy "p" for table "t" does not exist`)
		s.exec("ROLLBACK TO renamed_name")
		// pg_depend is a catalog-row snapshot, not a fresh table executor: it
		// remains committed while the table's retained AX blocks peer SELECT.
		fixture.RunStep(s.t, s.p.Ctx, s.peer, s.b.Recorder, fixture.Select(fmt.Sprintf("SELECT count(*) FROM pg_depend WHERE classid=3256 AND objid=%d AND refobjid='helper_a'::regclass AND deptype='n'", oid), []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
		s.exec("SAVEPOINT dependency_failure")
		s.run(fixture.ImplicitError("DROP TABLE helper_b RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop table helper_b because other objects depend on it", Detail: "policy renamed on table t depends on table helper_b", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}))
		s.exec("ROLLBACK TO dependency_failure")
		s.exec("DROP TABLE helper_b CASCADE")
		s.ids()
		s.exec("ROLLBACK TO dependency_failure")
		s.sameOID("t", "renamed", oid)
		s.ids(2)
		finish(false)
		s.sameOID("t", "p", oid)
		s.edges(oid, "helper_a", true)
		s.edges(oid, "helper_b", false)
		s.ids(1)
		finish = s.begin(mode)
		s.ddl("ALTER POLICY p ON t USING(EXISTS(SELECT 1 FROM helper_b WHERE helper_b.id=t.id))")
		finish(true)
		s.sameOID("t", "p", oid)
		s.edges(oid, "helper_a", false)
		s.edges(oid, "helper_b", true)
		s.ids(2)
		// Schema grants are setup, not part of the policy transaction scope.
		// PUBLIC usage also covers the role created privately below.
		s.exec("GRANT USAGE ON SCHEMA public TO PUBLIC")
		s.t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if _, err := s.b.DB.ExecContext(ctx, "ALTER DEFAULT PRIVILEGES REVOKE SELECT ON TABLES FROM tp_reader"); err != nil {
				s.t.Error(err)
			}
		})
		s.exec("ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO tp_reader")
		finish = s.begin(mode)
		s.exec("CREATE ROLE tp_private IN ROLE tp_reader")
		s.exec("CREATE TABLE private_provider(id INT)")
		s.run(fixture.Exec("INSERT INTO private_provider VALUES(3)", 1))
		s.ddl("ALTER POLICY p ON t TO tp_private USING(EXISTS(SELECT 1 FROM private_provider WHERE private_provider.id=t.id))")
		s.run(fixture.Exec("INSERT INTO u VALUES(3,TRUE)", 1))
		s.ddl("CREATE POLICY private_role_policy ON u TO tp_private USING(EXISTS(SELECT 1 FROM private_provider WHERE private_provider.id=u.id))")
		s.oid("u", "private_role_policy")
		s.exec("SET ROLE tp_private")
		s.run(fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}}))
		s.run(fixture.Select("SELECT id FROM u ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}}))
		s.exec("RESET ROLE")
		fixture.RunStep(s.t, s.p.Ctx, s.peer, s.b.Recorder, fixture.Select("SELECT count(*) FROM pg_roles WHERE rolname='tp_private'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		finish(false)
		s.sameOID("t", "p", oid)
		s.ids(2)
	})
}
