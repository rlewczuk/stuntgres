package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceStatisticsAlterPreCanceled(t *testing.T) {
	withStatisticsAlter(t, func(t *testing.T, s ndistinctScenario) {
		statisticsAlterSetup(s)
		var owner string
		if err := s.conn.QueryRowContext(s.pair.Ctx, "SELECT current_user").Scan(&owner); err != nil {
			t.Fatal(err)
		}
		oid := fixture.DeparseDefinitionID(t, s.conn, "SELECT oid FROM pg_statistic_ext WHERE stxname='sa_before'")
		intact := func() {
			if got := fixture.DeparseDefinitionID(t, s.conn, "SELECT oid FROM pg_statistic_ext WHERE stxname='sa_before'"); got != oid {
				t.Fatal("precanceled command replaced source identity")
			}
			s.run(statisticsAlterMetadata("sa_before", "public", owner, nil))
			s.run(fixture.Select("SELECT d.refclassid::bigint,d.refobjsubid,d.deptype,d.refobjid=CASE WHEN d.refclassid=1259 THEN s.stxrelid ELSE s.stxnamespace END AS reference_link FROM pg_statistic_ext s JOIN pg_depend d ON d.classid=3381 AND d.objid=s.oid WHERE s.oid=$1::oid ORDER BY d.refclassid,d.refobjsubid", []string{"refclassid", "refobjsubid", "deptype", "reference_link"}, []string{"INT8", "INT4", "CHAR", "BOOL"}, [][]any{{int64(1259), int64(1), "a", true}, {int64(1259), int64(2), "a", true}, {int64(2615), int64(0), "n", true}}, oid))
			statisticsAlterFacts(t, s, "sa_before")
			s.run(fixture.Select("SELECT a,b FROM sa_input ORDER BY a,b", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(1), int64(10)}, {int64(2), int64(20)}, {int64(2), int64(20)}}))
		}
		intact()
		// The native API's pre-dispatch context boundary is intentionally distinct
		// from PostgreSQL's admitted server cancellation protocol.
		if !s.pg {
			var before bytes.Buffer
			if err := stuntgres.Dump(s.pair.Ctx, s.pair.Native, &before); err != nil {
				t.Fatal(err)
			}
			var notices []fixture.Diagnostic
			_, worker, _ := fixture.NoticeReferencePool(t, s.pair, false, &notices)
			canceled, cancel := context.WithCancel(s.pair.Ctx)
			cancel()
			for _, action := range []string{"RENAME TO sa_after", "SET SCHEMA sa_destination", "OWNER TO sa_recipient", "SET STATISTICS 10001"} {
				query := "ALTER STATISTICS public.sa_before " + action
				var held *sql.Stmt
				var err error
				if s.prepared {
					held, err = worker.PrepareContext(s.pair.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
				}
				var result sql.Result
				if held != nil {
					result, err = held.ExecContext(canceled)
				} else {
					result, err = worker.ExecContext(canceled, query)
				}
				if held != nil {
					held.Close()
				}
				if result != nil {
					t.Fatal("precanceled ALTER returned a result", action)
				}
				fixture.LexicalOrdinary(t, err, context.Canceled)
				if len(notices) != 0 {
					t.Fatal("precanceled ALTER delivered notices", action, notices)
				}
				intact()
				var after bytes.Buffer
				if err = stuntgres.Dump(s.pair.Ctx, s.pair.Native, &after); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(before.Bytes(), after.Bytes()) {
					t.Fatal("precanceled ALTER changed definitions or collected state", action)
				}
			}
		}
		intact()
	})
}
