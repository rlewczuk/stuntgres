package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePredefinedAllData(t *testing.T) {
	predefinedCases(t, []string{"pdef_actor", "pdef_writer", "pdef_plain", "pdef_leaf"}, func(s *membershipScenario, db *sql.DB) {
		predefinedDataSetup(s)
		s.actor("pdef_actor")
		predefinedFailure(s, "SELECT id,v FROM pdef_ad.t ORDER BY id", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema pdef_ad", Position: "18"}, true, "prepare")
		s.actor("")
		s.exec("GRANT pg_read_all_data TO pdef_actor")
		s.actor("pdef_actor")
		for _, target := range []string{"pdef_ad.t", "pdef_ad.v", "pdef_ad.mv"} {
			predefinedRows(s, target, predefinedInitialRows())
		}
		predefinedDenied(s, "INSERT INTO pdef_ad.t VALUES(3,'c')", "permission denied for table t", false)
		predefinedFailure(s, "CREATE TABLE pdef_ad.denied(id INT)", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema pdef_ad", Position: "14"}, false, "execute")
		predefinedDenied(s, "ALTER TABLE pdef_ad.t ADD COLUMN denied INT", "must be owner of table t", false)
		predefinedDenied(s, "DROP TABLE pdef_ad.t", "must be owner of table t", false)
		predefinedDenied(s, "REINDEX TABLE pdef_ad.t", "permission denied for table t", false)
		s.clearNotices()
		s.exec("GRANT SELECT ON pdef_ad.t TO pdef_plain")
		s.notice("WARNING", "01007", `no privileges were granted for "t"`)
		s.actor("")
		// A column ACL never subtracts the predefined global SELECT right.
		s.exec("GRANT SELECT(id) ON pdef_ad.t TO pdef_actor")
		s.exec("REVOKE SELECT(id) ON pdef_ad.t FROM pdef_actor")
		s.actor("pdef_actor")
		predefinedRows(s, "pdef_ad.t", predefinedInitialRows())
		s.actor("pdef_plain")
		predefinedFailure(s, "SELECT id,v FROM pdef_ad.t ORDER BY id", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema pdef_ad", Position: "18"}, true, "prepare")
		s.actor("")
		s.exec("GRANT pg_write_all_data TO pdef_writer")
		s.actor("pdef_writer")
		predefinedDenied(s, "SELECT id,v FROM pdef_ad.t ORDER BY id", "permission denied for table t", true)
		for _, q := range []string{"UPDATE pdef_ad.t SET v='bad' WHERE id=1", "DELETE FROM pdef_ad.t WHERE id=1"} {
			predefinedDenied(s, q, "permission denied for table t", false)
		}
		if s.backend.PG {
			predefinedDenied(s, "INSERT INTO pdef_ad.t VALUES(9,'bad') RETURNING id", "permission denied for table t", false)
		} else {
			predefinedFailure(s, "INSERT INTO pdef_ad.t VALUES(9,'bad') RETURNING id", fixture.Diagnostic{Code: "0A000", Message: "INSERT suffix is unsupported", Position: "39"}, false, "prepare")
		}
		s.actor("")
		predefinedRows(s, "pdef_ad.t", predefinedInitialRows())
		s.actor("pdef_writer")
		s.run(fixture.Exec("INSERT INTO pdef_ad.t VALUES(3,'c')", 1))
		s.actor("")
		predefinedRows(s, "pdef_ad.t", [][]any{{int64(1), "a"}, {int64(2), "b"}, {int64(3), "c"}})
		s.run(fixture.Exec("DELETE FROM pdef_ad.t WHERE id=3", 1))
		s.actor("pdef_writer")
		s.run(fixture.Exec("UPDATE pdef_ad.t SET v='z'", 2))
		s.actor("")
		predefinedRows(s, "pdef_ad.t", [][]any{{int64(1), "z"}, {int64(2), "z"}})
		s.actor("pdef_writer")
		s.run(fixture.Exec("DELETE FROM pdef_ad.t", 2))
		s.actor("")
		predefinedRows(s, "pdef_ad.t", [][]any{})
		s.run(fixture.Exec("INSERT INTO pdef_ad.t VALUES(1,'a'),(2,'b')", 2))
		s.exec("GRANT pg_read_all_data TO pdef_writer")
		s.actor("pdef_writer")
		s.run(fixture.Select("UPDATE pdef_ad.t SET v='x' WHERE id=1 RETURNING id,v", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}}))
		predefinedRows(s, "pdef_ad.t", [][]any{{int64(1), "x"}, {int64(2), "b"}})
		s.run(fixture.Exec("DELETE FROM pdef_ad.t WHERE id=2", 1))
		predefinedRows(s, "pdef_ad.t", [][]any{{int64(1), "x"}})
		predefinedDenied(s, "DROP TABLE pdef_ad.t", "must be owner of table t", false)
		s.actor("")
		// Distinguish all-INHERIT capability paths from all-SET paths.
		s.exec("GRANT pg_read_all_data TO pdef_leaf WITH INHERIT FALSE, SET TRUE")
		s.actor("pdef_leaf")
		predefinedFailure(s, "SELECT id,v FROM pdef_ad.t ORDER BY id", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema pdef_ad", Position: "18"}, true, "prepare")
		s.exec("SET ROLE pg_read_all_data")
		predefinedRows(s, "pdef_ad.t", [][]any{{int64(1), "x"}})
		s.actor("")
		s.exec("GRANT pg_read_all_data TO pdef_leaf WITH INHERIT TRUE, SET FALSE")
		s.actor("pdef_leaf")
		predefinedRows(s, "pdef_ad.t", [][]any{{int64(1), "x"}})
		predefinedDenied(s, "SET ROLE pg_read_all_data", `permission denied to set role "pg_read_all_data"`, false)
		s.actor("")
	})
}

func TestReferencePredefinedSequences(t *testing.T) {
	predefinedCases(t, []string{"pdef_actor", "pdef_writer"}, func(s *membershipScenario, db *sql.DB) {
		predefinedDataSetup(s)
		sequence := func(q, label string, n int64) {
			predefinedSelect(s, q, []string{label}, []string{"INT8"}, [][]any{{n}})
		}
		sequence("SELECT nextval('pdef_ad.s')", "nextval", 10)
		s.exec("GRANT pg_read_all_data TO pdef_actor")
		s.exec("GRANT pg_write_all_data TO pdef_writer")
		s.actor("pdef_actor")
		predefinedSelect(s, "SELECT last_value,is_called FROM pdef_ad.s", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(10), true}})
		sequence("SELECT currval('pdef_ad.s')", "currval", 10)
		for _, q := range []string{"SELECT nextval('pdef_ad.s')", "SELECT setval('pdef_ad.s',99,true)"} {
			predefinedDenied(s, q, "permission denied for sequence s", true)
		}
		s.actor("pdef_writer")
		sequence("SELECT nextval('pdef_ad.s')", "nextval", 11)
		sequence("SELECT setval('pdef_ad.s',20,true)", "setval", 20)
		sequence("SELECT nextval('pdef_ad.s')", "nextval", 21)
		predefinedDenied(s, "SELECT currval('pdef_ad.s')", "permission denied for sequence s", true)
		predefinedDenied(s, "SELECT last_value FROM pdef_ad.s", "permission denied for sequence s", true)
		s.actor("")
		s.exec("GRANT pg_read_all_data TO pdef_writer")
		s.actor("pdef_writer")
		sequence("SELECT nextval('pdef_ad.s')", "nextval", 22)
		sequence("SELECT currval('pdef_ad.s')", "currval", 22)
		s.actor("")
		peer, err := db.Conn(s.p.Ctx)
		if err != nil {
			s.t.Fatal(err)
		}
		defer peer.Close()
		fixture.RunStep(s.t, s.p.Ctx, peer, s.record, fixture.Exec("SET SESSION AUTHORIZATION pdef_actor", 0))
		step := fixture.RoleReferenceError("SELECT currval('pdef_ad.s')", "55000", `currval of sequence "s" is not yet defined in this session`, "", s.prepared, "execute")
		step.SelectRows = true
		fixture.RunStep(s.t, s.p.Ctx, peer, s.record, step)
		s.actor("pdef_actor")
		held := predefinedHeld(s, "SELECT last_value FROM pdef_ad.s")
		s.actor("")
		s.exec("REVOKE pg_read_all_data FROM pdef_actor")
		// Retain independent namespace access so this specifically tests sequence SELECT.
		s.exec("GRANT pg_write_all_data TO pdef_actor")
		s.actor("pdef_actor")
		failure := fixture.RoleReferenceError(held.SQL, "42501", "permission denied for sequence s", "", false, "execute")
		failure.SelectRows = true
		fixture.RunStep(s.t, s.p.Ctx, held, s.record, failure)
		s.actor("")
		predefinedSelect(s, "SELECT last_value,is_called FROM pdef_ad.s", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(22), true}})
		s.exec("GRANT pg_read_all_data TO pdef_actor")
		s.actor("pdef_actor")
		fixture.RunStep(s.t, s.p.Ctx, held, s.record, fixture.Select(held.SQL, []string{"last_value"}, []string{"INT8"}, [][]any{{int64(22)}}))
		s.actor("")
		s.exec("GRANT pg_read_all_data TO pdef_actor WITH INHERIT FALSE, SET TRUE")
		s.actor("pdef_actor")
		predefinedDenied(s, "SELECT last_value FROM pdef_ad.s", "permission denied for sequence s", true)
		s.exec("SET ROLE pg_read_all_data")
		sequence("SELECT currval('pdef_ad.s')", "currval", 22)
		s.actor("")
		s.exec("GRANT pg_read_all_data TO pdef_actor WITH INHERIT TRUE, SET FALSE")
		s.actor("pdef_actor")
		sequence("SELECT currval('pdef_ad.s')", "currval", 22)
		predefinedDenied(s, "SET ROLE pg_read_all_data", `permission denied to set role "pg_read_all_data"`, false)
		s.actor("")
	})
}

