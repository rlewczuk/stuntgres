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
func ownershipTypedSetup(s *fixture.RoleMembershipScenario) {
	s.Exec("CREATE SCHEMA own_schema AUTHORIZATION own_other")
	for _, role := range []string{"own_source", "own_target", "own_reader", "own_actor", "own_middle"} {
		s.Exec("GRANT own_other TO " + role + " WITH INHERIT TRUE, SET FALSE")
	}
	s.Exec("SET search_path TO own_schema,public")
	s.Actor("own_source")
	s.Exec("CREATE TYPE own_pair AS(id INT)")
	s.Actor("own_target")
	s.Exec("CREATE TABLE own_typed OF own_pair(id DEFAULT 7 NOT NULL,CONSTRAINT own_positive CHECK(id>0))")
	s.Exec("CREATE INDEX own_typed_index ON own_typed(id)")
	s.Run(fixture.Exec("INSERT INTO own_typed DEFAULT VALUES", 1))
	s.Actor("")
}

func ownershipTypedState(s *fixture.RoleMembershipScenario, source, table string) {
	s.T.Helper()
	ownershipOwner(s, "own_pair", source)
	ownershipOwner(s, "own_typed", table)
	ownershipOwner(s, "own_typed_index", table)
	ownershipRows(s, "own_typed")
	ownershipSelect(s, "SELECT reloftype='own_pair'::regtype::oid AS attached FROM pg_class WHERE oid='own_typed'::regclass::oid", []string{"attached"}, []string{"BOOL"}, [][]any{{true}})
	ownershipSelect(s, "SELECT 'own_typed'::regtype::oid<>'own_pair'::regtype::oid AS distinct_row", []string{"distinct_row"}, []string{"BOOL"}, [][]any{{true}})
	ownershipSelect(s, "SELECT deptype FROM pg_depend WHERE classid='pg_class'::regclass::oid AND objid='own_typed'::regclass::oid AND objsubid=0 AND refclassid=1247::oid AND refobjid='own_pair'::regtype::oid AND refobjsubid=0", []string{"deptype"}, []string{"CHAR"}, [][]any{{"n"}})
}

func TestReferenceRoleOwnershipTypedTables(t *testing.T) {
	ownershipCases(t, func(s *fixture.RoleMembershipScenario) {
		ownershipTypedSetup(s)
		ownershipTypedState(s, "own_source", "own_target")
		var sourceID, tableID, rowID, indexID int64
		if err := s.C.QueryRowContext(s.P.Ctx, "SELECT 'own_pair'::regtype::oid::BIGINT,'own_typed'::regclass::oid::BIGINT,'own_typed'::regtype::oid::BIGINT,'own_typed_index'::regclass::oid::BIGINT").Scan(&sourceID, &tableID, &rowID, &indexID); err != nil || sourceID <= 0 || tableID <= 0 || rowID <= 0 || indexID <= 0 || rowID == sourceID {
			s.T.Fatal("typed identities", sourceID, tableID, rowID, indexID, err)
		}
		stable := func() {
			ownershipSelect(s, "SELECT 'own_pair'::regtype::oid::BIGINT AS source,'own_typed'::regclass::oid::BIGINT AS consumer,'own_typed'::regtype::oid::BIGINT AS rowtype,'own_typed_index'::regclass::oid::BIGINT AS index", []string{"source", "consumer", "rowtype", "index"}, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{sourceID, tableID, rowID, indexID}})
		}
		s.Exec("ALTER TYPE own_pair OWNER TO own_actor")
		ownershipTypedState(s, "own_actor", "own_target")
		stable()
		s.Exec("ALTER TABLE own_typed OWNER TO own_reader")
		ownershipTypedState(s, "own_actor", "own_reader")
		stable()
		s.Exec("REASSIGN OWNED BY own_actor TO own_source")
		ownershipTypedState(s, "own_source", "own_reader")
		stable()
		s.Exec("REASSIGN OWNED BY own_reader TO own_middle")
		ownershipTypedState(s, "own_source", "own_middle")
		stable()
		// Controls are an actual native API, not a fictitious PostgreSQL API.
		if !s.Backend.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &dump); err != nil {
				s.T.Fatal(err)
			}
			if err := stuntgres.Reset(s.P.Ctx, s.P.Native); err != nil {
				s.T.Fatal(err)
			}
			if err := stuntgres.Restore(s.P.Ctx, s.P.Native, bytes.NewReader(dump.Bytes())); err != nil {
				s.T.Fatal(err)
			}
			ownershipTypedState(s, "own_source", "own_middle")
		}
		s.Actor("own_middle")
		s.Run(fixture.Exec("INSERT INTO own_typed DEFAULT VALUES", 1))
		ownershipSelect(s, "SELECT id FROM own_typed ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}})
		ownershipFailure(s, "INSERT INTO own_typed VALUES(0)", fixture.Diagnostic{Code: "23514", Message: `new row for relation "own_typed" violates check constraint "own_positive"`, Detail: "Failing row contains (0).", Schema: "own_schema", Table: "own_typed", Constraint: "own_positive"})
		s.Actor("")
	})
}

