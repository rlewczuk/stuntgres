package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"testing"

	"github.com/lib/pq"
	"stuntgres"
	"stuntgres/reference/fixture"
)

// Schema authority is actual all-INHERIT owner membership, not an unavailable
// schema/type GRANT route. Source and consumer remain independent owner roots.
func ownershipTypedSetup(s *membershipScenario) {
	s.exec("CREATE SCHEMA own_schema AUTHORIZATION own_other")
	for _, role := range []string{"own_source", "own_target", "own_reader", "own_actor", "own_middle"} {
		s.exec("GRANT own_other TO " + role + " WITH INHERIT TRUE, SET FALSE")
	}
	s.exec("SET search_path TO own_schema,public")
	s.actor("own_source")
	s.exec("CREATE TYPE own_pair AS(id INT)")
	s.actor("own_target")
	s.exec("CREATE TABLE own_typed OF own_pair(id DEFAULT 7 NOT NULL,CONSTRAINT own_positive CHECK(id>0))")
	s.exec("CREATE INDEX own_typed_index ON own_typed(id)")
	s.run(fixture.Exec("INSERT INTO own_typed DEFAULT VALUES", 1))
	s.actor("")
}

func ownershipTypedState(s *membershipScenario, source, table string) {
	s.t.Helper()
	ownershipOwner(s, "own_pair", source)
	ownershipOwner(s, "own_typed", table)
	ownershipOwner(s, "own_typed_index", table)
	ownershipRows(s, "own_typed")
	ownershipSelect(s, "SELECT reloftype='own_pair'::regtype::oid AS attached FROM pg_class WHERE oid='own_typed'::regclass::oid", []string{"attached"}, []string{"BOOL"}, [][]any{{true}})
	ownershipSelect(s, "SELECT 'own_typed'::regtype::oid<>'own_pair'::regtype::oid AS distinct_row", []string{"distinct_row"}, []string{"BOOL"}, [][]any{{true}})
	ownershipSelect(s, "SELECT deptype FROM pg_depend WHERE classid='pg_class'::regclass::oid AND objid='own_typed'::regclass::oid AND objsubid=0 AND refclassid=1247::oid AND refobjid='own_pair'::regtype::oid AND refobjsubid=0", []string{"deptype"}, []string{"CHAR"}, [][]any{{"n"}})
}

func TestReferenceRoleOwnershipTypedTables(t *testing.T) {
	ownershipCases(t, func(s *membershipScenario) {
		ownershipTypedSetup(s)
		ownershipTypedState(s, "own_source", "own_target")
		var sourceID, tableID, rowID, indexID int64
		if err := s.c.QueryRowContext(s.p.Ctx, "SELECT 'own_pair'::regtype::oid::BIGINT,'own_typed'::regclass::oid::BIGINT,'own_typed'::regtype::oid::BIGINT,'own_typed_index'::regclass::oid::BIGINT").Scan(&sourceID, &tableID, &rowID, &indexID); err != nil || sourceID <= 0 || tableID <= 0 || rowID <= 0 || indexID <= 0 || rowID == sourceID {
			s.t.Fatal("typed identities", sourceID, tableID, rowID, indexID, err)
		}
		stable := func() {
			ownershipSelect(s, "SELECT 'own_pair'::regtype::oid::BIGINT AS source,'own_typed'::regclass::oid::BIGINT AS consumer,'own_typed'::regtype::oid::BIGINT AS rowtype,'own_typed_index'::regclass::oid::BIGINT AS index", []string{"source", "consumer", "rowtype", "index"}, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{sourceID, tableID, rowID, indexID}})
		}
		s.exec("ALTER TYPE own_pair OWNER TO own_actor")
		ownershipTypedState(s, "own_actor", "own_target")
		stable()
		s.exec("ALTER TABLE own_typed OWNER TO own_reader")
		ownershipTypedState(s, "own_actor", "own_reader")
		stable()
		s.exec("REASSIGN OWNED BY own_actor TO own_source")
		ownershipTypedState(s, "own_source", "own_reader")
		stable()
		s.exec("REASSIGN OWNED BY own_reader TO own_middle")
		ownershipTypedState(s, "own_source", "own_middle")
		stable()
		// Controls are an actual native API, not a fictitious PostgreSQL API.
		if !s.backend.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &dump); err != nil {
				s.t.Fatal(err)
			}
			if err := stuntgres.Reset(s.p.Ctx, s.p.Native); err != nil {
				s.t.Fatal(err)
			}
			if err := stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				s.t.Fatal(err)
			}
			ownershipTypedState(s, "own_source", "own_middle")
		}
		s.actor("own_middle")
		s.run(fixture.Exec("INSERT INTO own_typed DEFAULT VALUES", 1))
		ownershipSelect(s, "SELECT id FROM own_typed ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}})
		ownershipFailure(s, "INSERT INTO own_typed VALUES(0)", fixture.Diagnostic{Code: "23514", Message: `new row for relation "own_typed" violates check constraint "own_positive"`, Detail: "Failing row contains (0).", Schema: "own_schema", Table: "own_typed", Constraint: "own_positive"})
		s.actor("")
	})
}

