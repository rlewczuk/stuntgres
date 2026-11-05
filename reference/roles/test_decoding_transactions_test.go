package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceTestDecodingTransactions(t *testing.T) {
	tdCases(t, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
		tdTable(s)
		tdSlot(s, "td_slot")
		tdDML(s)
		tdPeek(s, "", tdDMLRows()...)
		tdGet(s, "", tdDMLRows()...)
		tdGet(s, "")
		predefinedSelect(s, "SELECT id,v FROM t", []string{"id", "v"}, []string{"INT4", "TEXT"}, nil)
	})
}

func TestReferenceTestDecodingFormatting(t *testing.T) {
	t.Run("typed_identifiers", func(t *testing.T) {
		tdCases(t, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
			s.Exec(`CREATE TABLE "select"("Mixed" integer,"a""b" text,truth boolean,n numeric(8,2),bits bit(3),gone text,z text)`)
			s.Exec(`ALTER TABLE "select" DROP COLUMN gone`)
			tdSlot(s, "td_slot")
			s.Run(fixture.Exec(`INSERT INTO "select" VALUES(7,E'O''Brien\\path',true,12.50,B'101',NULL)`, 1))
			tdGet(s, "", tdTxn(`table public."select": INSERT: "Mixed"[integer]:7 "a""b"[text]:'O''Brien\path' truth[boolean]:true n[numeric]:12.50 bits[bit]:B'101' z[text]:null`)...)
		})
	})
	t.Run("canonical_types", func(t *testing.T) {
		tdCases(t, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
			s.Exec("CREATE TABLE td_types(s smallint,b bigint,o oid,r real,d double precision,v varbit,flag boolean,dt date,raw bytea)")
			tdSlot(s, "td_slot")
			s.Run(fixture.Exec("INSERT INTO td_types VALUES(-2,9223372036854775807,42,1.5,2.25,B'011',false,DATE '2026-01-01',decode('00ff','hex'))", 1))
			tdGet(s, "", tdTxn(`table public.td_types: INSERT: s[smallint]:-2 b[bigint]:9223372036854775807 o[oid]:42 r[real]:1.5 d[double precision]:2.25 v[bit varying]:B'011' flag[boolean]:false dt[date]:'2026-01-01' raw[bytea]:'\x00ff'`)...)
		})
	})
	t.Run("replica_identity", func(t *testing.T) {
		tdCases(t, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
			tdTable(s)
			s.Exec("ALTER TABLE t REPLICA IDENTITY FULL")
			s.Run(fixture.Exec("INSERT INTO t VALUES(1,NULL)", 1))
			tdSlot(s, "td_slot")
			s.Exec("BEGIN")
			s.Run(fixture.Exec("UPDATE t SET v='v'", 1))
			s.Run(fixture.Exec("DELETE FROM t", 1))
			s.Exec("COMMIT")
			tdGet(s, "", tdTxn("table public.t: UPDATE: old-key: id[integer]:1 new-tuple: id[integer]:1 v[text]:'v'", "table public.t: DELETE: id[integer]:1 v[text]:'v'")...)
			s.Exec("ALTER TABLE t REPLICA IDENTITY NOTHING")
			tdGet(s, "")
			s.Run(fixture.Exec("INSERT INTO t VALUES(2,'v')", 1))
			tdGet(s, "", tdTxn(tdInsert(2))...)
			s.Run(fixture.Exec("DELETE FROM t", 1))
			tdGet(s, "", tdTxn("table public.t: DELETE: (no-tuple-data)")...)
		})
	})
}

func TestReferenceTestDecodingTruncate(t *testing.T) {
	tdCases(t, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
		s.Exec("CREATE TABLE t(id serial PRIMARY KEY)")
		s.Exec("CREATE TABLE u(id integer REFERENCES t)")
		tdSlot(s, "td_slot")
		s.Exec("TRUNCATE u,t RESTART IDENTITY CASCADE")
		tdGet(s, "", tdTxn("table public.u, public.t: TRUNCATE: restart_seqs cascade")...)
		s.Exec("TRUNCATE t CASCADE")
		tdGet(s, "", tdTxn("table public.t, public.u: TRUNCATE: cascade")...)
		predefinedSelect(s, "SELECT count(*) FROM t", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}})
	})
}