func TestReferencePredefinedDataFeatureSmoke(t *testing.T) {
	predefinedCases(t, []string{"pdef_actor"}, func(s *membershipScenario, db *sql.DB) {
		predefinedDataSetup(s)
		held := predefinedHeld(s, "SELECT id,v FROM pdef_ad.t ORDER BY id")
		s.actor("pdef_actor")
		predefinedFailure(s, "SELECT id,v FROM pdef_ad.t ORDER BY id", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema pdef_ad", Position: "18"}, true, "prepare")
		s.actor("")
		s.exec("GRANT pg_read_all_data,pg_write_all_data TO pdef_actor")
		s.actor("pdef_actor")
		fixture.RunStep(s.t, s.p.Ctx, held, s.record, fixture.Select(held.SQL, []string{"id", "v"}, []string{"INT4", "TEXT"}, predefinedInitialRows()))
		s.run(fixture.Exec("INSERT INTO pdef_ad.t VALUES(3,'c')", 1))
		s.run(fixture.Exec("UPDATE pdef_ad.t SET v='x' WHERE id=1", 1))
		predefinedSelect(s, "SELECT nextval('pdef_ad.s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(10)}})
		predefinedDenied(s, "DROP TABLE pdef_ad.t", "must be owner of table t", false)
		s.actor("")
		s.exec("REVOKE pg_read_all_data FROM pdef_actor")
		s.actor("pdef_actor")
		failure := fixture.RoleReferenceError(held.SQL, "42501", "permission denied for table t", "", false, "execute")
		failure.SelectRows = true
		fixture.RunStep(s.t, s.p.Ctx, held, s.record, failure)
		predefinedSelect(s, "SELECT nextval('pdef_ad.s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(11)}})
		s.actor("")
		s.exec("GRANT pg_read_all_data TO pdef_actor")
		s.actor("pdef_actor")
		predefinedRows(s, "pdef_ad.t", [][]any{{int64(1), "x"}, {int64(2), "b"}, {int64(3), "c"}})
		s.actor("")
	})
}
