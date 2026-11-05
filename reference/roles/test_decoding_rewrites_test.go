package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func tdRewriteWitness(s *fixture.RoleMembershipScenario) {
	s.T.Helper()
	// A typed volatile default forces the same real destination-row rewrite
	// as the REL_18_6 ddl.out example, without unrelated ADD serial support.
	s.Exec("CREATE TABLE tr_unique(id2 serial UNIQUE NOT NULL,data integer)")
	s.Run(fixture.Exec("INSERT INTO tr_unique(data) VALUES(10)", 1))
	s.Exec("CREATE SEQUENCE td_rewrite_seq")
	tdSlot(s, "td_slot")
	s.Exec("ALTER TABLE tr_unique RENAME TO tr_pkey")
	s.Exec("ALTER TABLE tr_pkey ADD COLUMN id integer DEFAULT nextval('td_rewrite_seq')")
	tdPeek(s, "")
	tdPeek(s, ",'include-rewrites','0'")
	tdPeek(s, ",'include-rewrites','1','include-rewrites','0'")
	tdGet(s, ",'include-rewrites','1'", tdTxn("table public.tr_pkey: INSERT: id2[integer]:1 data[integer]:10 id[integer]:1")...)
	predefinedSelect(s, "SELECT id2,data,id FROM tr_pkey", []string{"id2", "data", "id"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(10), int64(1)}})
}

func TestReferenceTestDecodingRewrites(t *testing.T) {
	t.Run("volatile_default_destination", func(t *testing.T) {
		tdCases(t, func(s *fixture.RoleMembershipScenario, _ *sql.DB) { tdRewriteWitness(s) })
	})
	t.Run("successive_type_snapshots", func(t *testing.T) {
		tdCases(t, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
			s.Exec("CREATE TABLE t(id integer PRIMARY KEY,v integer)")
			s.Run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
			tdSlot(s, "td_slot")
			s.Exec("BEGIN")
			s.Exec("ALTER TABLE t ALTER COLUMN v TYPE text USING ('x'||v::text)")
			s.Run(fixture.Exec("UPDATE t SET v='y'", 1))
			s.Exec("ALTER TABLE t ALTER COLUMN v TYPE integer USING length(v)")
			s.Run(fixture.Exec("INSERT INTO t VALUES(2,20)", 1))
			s.Exec("COMMIT")
			ordinary := tdTxn("table public.t: UPDATE: id[integer]:1 v[text]:'y'", "table public.t: INSERT: id[integer]:2 v[integer]:20")
			tdPeek(s, "", ordinary...)
			tdGet(s, ",'include-rewrites','1'", tdTxn("table public.t: INSERT: id[integer]:1 v[text]:'x10'", "table public.t: UPDATE: id[integer]:1 v[text]:'y'", "table public.t: INSERT: id[integer]:1 v[integer]:1", "table public.t: INSERT: id[integer]:2 v[integer]:20")...)
			predefinedSelect(s, "SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(20)}})
		})
	})
	t.Run("metadata_rollback_failure", func(t *testing.T) {
		tdCases(t, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
			s.Exec("CREATE TABLE t(id integer PRIMARY KEY,v text)")
			s.Run(fixture.Exec("INSERT INTO t VALUES(1,'bad')", 1))
			s.Exec("CREATE SEQUENCE td_rollback_seq")
			tdSlot(s, "td_slot")
			s.Exec("ALTER TABLE t RENAME COLUMN v TO renamed")
			s.Exec("ALTER TABLE t ADD COLUMN fast integer DEFAULT 9")
			tdPeek(s, ",'include-rewrites','1'")
			s.Exec("BEGIN")
			s.Exec("ALTER TABLE t ADD COLUMN undone integer DEFAULT nextval('td_rollback_seq')")
			s.Exec("ROLLBACK")
			s.Exec("BEGIN")
			s.Exec("SAVEPOINT s")
			s.Exec("ALTER TABLE t ADD COLUMN undone integer DEFAULT nextval('td_rollback_seq')")
			s.Exec("ROLLBACK TO s")
			s.Exec("COMMIT")
			tdPeek(s, ",'include-rewrites','1'")
			tdFailure(s, "ALTER TABLE t ALTER COLUMN renamed TYPE integer USING renamed::integer", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`})
			tdGet(s, ",'include-rewrites','1'")
			predefinedSelect(s, "SELECT id,renamed,fast FROM t", []string{"id", "renamed", "fast"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(1), "bad", int64(9)}})
		})
	})
	t.Run("stream_filter", func(t *testing.T) {
		tdCases(t, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
			s.Exec("CREATE TABLE t(id integer,v integer)")
			s.Run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
			tdSlot(s, "td_slot")
			s.Exec("SET debug_logical_replication_streaming=immediate")
			s.Exec("ALTER TABLE t ALTER COLUMN v TYPE text USING ('x'||v::text)")
			tdPeek(s, tdStream)
			tdGet(s, tdStream+",'include-rewrites','1'", tdOpen, tdChange, tdClose, tdCommit)
		})
	})
}