func TestReferenceTestDecodingVisibility(t *testing.T) {
	tdCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		tdTable(s)
		tdSlot(s, "td_slot")
		a, b := tdPeer(s, db), tdPeer(s, db)
		tdExec(s, a, "BEGIN", 0)
		tdExec(s, a, "INSERT INTO t VALUES(1,'v')", 1)
		tdPeek(s, "")
		tdExec(s, b, "BEGIN", 0)
		tdExec(s, b, "INSERT INTO t VALUES(2,'v')", 1)
		tdExec(s, b, "COMMIT", 0)
		tdPeek(s, "", tdTxn(tdInsert(2))...)
		tdExec(s, a, "COMMIT", 0)
		want := append(tdTxn(tdInsert(2)), tdTxn(tdInsert(1))...)
		tdGet(s, "", want...)
		s.Exec("BEGIN")
		s.Run(fixture.Exec("INSERT INTO t VALUES(3,'v')", 1))
		s.Exec("ROLLBACK")
		tdGet(s, "")
		s.Exec("BEGIN")
		s.Exec("SAVEPOINT outer_save")
		s.Run(fixture.Exec("INSERT INTO t VALUES(4,'v')", 1))
		s.Exec("SAVEPOINT inner_save")
		s.Run(fixture.Exec("INSERT INTO t VALUES(5,'v')", 1))
		s.Exec("RELEASE inner_save")
		s.Exec("ROLLBACK TO outer_save")
		s.Run(fixture.Exec("INSERT INTO t VALUES(6,'v')", 1))
		s.Exec("ROLLBACK TO outer_save")
		s.Run(fixture.Exec("INSERT INTO t VALUES(7,'v')", 1))
		s.Exec("COMMIT")
		tdGet(s, "", tdTxn(tdInsert(7))...)
		s.Exec(`DO $$ BEGIN BEGIN INSERT INTO t VALUES(8,'v'); RAISE EXCEPTION 'undo'; EXCEPTION WHEN raise_exception THEN INSERT INTO t VALUES(9,'v'); END; END $$`)
		tdGet(s, "", tdTxn(tdInsert(9))...)
		predefinedSelect(s, "SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(7)}, {int64(9)}})
	})
}

func TestReferenceTestDecodingEmptyTransactions(t *testing.T) {
	tdCases(t, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
		tdSlot(s, "td_slot")
		s.Exec("CREATE TABLE catalog_only(v integer)")
		// A real catalog write, not a read-only BEGIN/COMMIT, feeds the decoder.
		tdPeek(s, "")
		tdPeek(s, ",'skip-empty-xacts','0'", "BEGIN", "COMMIT")
		tdGet(s, ",'skip-empty-xacts','0'", "BEGIN", "COMMIT")
		tdGet(s, ",'skip-empty-xacts','0'")
	})
}

func TestReferenceTestDecodingOrigins(t *testing.T) {
	tdCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		tdTable(s)
		tdSlot(s, "td_slot")
		writer := tdPeer(s, db)
		var oid string
		if err := writer.QueryRowContext(s.P.Ctx, "SELECT pg_replication_origin_create('td_origin')::text").Scan(&oid); err != nil {
			s.T.Fatal(err)
		}
		s.T.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			writer.ExecContext(ctx, "ROLLBACK")
			writer.ExecContext(ctx, "SELECT pg_replication_origin_session_reset()")
			if _, err := writer.ExecContext(ctx, "SELECT pg_replication_origin_drop('td_origin')"); err != nil {
				s.T.Error(err)
			}
		})
		tdExec(s, writer, "SELECT pg_replication_origin_session_setup('td_origin')", 1)
		tdExec(s, writer, "BEGIN", 0)
		tdExec(s, writer, "SELECT pg_replication_origin_xact_setup('0/100','2026-01-01 00:00:00+00')", 1)
		tdExec(s, writer, "INSERT INTO t VALUES(1,'v')", 1)
		tdExec(s, writer, "COMMIT", 0)
		tdExec(s, writer, "SELECT pg_replication_origin_session_reset()", 1)
		s.Run(fixture.Exec("INSERT INTO t VALUES(2,'v')", 1))
		tdPeek(s, "", append(tdTxn(tdInsert(1)), tdTxn(tdInsert(2))...)...)
		tdPeek(s, ",'only-local','1','only-local','0'", append(tdTxn(tdInsert(1)), tdTxn(tdInsert(2))...)...)
		tdGet(s, ",'only-local','1'", tdTxn(tdInsert(2))...)
		tdGet(s, "")
	})
}