func TestReferenceRoleOwnershipTypedDependencyCleanup(t *testing.T) {
	for _, union := range []bool{false, true} {
		t.Run(fmt.Sprintf("union=%t", union), func(t *testing.T) {
			ownershipProviderCases(t, func(s *membershipScenario, database string) {
				ownershipTypedSetup(s)
				s.clearNotices()
				ownershipFailure(s, "DROP OWNED BY own_source RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop type own_pair because other objects depend on it", Detail: "table own_typed depends on type own_pair", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
				ownershipNotices(s)
				ownershipTypedState(s, "own_source", "own_target")
				ownershipSeed(s, "own_early", "own_source")
				// Namespace ownership is not sentinel ownership: the doomed grant
				// holder must not retain implicit owner grant options after cleanup.
				s.exec("GRANT own_other TO own_alt WITH INHERIT TRUE, SET FALSE")
				s.actor("own_alt")
				s.exec("CREATE TABLE own_sentinel(id INT)")
				s.run(fixture.Exec("INSERT INTO own_sentinel VALUES(9)", 1))
				s.exec("GRANT SELECT ON own_sentinel TO own_source WITH GRANT OPTION")
				s.actor("own_source")
				s.exec("GRANT SELECT ON own_sentinel TO own_downstream")
				s.actor("")
				s.exec("GRANT own_other TO own_downstream WITH INHERIT TRUE, SET FALSE")
				s.exec("GRANT SELECT ON own_typed TO own_reader")
				s.exec("GRANT CREATE ON DATABASE " + pq.QuoteIdentifier(database) + " TO own_source WITH GRANT OPTION")
				s.exec("GRANT own_middle TO own_source WITH ADMIN TRUE")
				s.exec("GRANT own_middle TO own_downstream GRANTED BY own_source")
				s.exec("CREATE POLICY own_typed_keep ON own_sentinel TO own_source USING(id>0)")
				s.exec("ALTER TABLE own_sentinel ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY")
				s.actor("own_source")
				ownershipPolicyRows(s, "SELECT id FROM own_sentinel", []string{"id"}, [][]any{{int64(9)}})
				s.actor("")
				var sourceID, tableID, rowID int64
				if err := s.c.QueryRowContext(s.p.Ctx, "SELECT 'own_pair'::regtype::oid::BIGINT,'own_typed'::regclass::oid::BIGINT,'own_typed'::regtype::oid::BIGINT").Scan(&sourceID, &tableID, &rowID); err != nil || sourceID <= 0 || tableID <= 0 || rowID <= 0 || sourceID == rowID {
					s.t.Fatal("cleanup identities", sourceID, tableID, rowID, err)
				}
				s.clearNotices()
				ownershipFailure(s, "DROP OWNED BY own_source RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop desired object(s) because other objects depend on them", Detail: "table own_typed depends on type own_pair", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
				ownershipNotices(s)
				ownershipTypedState(s, "own_source", "own_target")
				ownershipSelect(s, "SELECT 'own_pair'::regtype::oid::BIGINT AS source,'own_typed'::regclass::oid::BIGINT AS consumer,'own_typed'::regtype::oid::BIGINT AS rowtype", []string{"source", "consumer", "rowtype"}, []string{"INT8", "INT8", "INT8"}, [][]any{{sourceID, tableID, rowID}})
				ownershipRows(s, "own_early")
				s.actor("own_reader")
				ownershipRows(s, "own_typed")
				s.actor("own_source")
				ownershipPolicyRows(s, "SELECT id FROM own_sentinel", []string{"id"}, [][]any{{int64(9)}})
				// Retained DB CREATE is exercised by an actual grant-option operation.
				s.exec("GRANT CREATE ON DATABASE " + pq.QuoteIdentifier(database) + " TO own_actor")
				s.exec("REVOKE CREATE ON DATABASE " + pq.QuoteIdentifier(database) + " FROM own_actor")
				s.actor("")
				s.exec("ALTER TABLE own_sentinel DISABLE ROW LEVEL SECURITY")
				s.actor("own_downstream")
				ownershipSelect(s, "SELECT id FROM own_sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}})
				s.actor("")
				ownershipInherited(s, "own_downstream", "own_middle", "MEMBER", true)
				s.clearNotices()
				if union {
					s.exec("DROP OWNED BY own_source,own_target RESTRICT")
					ownershipNotices(s)
				} else {
					s.exec("DROP OWNED BY own_source CASCADE")
					ownershipNotices(s, ownershipCascadeNotice("drop cascades to table own_typed", ""))
				}
				ownershipCount(s, "SELECT count(*) FROM pg_class WHERE relname='own_pair' OR relname='own_typed' OR relname='own_typed_index' OR relname='own_early'", 0)
				ownershipCount(s, fmt.Sprintf("SELECT count(*) FROM pg_depend WHERE (classid=1259::oid AND objid=%d::oid) OR (refclassid=1259::oid AND refobjid=%d::oid) OR (classid=1247::oid AND (objid=%d::oid OR objid=%d::oid)) OR (refclassid=1247::oid AND (refobjid=%d::oid OR refobjid=%d::oid))", tableID, tableID, sourceID, rowID, sourceID, rowID), 0)
				if s.backend.PG {
					ownershipCount(s, "SELECT count(*) FROM pg_type WHERE typname='own_pair' OR typname='_own_pair' OR typname='own_typed' OR typname='_own_typed'", 0)
				}
				ownershipSelect(s, "SELECT id FROM own_sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}})
				ownershipOwner(s, "own_sentinel", "own_alt")
				ownershipFailure(s, "ALTER POLICY own_typed_keep ON own_sentinel", fixture.Diagnostic{Code: "42704", Message: `policy "own_typed_keep" for table "own_sentinel" does not exist`})
				ownershipInherited(s, "own_downstream", "own_middle", "MEMBER", false)
				for _, role := range []string{"own_source", "own_downstream"} {
					s.actor(role)
					ownershipFailure(s, "SELECT id FROM own_sentinel", fixture.Diagnostic{Code: "42501", Message: "permission denied for table own_sentinel"})
				}
				s.actor("")
				// This must succeed, not become a dynamically learned error golden.
				// Retain every public diagnostic field if cleanup leaves a blocker.
				const dropSource = "DROP ROLE own_source"
				var result sql.Result
				var err error
				if s.prepared {
					stmt, prepareErr := s.c.PrepareContext(s.p.Ctx, dropSource)
					if prepareErr != nil {
						s.t.Fatalf("final role cleanup Prepare: %#v", fixture.ExtractDiagnostic(s.t, prepareErr))
					}
					result, err = stmt.ExecContext(s.p.Ctx)
					if closeErr := stmt.Close(); closeErr != nil {
						s.t.Error(closeErr)
					}
				} else {
					result, err = s.c.ExecContext(s.p.Ctx, dropSource)
				}
				if err != nil {
					s.t.Fatalf("final role cleanup Execute: %#v", fixture.ExtractDiagnostic(s.t, err))
				}
				fixture.LockAffected(s.t, result, 0)
			})
		})
	}
}
