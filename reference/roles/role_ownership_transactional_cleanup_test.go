package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleOwnershipTransactionalCleanup(t *testing.T) {
	ownershipTransactionCases(t, func(s *fixture.RoleMembershipScenario, route string) {
		ownershipSeed(s, "tx_cleanup_root", "own_source")
		ownershipSeed(s, "tx_cleanup_grants", "own_other")
		ownershipSeed(s, "tx_cleanup_policy_probe", "own_other")
		for _, q := range []string{
			"CREATE VIEW tx_cleanup_view AS SELECT id FROM tx_cleanup_root",
			"CREATE VIEW tx_cleanup_nested AS SELECT id FROM tx_cleanup_view",
			"ALTER VIEW tx_cleanup_view OWNER TO own_other",
			"ALTER VIEW tx_cleanup_nested OWNER TO own_other",
			"GRANT SELECT,UPDATE ON tx_cleanup_grants TO own_source WITH GRANT OPTION",
			"GRANT SELECT(id),UPDATE(id) ON tx_cleanup_grants TO own_source WITH GRANT OPTION",
			"GRANT SELECT ON tx_cleanup_grants TO own_alt WITH GRANT OPTION",
			"CREATE POLICY tx_policy_mixed ON tx_cleanup_grants TO own_source,own_reader,own_source USING(id>0)",
			"CREATE POLICY tx_policy_last ON tx_cleanup_grants TO own_source USING(id>0)",
			"GRANT SELECT ON tx_cleanup_policy_probe TO PUBLIC",
			"CREATE POLICY tx_probe_mixed ON tx_cleanup_policy_probe TO own_source,own_reader,own_source USING(id>0)",
			"CREATE POLICY tx_probe_last ON tx_cleanup_policy_probe TO own_source USING(id>0)",
			"ALTER TABLE tx_cleanup_policy_probe ENABLE ROW LEVEL SECURITY,FORCE ROW LEVEL SECURITY",
			"GRANT own_middle TO own_source WITH ADMIN TRUE",
			"GRANT own_middle TO own_downstream WITH INHERIT TRUE GRANTED BY own_source",
			"ALTER DEFAULT PRIVILEGES FOR ROLE own_source GRANT SELECT ON TABLES TO own_reader",
			"ALTER DEFAULT PRIVILEGES FOR ROLE own_other GRANT SELECT ON TABLES TO own_source,own_alt",
		} {
			s.Exec(q)
		}
		s.Actor("own_source")
		s.Exec("GRANT SELECT,UPDATE ON tx_cleanup_grants TO own_downstream")
		s.Exec("GRANT SELECT(id),UPDATE(id) ON tx_cleanup_grants TO own_reader")
		s.Actor("own_alt")
		s.Exec("GRANT SELECT ON tx_cleanup_grants TO own_downstream")
		// Establish real delegated UPDATE authority before cleanup, without
		// leaving a peer-blocking row write inside any later explicit route.
		s.Actor("own_downstream")
		s.Run(fixture.Exec("UPDATE tx_cleanup_grants SET id=id WHERE id=7", 1))
		s.Actor("")
		const acl = "SELECT c.oid::BIGINT,c.relacl::TEXT FROM pg_class c WHERE c.relname IN('tx_cleanup_root','tx_cleanup_view','tx_cleanup_nested','tx_cleanup_grants') ORDER BY c.relname"
		const col = "SELECT attnum,attacl::TEXT FROM pg_attribute WHERE attrelid='tx_cleanup_grants'::regclass AND attnum>0 ORDER BY attnum"
		const def = "SELECT d.oid::BIGINT,d.defaclrole::BIGINT,d.defaclacl::TEXT FROM pg_default_acl d JOIN pg_roles r ON r.oid=d.defaclrole WHERE r.rolname IN('own_source','own_other') ORDER BY d.oid"
		const member = "SELECT pg_has_role('own_downstream','own_middle','MEMBER') AS member,pg_has_role('own_downstream','own_middle','USAGE') AS usage,pg_has_role('own_downstream','own_middle','SET') AS can_set"
		const policy = "SELECT policyname,roles::TEXT FROM pg_policies WHERE tablename='tx_cleanup_grants' ORDER BY policyname"
		const probePolicies = "SELECT policyname,roles::TEXT FROM pg_policies WHERE tablename='tx_cleanup_policy_probe' ORDER BY policyname"
		before := map[string][][]any{}
		for _, q := range []string{acl, col, def, member, policy, probePolicies} {
			before[q] = ownershipTransactionSnapshot(s, s.C, q)
		}
		if len(before[acl]) != 4 || len(before[col]) != 1 || len(before[def]) != 2 || len(before[member]) != 1 || len(before[policy]) != 2 || len(before[probePolicies]) != 2 {
			s.T.Fatal("complete cleanup preconditions", before)
		}
		ownershipSelect(s, member, []string{"member", "usage", "can_set"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}})
		peer, err := s.Backend.DB.Conn(s.P.Ctx)
		if err != nil {
			s.T.Fatal(err)
		}
		defer peer.Close()
		policyState := func(e fixture.Executor, cleaned bool) {
			s.T.Helper()
			run := func(step fixture.Step) { fixture.RunStep(s.T, s.P.Ctx, e, s.Record, step) }
			// A successful empty SELECT proves the independently granted SELECT
			// survives: missing ACL authority would produce 42501, not no rows.
			for _, role := range []string{"own_source", "own_reader", "own_alt"} {
				run(fixture.Exec("SET ROLE "+role, 0))
				var want [][]any
				if role == "own_reader" || role == "own_source" && !cleaned {
					want = [][]any{{int64(7)}}
				}
				run(fixture.Select("SELECT id FROM tx_cleanup_policy_probe", []string{"id"}, []string{"INT4"}, want))
				run(fixture.Exec("RESET ROLE", 0))
			}
		}
		grantState := func(e fixture.Executor, cleaned, inside bool) {
			s.T.Helper()
			run := func(step fixture.Step) { fixture.RunStep(s.T, s.P.Ctx, e, s.Record, step) }
			deny := func(query string, selectRows bool) {
				if inside {
					run(fixture.Exec("SAVEPOINT cleanup_acl_probe", 0))
				}
				step := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for table tx_cleanup_grants"})
				step.SelectRows, step.ErrorStage = selectRows, "execute"
				run(step)
				if inside {
					run(fixture.ImplicitError("SELECT 1", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
					run(fixture.Exec("ROLLBACK TO cleanup_acl_probe", 0))
					run(fixture.Exec("RELEASE cleanup_acl_probe", 0))
				}
			}
			run(fixture.Exec("SET ROLE own_source", 0))
			if cleaned {
				deny("SELECT id FROM tx_cleanup_grants", true)
			} else {
				run(fixture.Select("SELECT id FROM tx_cleanup_grants", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			}
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("SET ROLE own_downstream", 0))
			run(fixture.Select("SELECT id FROM tx_cleanup_grants", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			if cleaned {
				deny("UPDATE tx_cleanup_grants SET id=id WHERE id=7", false)
			}
			run(fixture.Exec("RESET ROLE", 0))
		}
		policyState(s.C, false)
		grantState(s.C, false, false)
		cascade := "DROP OWNED BY own_source,own_source CASCADE"
		for _, commit := range []bool{false, true} {
			if route == "auto" && !commit {
				continue
			}
			x := ownershipBegin(s, route, cascade, "DROP OWNED BY own_source RESTRICT", "DROP OWNED BY own_source,own_absent")
			defer x.abort()
			if route != "auto" {
				x.exec("SAVEPOINT healthy")
			}
			missing := fixture.ImplicitError("DROP OWNED BY own_source,own_absent", fixture.Diagnostic{Code: "42704", Message: `role "own_absent" does not exist`})
			missing.ErrorStage = "execute"
			x.run(missing)
			if route != "auto" {
				x.run(fixture.ImplicitError("SELECT 1", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
				x.exec("ROLLBACK TO healthy")
			}
			for q, rows := range before {
				ownershipTransactionSnapshotEqual(s, x.e, q, rows)
			}
			policyState(x.e, false)
			grantState(x.e, false, route != "auto")
			restrict := fixture.ImplicitError("DROP OWNED BY own_source RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop desired object(s) because other objects depend on them", Detail: "view tx_cleanup_view depends on table tx_cleanup_root\nview tx_cleanup_nested depends on view tx_cleanup_view", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
			restrict.ErrorStage = "execute"
			x.run(restrict)
			if route != "auto" {
				x.exec("ROLLBACK TO healthy")
			}
			for q, rows := range before {
				ownershipTransactionSnapshotEqual(s, x.e, q, rows)
			}
			policyState(x.e, false)
			grantState(x.e, false, route != "auto")
			s.ClearNotices()
			x.exec(cascade)
			x.run(fixture.Select(member, []string{"member", "usage", "can_set"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{false, false, false}}))
			ownershipNotices(s, ownershipCascadeNotice("drop cascades to 2 other objects", "drop cascades to view tx_cleanup_view\ndrop cascades to view tx_cleanup_nested"))
			x.run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname IN('tx_cleanup_root','tx_cleanup_view','tx_cleanup_nested')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			x.run(fixture.Select("SELECT id FROM tx_cleanup_grants", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			x.run(fixture.Select("SELECT pg_has_role('own_downstream','own_middle','MEMBER') AS inherited", []string{"inherited"}, []string{"BOOL"}, [][]any{{false}}))
			grantState(x.e, true, route != "auto")
			policyState(x.e, true)
			x.run(fixture.Select(policy, []string{"policyname", "roles"}, []string{"NAME", "TEXT"}, [][]any{{"tx_policy_mixed", "{own_reader}"}}))
			x.run(fixture.Select("SELECT count(*) FROM pg_default_acl d JOIN pg_roles r ON r.oid=d.defaclrole WHERE r.rolname='own_source'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			if route != "auto" {
				for q, rows := range before {
					ownershipTransactionSnapshotEqual(s, peer, q, rows)
				}
				// Sole-target policy deletion holds the surviving tables AX.
				// The old peer catalogs above remain readable; real row reads
				// resume after release (queued reads are covered by PolicyLocks).
				x.exec("ROLLBACK TO healthy")
				policyState(peer, false)
				grantState(peer, false, false)
				for q, rows := range before {
					ownershipTransactionSnapshotEqual(s, x.e, q, rows)
				}
				policyState(x.e, false)
				grantState(x.e, false, true)
				x.exec("ROLLBACK TO healthy")
				x.exec("RELEASE healthy")
				s.ClearNotices()
				x.exec(cascade)
				s.ClearNotices()
				policyState(x.e, true)
				grantState(x.e, true, true)
			}
			x.end(commit)
			if route != "auto" {
				policyState(peer, commit)
				grantState(peer, commit, false)
			}
			if !commit {
				for q, rows := range before {
					ownershipTransactionSnapshotEqual(s, s.C, q, rows)
				}
				policyState(s.C, false)
				grantState(s.C, false, false)
				ownershipRoleDependency(s, "own_source", "owner of table tx_cleanup_root", "privileges for table tx_cleanup_grants", "privileges for column id of table tx_cleanup_grants", "target of policy tx_policy_mixed on table tx_cleanup_grants", "target of policy tx_policy_mixed on table tx_cleanup_grants", "target of policy tx_policy_last on table tx_cleanup_grants", "target of policy tx_probe_mixed on table tx_cleanup_policy_probe", "target of policy tx_probe_mixed on table tx_cleanup_policy_probe", "target of policy tx_probe_last on table tx_cleanup_policy_probe", "privileges for membership of role own_downstream in role own_middle", ownershipDefaultLine("own_source", "", "relations", true), ownershipDefaultLine("own_other", "", "relations", false))
			}
			if commit {
				for _, target := range []struct{ name, table string }{{"tx_policy_last", "tx_cleanup_grants"}, {"tx_probe_last", "tx_cleanup_policy_probe"}} {
					ownershipFailure(s, "ALTER POLICY "+target.name+" ON "+target.table, fixture.Diagnostic{Code: "42704", Message: `policy "` + target.name + `" for table "` + target.table + `" does not exist`})
				}
				policyState(s.C, true)
				grantState(s.C, true, false)
			}
		}
		ownershipCount(s, "SELECT count(*) FROM pg_namespace WHERE nspname='public'", 1)
		ownershipCount(s, "SELECT count(*) FROM pg_database WHERE datname=current_database()", 1)
		s.Exec("DROP ROLE own_source")
		ownershipRows(s, "tx_cleanup_grants")
	})
}

func TestReferenceRoleOwnershipTransactionalClosure(t *testing.T) {
	ownershipTransactionCases(t, func(s *fixture.RoleMembershipScenario, route string) {
		for _, q := range []string{
			"CREATE TABLE tx_closure_parent(id INT PRIMARY KEY)", "INSERT INTO tx_closure_parent VALUES(7)",
			"CREATE TABLE tx_closure_fk(id INT REFERENCES tx_closure_parent(id))",
			"CREATE TYPE tx_closure_enum AS ENUM('a')",
			"CREATE TABLE tx_closure_column(id INT,value tx_closure_enum)",
			"CREATE TYPE tx_closure_composite AS(id INT)",
			"CREATE TABLE tx_closure_typed OF tx_closure_composite",
			"ALTER TABLE tx_closure_parent OWNER TO own_source",
			"ALTER TYPE tx_closure_enum OWNER TO own_source",
			"ALTER TYPE tx_closure_composite OWNER TO own_source",
			"ALTER TABLE tx_closure_fk OWNER TO own_other",
			"ALTER TABLE tx_closure_column OWNER TO own_other",
			"ALTER TABLE tx_closure_typed OWNER TO own_other",
		} {
			if q == "INSERT INTO tx_closure_parent VALUES(7)" {
				s.Run(fixture.Exec(q, 1))
			} else {
				s.Exec(q)
			}
		}
		s.Run(fixture.Exec("INSERT INTO tx_closure_fk VALUES(7)", 1))
		s.Run(fixture.Exec("INSERT INTO tx_closure_column VALUES(7,'a')", 1))
		s.Run(fixture.Exec("INSERT INTO tx_closure_typed VALUES(7)", 1))
		var fkID, colID int64
		if err := s.C.QueryRowContext(s.P.Ctx, "SELECT 'tx_closure_fk'::regclass::oid::BIGINT,'tx_closure_column'::regclass::oid::BIGINT").Scan(&fkID, &colID); err != nil || fkID <= 0 || colID <= 0 {
			s.T.Fatal(fkID, colID, err)
		}
		for _, commit := range []bool{false, true} {
			if route == "auto" && !commit {
				continue
			}
			x := ownershipBegin(s, route, "DROP OWNED BY own_source CASCADE")
			defer x.abort()
			x.exec("DROP OWNED BY own_source CASCADE")
			x.run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname IN('tx_closure_parent','tx_closure_typed','tx_closure_composite')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			x.run(fixture.Select("SELECT count(*) FROM pg_constraint WHERE conrelid='tx_closure_fk'::regclass AND contype='f'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			x.run(fixture.Select("SELECT attnum,attname FROM pg_attribute WHERE attrelid='tx_closure_column'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attnum", "attname"}, []string{"INT2", "NAME"}, [][]any{{int64(1), "id"}}))
			for _, name := range []string{"tx_closure_fk", "tx_closure_column"} {
				x.run(fixture.Select("SELECT id FROM "+name, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			}
			x.end(commit)
			if !commit {
				ownershipRows(s, "tx_closure_typed")
				ownershipCount(s, "SELECT count(*) FROM pg_constraint WHERE conrelid='tx_closure_fk'::regclass AND contype='f'", 1)
			}
		}
		ownershipSelect(s, "SELECT 'tx_closure_fk'::regclass::oid::BIGINT AS fk,'tx_closure_column'::regclass::oid::BIGINT AS col", []string{"fk", "col"}, []string{"INT8", "INT8"}, [][]any{{fkID, colID}})
	})
}
