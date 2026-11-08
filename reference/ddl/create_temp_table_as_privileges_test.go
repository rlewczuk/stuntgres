package reference_test

import (
	"context"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCreateTempTableAsPrivileges(t *testing.T) {
	tempAsScenario(t, func(s *fixture.TempReferenceSession) {
		var database string
		if err := s.C.QueryRowContext(s.P.Ctx, "SELECT current_database()").Scan(&database); err != nil {
			s.T.Fatal(err)
		}
		quoted := `"` + strings.ReplaceAll(database, `"`, `""`) + `"`
		var publicTemp bool
		if err := s.C.QueryRowContext(s.P.Ctx, "SELECT EXISTS(SELECT 1 FROM pg_database d CROSS JOIN LATERAL aclexplode(COALESCE(d.datacl,acldefault('d',d.datdba))) a WHERE d.datname=current_database() AND a.grantee=0 AND a.privilege_type='TEMPORARY')").Scan(&publicTemp); err != nil {
			s.T.Fatal(err)
		}
		tempAsExec(s, s.C, "CREATE ROLE ta_denied", 0)
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
			defer cancel()
			for _, q := range []string{"RESET ROLE", "DROP OWNED BY ta_denied", "DROP ROLE ta_denied"} {
				if _, err := s.C.ExecContext(ctx, q); err != nil {
					s.T.Error(q, err)
				}
			}
			if publicTemp {
				if _, err := s.C.ExecContext(ctx, "GRANT TEMPORARY ON DATABASE "+quoted+" TO PUBLIC"); err != nil {
					s.T.Error(err)
				}
			}
		}()
		tempAsExec(s, s.C, "REVOKE TEMPORARY ON DATABASE "+quoted+" FROM PUBLIC", 0)
		tempAsExec(s, s.C, "SET ROLE ta_denied", 0)
		tempAsError(s, s.C, "CREATE TEMP TABLE ta_denied_temp AS SELECT 1 AS v", "42501", "permission denied to create temporary tables in database \""+database+"\"", "", false)
		tempAsError(s, s.C, "CREATE TABLE public.ta_denied_regular AS SELECT 1 AS v", "42501", "permission denied for schema public", "", false)
		tempAsExec(s, s.C, "RESET ROLE", 0)
		tempAsAbsent(s, "ta_denied_temp")
		tempAsAbsent(s, "ta_denied_regular")
	})
}
