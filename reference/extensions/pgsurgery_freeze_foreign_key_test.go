package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgsurgeryFreezeForeignKey(t *testing.T) {
	for _, action := range []string{"CASCADE", "RESTRICT"} {
		t.Run(action, func(t *testing.T) {
			freezeCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				horizon := freezeHorizon(t, p, b)
				defer horizon.Close()
				defer horizon.ExecContext(p.Ctx, "ROLLBACK")
				freezeRun(t, p, b, c, fixture.Exec("CREATE TABLE fs_part.ri_parent(k integer PRIMARY KEY)", 0))
				freezeRun(t, p, b, c, fixture.Exec("INSERT INTO fs_part.ri_parent VALUES(1),(2)", 2))
				freezeRun(t, p, b, c, fixture.Exec("ALTER TABLE fs_part.heap ADD CONSTRAINT ri_fk FOREIGN KEY(k) REFERENCES fs_part.ri_parent ON DELETE "+action, 0))
				a := freezeOne(t, p, c, "INSERT INTO fs_part.heap VALUES(1,'A') RETURNING k,v,ctid,xmin,xmax")
				z := freezeOne(t, p, c, "INSERT INTO fs_part.heap VALUES(2,'sentinel') RETURNING k,v,ctid,xmin,xmax")
				q := "UPDATE fs_part.heap SET v='B' WHERE v='A' RETURNING k,v,ctid,xmin,xmax"
				if action == "RESTRICT" {
					q = "UPDATE fs_part.heap SET k=2,v='B' WHERE v='A' RETURNING k,v,ctid,xmin,xmax"
				}
				next := freezeOne(t, p, c, q)
				freezeApply(t, p, b, c, a.tid)
				a = freezeRepaired(a)
				freezeExpect(t, p, b, c, a, next, z)
				if action == "CASCADE" {
					freezeRun(t, p, b, c, fixture.Exec("DELETE FROM fs_part.ri_parent WHERE k=1", 1))
					freezeExpect(t, p, b, c, z)
				} else {
					want := fixture.ControlsReferenceDiagnostic("23001", `update or delete on table "ri_parent" violates RESTRICT setting of foreign key constraint "ri_fk" on table "heap"`)
					want.Detail = `Key (k)=(1) is referenced from table "heap".`
					want.Schema, want.Table, want.Constraint = "fs_part", "heap", "ri_fk"
					freezeBoth(t, p, b, c, fixture.ImplicitError("DELETE FROM fs_part.ri_parent WHERE k=1", want))
					freezeForeignKeyPayloads(t, p, b, c, a, next, z)
				}
			})
		})
	}
}

func TestReferencePgsurgeryFreezeForeignKeyIndex(t *testing.T) {
	freezeCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		freezeRun(t, p, b, c, fixture.Exec("CREATE TABLE fs_part.ri_parent(k integer,v text)", 0))
		freezeRun(t, p, b, c, fixture.Exec("ALTER TABLE fs_part.ri_parent SET(autovacuum_enabled=false)", 0))
		freezeRun(t, p, b, c, fixture.Exec("BEGIN", 0))
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		lost := freezeOne(t, p, c, "INSERT INTO fs_part.ri_parent VALUES(7,'unindexed') RETURNING k,v,ctid,xmin,xmax")
		freezeRun(t, p, b, c, fixture.Exec("ROLLBACK", 0))
		// Build the actual supporting root while the inserted version is aborted.
		// Repair changes its heap header, not membership in that already-built root.
		freezeRun(t, p, b, c, fixture.Exec("ALTER TABLE fs_part.ri_parent ADD PRIMARY KEY(k)", 0))
		freezeRun(t, p, b, c, fixture.Exec("ALTER TABLE fs_part.heap ADD CONSTRAINT ri_fk FOREIGN KEY(k) REFERENCES fs_part.ri_parent", 0))
		freezeRun(t, p, b, c, freezeVoid(freezeSQL, "fs_part.ri_parent", freezeTIDs(lost.tid)))
		repaired := freezeOne(t, p, c, "SELECT k,v,ctid,xmin,xmax FROM fs_part.ri_parent")
		if repaired != freezeRepaired(lost) {
			t.Fatal("repair changed tuple identity or payload", repaired, lost)
		}
		want := fixture.ControlsReferenceDiagnostic("23503", `insert or update on table "heap" violates foreign key constraint "ri_fk"`)
		want.Detail = `Key (k)=(7) is not present in table "ri_parent".`
		want.Schema, want.Table, want.Constraint = "fs_part", "heap", "ri_fk"
		freezeRun(t, p, b, c, fixture.Exec("SET enable_seqscan=off", 0))
		defer c.ExecContext(p.Ctx, "RESET enable_seqscan")
		freezeBoth(t, p, b, c, fixture.ImplicitError("INSERT INTO fs_part.heap VALUES(7,'missing-index-member')", want))
		freezeExpect(t, p, b, c)
	})
}