func TestReferenceRoleOwnershipTypedDependencyCleanup(t *testing.T) {
	for _, union := range []bool{false, true} {
		t.Run(fmt.Sprintf("union=%t", union), func(t *testing.T) {
			ownershipProviderCases(t, func(s *fixture.RoleMembershipScenario, database string) {
				ownershipTypedSetup(s)
				s.ClearNotices()
				ownershipFailure(s, "DROP OWNED BY own_source RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop type own_pair because other objects depend on it", Detail: "table own_typed depends on type own_pair", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
				ownershipNotices(s)
				ownershipTypedState(s, "own_source", "own_target")
				ownershipSeed(s, "own_early", "own_source")
				// Namespace ownership is not sentinel ownership: the doomed grant
				// holder must not retain implicit owner grant options after cleanup.
				s.Exec("GRANT own_other TO own_alt WITH INHERIT TRUE, SET FALSE")
				s.Actor("own_alt")
				s.Exec("CREATE TABLE own_sentinel(id INT)")
				s.Run(fixture.Exec("INSERT INTO own_sentinel VALUES(9)", 1))
				s.Exec("GRANT SELECT ON own_sentinel TO own_source WITH GRANT OPTION")
				s.Actor("own_source")
				s.Exec("GRANT SELECT ON own_sentinel TO own_downstream")
				s.Actor("")
				s.Exec("GRANT own_other TO own_downstream WITH INHERIT TRUE, SET FALSE")
				s.Exec("GRANT SELECT ON own_typed TO own_reader")
				s.Exec("GRANT CREATE ON DATABASE " + pq.QuoteIdentifier(database) + " TO own_source WITH GRANT OPTION")
				s.Exec("GRANT own_middle TO own_source WITH ADMIN TRUE")
				s.Exec("GRANT own_middle TO own_downstream GRANTED BY own_source")
				s.Exec("CREATE POLICY own_typed_keep ON own_sentinel TO own_source USING(id>0)")
				s.Exec("ALTER TABLE own_sentinel ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY")
				s.Actor("own_source")
				ownershipPolicyRows(s, "SELECT id FROM own_sentinel", []string{"id"}, [][]any{{int64(9)}})
				s.Actor("")
				var sourceID, tableID, rowID int64
				if err := s.C.QueryRowContext(s.P.Ctx, "SELECT 'own_pair'::regtype::oid::BIGINT,'own_typed'::regclass::oid::BIGINT,'own_typed'::regtype::oid::BIGINT").Scan(&sourceID, &tableID, &rowID); err != nil || sourceID <= 0 || tableID <= 0 || rowID <= 0 || sourceID == rowID {
					s.T.Fatal("cleanup identities", sourceID, tableID, rowID, err)
				}
				s.ClearNotices()
				ownershipFailure(s, "DROP OWNED BY own_source RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop desired object(s) because other objects depend on them", Detail: "table own_typed depends on type own_pair", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
				ownershipNotices(s)
				ownershipTypedState(s, "own_source", "own_target")
				ownershipSelect(s, "SELECT 'own_pair'::regtype::oid::BIGINT AS source,'own_typed'::regclass::oid::BIGINT AS consumer,'own_typed'::regtype::oid::BIGINT AS rowtype", []string{"source", "consumer", "rowtype"}, []string{"INT8", "INT8", "INT8"}, [][]any{{sourceID, tableID, rowID}})
				ownershipRows(s, "own_early")
				s.Actor("own_reader")
				ownershipRows(s, "own_typed")
				s.Actor("own_source")
				ownershipPolicyRows(s, "SELECT id FROM own_sentinel", []string{"id"}, [][]any{{int64(9)}})
				// Retained DB CREATE is exercised by an actual grant-option operation.
				s.Exec("GRANT CREATE ON DATABASE " + pq.QuoteIdentifier(database) + " TO own_actor")
				s.Exec("REVOKE CREATE ON DATABASE " + pq.QuoteIdentifier(database) + " FROM own_actor")
				s.Actor("")
				s.Exec("ALTER TABLE own_sentinel DISABLE ROW LEVEL SECURITY")
				s.Actor("own_downstream")
				ownershipSelect(s, "SELECT id FROM own_sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}})
				s.Actor("")
				ownershipInherited(s, "own_downstream", "own_middle", "MEMBER", true)
				s.ClearNotices()
				if union {
					s.Exec("DROP OWNED BY own_source,own_target RESTRICT")
					ownershipNotices(s)
				} else {
					s.Exec("DROP OWNED BY own_source CASCADE")
					ownershipNotices(s, ownershipCascadeNotice("drop cascades to table own_typed", ""))
				}
				ownershipCount(s, "SELECT count(*) FROM pg_class WHERE relname='own_pair' OR relname='own_typed' OR relname='own_typed_index' OR relname='own_early'", 0)
				ownershipCount(s, fmt.Sprintf("SELECT count(*) FROM pg_depend WHERE (classid=1259::oid AND objid=%d::oid) OR (refclassid=1259::oid AND refobjid=%d::oid) OR (classid=1247::oid AND (objid=%d::oid OR objid=%d::oid)) OR (refclassid=1247::oid AND (refobjid=%d::oid OR refobjid=%d::oid))", tableID, tableID, sourceID, rowID, sourceID, rowID), 0)
				ownershipCount(s, "SELECT count(*) FROM pg_type WHERE typname='own_pair' OR typname='_own_pair' OR typname='own_typed' OR typname='_own_typed'", 0)
				ownershipSelect(s, "SELECT id FROM own_sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}})
				ownershipOwner(s, "own_sentinel", "own_alt")
				ownershipFailure(s, "ALTER POLICY own_typed_keep ON own_sentinel", fixture.Diagnostic{Code: "42704", Message: `policy "own_typed_keep" for table "own_sentinel" does not exist`})
				ownershipInherited(s, "own_downstream", "own_middle", "MEMBER", false)
				for _, role := range []string{"own_source", "own_downstream"} {
					s.Actor(role)
					ownershipFailure(s, "SELECT id FROM own_sentinel", fixture.Diagnostic{Code: "42501", Message: "permission denied for table own_sentinel"})
				}
				s.Actor("")
				// This must succeed, not become a dynamically learned error golden.
				// Retain every public diagnostic field if cleanup leaves a blocker.
				const dropSource = "DROP ROLE own_source"
				var result sql.Result
				var err error
				if s.Prepared {
					stmt, prepareErr := s.C.PrepareContext(s.P.Ctx, dropSource)
					if prepareErr != nil {
						s.T.Fatalf("final role cleanup Prepare: %#v", fixture.ExtractDiagnostic(s.T, prepareErr))
					}
					result, err = stmt.ExecContext(s.P.Ctx)
					if closeErr := stmt.Close(); closeErr != nil {
						s.T.Error(closeErr)
					}
				} else {
					result, err = s.C.ExecContext(s.P.Ctx, dropSource)
				}
				if err != nil {
					s.T.Fatalf("final role cleanup Execute: %#v", fixture.ExtractDiagnostic(s.T, err))
				}
				fixture.LockAffected(s.T, result, 0)
			})
		})
	}
}
