package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// Complete diagnostics and failed-list atomicity were observed on PostgreSQL
// 18.6 UTF8/C. Only the family identity is relational, not a fixed user OID.
func TestReferenceOperatorFamilySecondaryUniqueness(t *testing.T) {
	familyScenario(t, func(t *testing.T, ctx context.Context, conn *sql.Conn, run func(fixture.Step)) {
		for _, prepared := range []bool{false, true} {
			for _, list := range []bool{false, true} {
				name := fmt.Sprintf("family_secondary_%t_%t", prepared, list)
				run(fixture.Exec("CREATE OPERATOR FAMILY public."+name+" USING gist", 0))
				var familyID int64
				if err := conn.QueryRowContext(ctx, `SELECT oid::bigint FROM pg_opfamily WHERE opfname=$1 AND opfnamespace='public'::regnamespace AND opfmethod=783`, name).Scan(&familyID); err != nil {
					t.Fatal(err)
				}
				if familyID == 0 {
					t.Fatal("family has zero identity")
				}
				query := "ALTER OPERATOR FAMILY public." + name + " USING gist ADD OPERATOR 2 pg_catalog.=(integer,integer)"
				var memberID int64
				if list {
					query = "ALTER OPERATOR FAMILY public." + name + " USING gist ADD OPERATOR 1 pg_catalog.=(integer,integer), OPERATOR 2 pg_catalog.=(integer,integer)"
				} else {
					run(fixture.Exec("ALTER OPERATOR FAMILY public."+name+" USING gist ADD OPERATOR 1 pg_catalog.=(integer,integer)", 0))
					if err := conn.QueryRowContext(ctx, `SELECT oid::bigint FROM pg_amop WHERE amopfamily=$1`, familyID).Scan(&memberID); err != nil {
						t.Fatal(err)
					}
					if memberID == 0 {
						t.Fatal("member has zero identity")
					}
				}
				step := fixture.ImplicitError(query, fixture.Diagnostic{
					Code: "23505", Message: `duplicate key value violates unique constraint "pg_amop_opr_fam_index"`,
					Detail: fmt.Sprintf("Key (amopopr, amoppurpose, amopfamily)=(96, s, %d) already exists.", familyID),
					Schema: "pg_catalog", Table: "pg_amop", Constraint: "pg_amop_opr_fam_index",
				})
				if prepared {
					step = fixture.Prepared(step)
				}
				run(step)
				var want [][]any
				if !list {
					want = [][]any{{memberID, familyID, int64(23), int64(23), int64(1), "s", int64(96), int64(783), int64(0)}}
				}
				run(fixture.Select(`SELECT oid::bigint,amopfamily::bigint,amoplefttype::bigint,amoprighttype::bigint,amopstrategy,amoppurpose,amopopr::bigint,amopmethod::bigint,amopsortfamily::bigint FROM pg_amop WHERE amopfamily=$1 ORDER BY amopstrategy`,
					[]string{"oid", "amopfamily", "amoplefttype", "amoprighttype", "amopstrategy", "amoppurpose", "amopopr", "amopmethod", "amopsortfamily"},
					[]string{"INT8", "INT8", "INT8", "INT8", "INT2", "CHAR", "INT8", "INT8", "INT8"}, want, familyID))
				run(fixture.Select(`SELECT 42 AS sentinel`, []string{"sentinel"}, []string{"INT4"}, [][]any{{int64(42)}}))
			}
		}
		// Identical operator/purpose in a distinct family is legal. This also
		// proves the failed list did not poison the secondary lookup index.
		run(fixture.Exec(`CREATE OPERATOR FAMILY public.family_secondary_control USING gist`, 0))
		run(fixture.Exec(`ALTER OPERATOR FAMILY public.family_secondary_control USING gist ADD OPERATOR 1 pg_catalog.=(integer,integer)`, 0))
		run(fixture.Select(`SELECT a.amopstrategy,a.amopopr::bigint FROM pg_amop a JOIN pg_opfamily f ON f.oid=a.amopfamily WHERE f.opfname='family_secondary_control' AND f.opfnamespace='public'::regnamespace`, []string{"amopstrategy", "amopopr"}, []string{"INT2", "INT8"}, [][]any{{int64(1), int64(96)}}))
	})
}
