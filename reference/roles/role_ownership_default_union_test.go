package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

// REL_18_0 shdepDropOwned queues creator addresses before later recipient
// normalization can delete the same record. The bounded oracle defect below
// is not native unsupported behavior: native must publish the complete union.
func TestReferenceRoleOwnershipDefaultCreatorRecipientUnion(t *testing.T) {
	for _, mode := range []string{"recipient_first", "owner_first_residual", "owner_first_baseline"} {
		t.Run(mode, func(t *testing.T) {
			ownershipCases(t, func(s *fixture.RoleMembershipScenario) {
				ownershipDefaultSchemas(s)
				ownershipDefaultTable(s, "own_union_root", "own_source")
				s.Exec("GRANT SELECT ON own_union_root TO own_target")
				s.Actor("own_source")
				fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "own_source", func() { s.Exec("CREATE SEQUENCE own_union_clock START 40") })
				ownershipSelect(s, "SELECT nextval('own_union_clock') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(40)}})
				s.Actor("")
				s.Actor("own_other")
				fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "own_other", func() { s.Exec("CREATE TABLE own_union_sentinel(id INT)") })
				s.Run(fixture.Exec("INSERT INTO own_union_sentinel VALUES(9)", 1))
				s.Actor("")
				ownershipDefaultTable(s, "own_union_membership", "own_middle")
				// Source inherits the actual probe owner; this is independently usable
				// membership provenance, not a fabricated catalog projection.
				s.Actor("own_source")
				ownershipRows(s, "own_union_membership")
				s.Actor("")
				ownershipDefault(s, "own_source", "", "GRANT SELECT ON TABLES TO own_target WITH GRANT OPTION")
				if mode == "owner_first_residual" {
					ownershipDefault(s, "own_source", "", "GRANT INSERT ON TABLES TO own_reader")
				}
				ownershipDefault(s, "own_alt", "", "GRANT SELECT ON TABLES TO own_reader")
				ownershipDefault(s, "own_alt", "own_bulk", "GRANT SELECT ON TABLES TO own_reader")
				unrelatedIDs := ownershipDefaultIDs(s, "own_alt", 2)
				sourceIDs := ownershipDefaultIDs(s, "own_source", 1)
				ownershipDefaultKeyCount(s, "own_source", "", "r", 1)
				var defaultOID int64
				if s.Backend.PG {
					// Match the failing address to its actual creator/global/relation key,
					// rather than an allocator literal or a native synthetic catalog OID.
					if err := s.C.QueryRowContext(s.P.Ctx, "SELECT d.oid::BIGINT FROM pg_default_acl d JOIN pg_roles r ON r.oid=d.defaclrole WHERE r.rolname='own_source' AND d.defaclnamespace=0 AND d.defaclobjtype='r'").Scan(&defaultOID); err != nil {
						s.T.Fatal(err)
					}
					if len(sourceIDs) != 1 || defaultOID != sourceIDs[0] || defaultOID <= 0 {
						s.T.Fatal("default key identity", defaultOID, sourceIDs)
					}
				}
				var tableID, clockID, sentinelID, middleID int64
				if err := s.C.QueryRowContext(s.P.Ctx, "SELECT 'own_union_root'::regclass::oid::BIGINT,'own_union_clock'::regclass::oid::BIGINT,'own_union_sentinel'::regclass::oid::BIGINT,'own_union_membership'::regclass::oid::BIGINT").Scan(&tableID, &clockID, &sentinelID, &middleID); err != nil || tableID <= 0 || clockID <= 0 || sentinelID <= 0 || middleID <= 0 {
					s.T.Fatal("union identities", tableID, clockID, sentinelID, middleID, err)
				}
				var sourceRoleID, targetRoleID int64
				if err := s.C.QueryRowContext(s.P.Ctx, "SELECT oid::BIGINT FROM pg_roles WHERE rolname='own_source'").Scan(&sourceRoleID); err != nil || sourceRoleID <= 0 {
					s.T.Fatal("source role identity", sourceRoleID, err)
				}
				if err := s.C.QueryRowContext(s.P.Ctx, "SELECT oid::BIGINT FROM pg_roles WHERE rolname='own_target'").Scan(&targetRoleID); err != nil || targetRoleID <= 0 || targetRoleID == sourceRoleID {
					s.T.Fatal("recipient role identity", targetRoleID, err)
				}
				q := "DROP OWNED BY own_source,own_target RESTRICT"
				if mode == "recipient_first" {
					q = "DROP OWNED BY own_target,own_target,own_source,own_source RESTRICT"
				}
				if mode == "owner_first_residual" {
					q = "DROP OWNED BY own_source,own_target,own_source,own_target RESTRICT"
				}
				s.ClearNotices()
				if mode == "owner_first_baseline" && s.Backend.PG {
					// Provisional complete REL_18_0 diagnostic, root captures the full fields.
					// The OID is the server's actual record for the independently named key.
					ownershipFailure(s, q, fixture.Diagnostic{Code: "XX000", Message: fmt.Sprintf("could not find tuple for default ACL %d", defaultOID)})
					ownershipNotices(s)
					ownershipSelect(s, "SELECT oid::BIGINT AS oid,rolname FROM pg_roles WHERE rolname='own_source' OR rolname='own_target' ORDER BY rolname", []string{"oid", "rolname"}, []string{"INT8", "NAME"}, [][]any{{sourceRoleID, "own_source"}, {targetRoleID, "own_target"}})
					ownershipOwner(s, "own_union_root", "own_source")
					ownershipRows(s, "own_union_root")
					ownershipOwner(s, "own_union_clock", "own_source")
					ownershipSelect(s, "SELECT 'own_union_root'::regclass::oid::BIGINT AS root,'own_union_clock'::regclass::oid::BIGINT AS clock", []string{"root", "clock"}, []string{"INT8", "INT8"}, [][]any{{tableID, clockID}})
					ownershipDefaultStable(s, "own_source", sourceIDs)
					ownershipDefaultKeyCount(s, "own_source", "", "r", 1)
					ownershipRoleDependency(s, "own_source", "owner of table own_union_root", "owner of sequence own_union_clock", ownershipDefaultLine("own_source", "", "relations", true))
					ownershipRoleDependency(s, "own_target", "privileges for table own_union_root", ownershipDefaultLine("own_source", "", "relations", false))
					s.Actor("own_target")
					ownershipRows(s, "own_union_root")
					s.Actor("")
					ownershipDefaultProjection(s, "table_name='own_union_root' AND grantee='own_target'", [][]any{{"own_source", "own_target", "public", "own_union_root", "SELECT", "NO", "YES"}})
					s.Actor("own_source")
					ownershipRows(s, "own_union_membership")
					ownershipSelect(s, "SELECT currval('own_union_clock') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(40)}})
					ownershipSelect(s, "SELECT nextval('own_union_clock') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(41)}})
					s.Actor("")
					ownershipDefaultTable(s, "own_union_after_rollback", "own_source")
					s.Actor("own_target")
					ownershipRows(s, "own_union_after_rollback")
					s.Actor("")
					ownershipDefaultProjection(s, "table_name='own_union_after_rollback' AND grantee='own_target'", [][]any{{"own_source", "own_target", "public", "own_union_after_rollback", "SELECT", "YES", "YES"}})
					s.Actor("own_target")
					s.Exec("GRANT SELECT ON own_union_after_rollback TO own_downstream")
					s.Actor("own_downstream")
					ownershipRows(s, "own_union_after_rollback")
					s.Actor("")
				} else {
					s.Exec(q)
					ownershipNotices(s)
					ownershipCount(s, "SELECT count(*) FROM pg_class WHERE relname='own_union_root' OR relname='own_union_clock'", 0)
					ownershipDefaultKeyCount(s, "own_source", "", "r", 0)
					s.Exec("DROP ROLE own_source,own_target")
					s.Exec("CREATE ROLE own_source LOGIN")
					s.Exec("CREATE ROLE own_target LOGIN")
					ownershipDefaultTable(s, "own_union_after_cleanup", "own_source")
					s.Actor("own_target")
					ownershipDefaultDenied(s, "own_union_after_cleanup")
					s.Actor("own_reader")
					ownershipDefaultDenied(s, "own_union_after_cleanup")
					s.Actor("")
				}
				ownershipDefaultStable(s, "own_alt", unrelatedIDs)
				ownershipDefaultKeyCount(s, "own_alt", "", "r", 1)
				ownershipDefaultKeyCount(s, "own_alt", "own_bulk", "r", 1)
				ownershipDefaultTable(s, "own_bulk.own_union_unrelated_future", "own_alt")
				s.Actor("own_reader")
				ownershipRows(s, "own_bulk.own_union_unrelated_future")
				s.Actor("")
				ownershipOwner(s, "own_union_sentinel", "own_other")
				ownershipOwner(s, "own_union_membership", "own_middle")
				ownershipSelect(s, "SELECT 'own_union_sentinel'::regclass::oid::BIGINT AS sentinel,'own_union_membership'::regclass::oid::BIGINT AS membership", []string{"sentinel", "membership"}, []string{"INT8", "INT8"}, [][]any{{sentinelID, middleID}})
				ownershipSelect(s, "SELECT id FROM own_union_sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}})
				ownershipRows(s, "own_union_membership")
			})
		})
	}
}