func TestReferencePgsurgeryFreezeForeignKeySnapshot(t *testing.T) {
	freezeCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		freezeRun(t, p, b, c, fixture.Exec("CREATE TABLE fs_part.ri_parent(k integer PRIMARY KEY)", 0))
		freezeRun(t, p, b, c, fixture.Exec("INSERT INTO fs_part.ri_parent VALUES(1),(2)", 2))
		freezeRun(t, p, b, c, fixture.Exec("ALTER TABLE fs_part.heap ADD CONSTRAINT ri_fk FOREIGN KEY(k) REFERENCES fs_part.ri_parent ON DELETE CASCADE", 0))
		a := freezeOne(t, p, c, "INSERT INTO fs_part.heap VALUES(1,'A') RETURNING k,v,ctid,xmin,xmax")
		z := freezeOne(t, p, c, "INSERT INTO fs_part.heap VALUES(2,'sentinel') RETURNING k,v,ctid,xmin,xmax")
		reader := freezeConn(t, p, b)
		defer reader.Close()
		freezeRun(t, p, b, reader, fixture.Exec("BEGIN ISOLATION LEVEL REPEATABLE READ", 0))
		defer reader.ExecContext(p.Ctx, "ROLLBACK")
		freezeExpect(t, p, b, reader, a, z)
		next := freezeOne(t, p, c, "UPDATE fs_part.heap SET v='B' WHERE v='A' RETURNING k,v,ctid,xmin,xmax")
		freezeApply(t, p, b, c, a.tid)
		freezeExpect(t, p, b, c, freezeRepaired(a), next, z)
		// Repair reset A's actual successor link. B is now an independent
		// current match, not a RowID duplicate of the retained matching A.
		want := fixture.ControlsReferenceDiagnostic("40001", "could not serialize access due to concurrent update")
		want.Context = `SQL statement "DELETE FROM ONLY "fs_part"."heap" WHERE $1 OPERATOR(pg_catalog.=) "k""`
		freezeRun(t, p, b, reader, fixture.ImplicitError("DELETE FROM fs_part.ri_parent WHERE k=1", want))
		freezeRun(t, p, b, reader, fixture.Exec("ROLLBACK", 0))
		freezeForeignKeyPayloads(t, p, b, c, freezeRepaired(a), next, z)
	})
}

// Failed RI commands can leave raw aborted lock/delete xmax behind. Atomicity
// here concerns the same visible physical tuples and payloads, not undoing those
// header writes; the pre-action witnesses independently require repaired 2/0.
func freezeForeignKeyPayloads(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, expected ...freezeHeader) {
	t.Helper()
	var values [][]any
	for _, h := range expected {
		values = append(values, []any{h.k, h.v, h.tid, h.xmin})
	}
	freezeBoth(t, p, b, c, fixture.Select("SELECT k,v,ctid,xmin FROM fs_part.heap ORDER BY k,v", []string{"k", "v", "ctid", "xmin"}, []string{"INT4", "TEXT", "TID", "XID"}, values))
}
