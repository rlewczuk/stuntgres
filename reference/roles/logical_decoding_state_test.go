package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceLogicalDecodingState(t *testing.T) {
	logicalDecodingCases(t, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
		s.Exec("CREATE TABLE ld_sentinel(id integer,v text)")
		s.Run(fixture.Exec("INSERT INTO ld_sentinel VALUES(2,'second'),(1,'first')", 2))
		s.Exec("ALTER ROLE ld_repl REPLICATION")
		check := func() {
			predefinedSelect(s, "SELECT id,v FROM ld_sentinel ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "first"}, {int64(2), "second"}})
			predefinedSelect(s, "SELECT rolreplication FROM pg_roles WHERE rolname='ld_repl'", []string{"rolreplication"}, []string{"BOOL"}, [][]any{{true}})
			predefinedSelect(s, "SELECT current_user::text AS actor", []string{"actor"}, []string{"TEXT"}, [][]any{{s.Bootstrap}})
			for _, sig := range logicalDecodingSignatures {
				logicalDecodingIdentity(s, sig)
				predefinedSelect(s, "SELECT * FROM " + sig.name + "('ld_absent',NULL,NULL) LIMIT 0", []string{"lsn", "xid", "data"}, []string{"PG_LSN", "XID", sig.data}, nil)
				for range 2 {
					logicalDecodingFailure(s, "SELECT * FROM " + sig.name + "('ld_absent',NULL,NULL)", logicalDecodingMissing("ld_absent"))
				}
			}
			predefinedSelect(s, "SELECT id,v FROM ld_sentinel ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "first"}, {int64(2), "second"}})
		}
		check()
		if !s.Backend.PG {
			var snapshot bytes.Buffer
			if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &snapshot); err != nil { s.T.Fatal(err) }
			if err := stuntgres.Reset(s.P.Ctx, s.P.Native); err != nil { s.T.Fatal(err) }
			if err := stuntgres.Restore(s.P.Ctx, s.P.Native, bytes.NewReader(snapshot.Bytes())); err != nil { s.T.Fatal(err) }
			check()
		}
	})
}

func TestReferenceLogicalDecodingFeatureSmoke(t *testing.T) {
	logicalDecodingCases(t, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
		for _, sig := range logicalDecodingSignatures {
			logicalDecodingIdentity(s, sig)
			predefinedSelect(s, "SELECT * FROM " + sig.name + "('ld_absent',NULL,NULL) LIMIT 0", []string{"lsn", "xid", "data"}, []string{"PG_LSN", "XID", sig.data}, nil)
			logicalDecodingFailure(s, "SELECT * FROM " + sig.name + "('ld_absent',NULL,NULL,'k')", fixture.Diagnostic{Code: "0A000", Message: "array must have even number of elements"})
			logicalDecodingFailure(s, "SELECT * FROM " + sig.name + "('ld_absent',NULL,NULL,'k','v')", logicalDecodingMissing("ld_absent"))
			held := predefinedHeld(s, "SELECT * FROM " + sig.name + "($1::name,NULL,NULL)")
			logicalDecodingHeldFailure(s, held, logicalDecodingMissing("ld_absent"), "ld_absent")
			s.Exec("SET ROLE ld_actor")
			logicalDecodingHeldFailure(s, held, logicalDecodingDenied(), "ld_absent")
			s.Exec("RESET ROLE")
			logicalDecodingHeldFailure(s, held, logicalDecodingMissing("ld_absent"), "ld_absent")
		}
	})
}
