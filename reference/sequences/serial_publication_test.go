//go:build reference

package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func serialReferenceDefinition(alter bool) string {
	if alter {
		return `ALTER TABLE consumer ALTER id SET DEFAULT nextval('q')`
	}
	return `CREATE TABLE consumer(id INT DEFAULT nextval('q'))`
}

func serialReferenceNoProviderHold(t *testing.T, p *fixture.Pair, pid int, oid int64) {
	t.Helper()
	var count int
	if err := fixture.Admin.QueryRowContext(p.Ctx, `SELECT count(*) FROM pg_locks WHERE pid=$1 AND locktype='relation' AND relation=$2::oid`, pid, oid).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("unexpected PostgreSQL provider relation hold", pid, oid, count)
	}
}

func serialReferenceProviderFinal(t *testing.T, p *fixture.Pair, c *sql.Conn, oid int64, exists bool, dangling bool) {
	t.Helper()
	var present bool
	if err := c.QueryRowContext(p.Ctx, `SELECT EXISTS(SELECT 1 FROM pg_class WHERE oid=$1::oid)`, oid).Scan(&present); err != nil || present != exists {
		t.Fatal(present, exists, err)
	}
	var count int
	if err := c.QueryRowContext(p.Ctx, `SELECT count(*) FROM pg_attrdef a JOIN pg_depend d ON d.classid='pg_attrdef'::regclass AND d.objid=a.oid WHERE a.adrelid='consumer'::regclass AND a.adnum=1 AND d.refclassid='pg_class'::regclass AND d.refobjid=$1::oid AND d.deptype='n'`, oid).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("captured PostgreSQL default provider dependency", count)
	}
	_, err := c.ExecContext(p.Ctx, `INSERT INTO consumer DEFAULT VALUES`)
	if !dangling {
		if err != nil {
			t.Fatal(err)
		}
		fixture.SequenceProbeValue(t, p.Ctx, c, `SELECT id FROM consumer`, 1)
		return
	}
	if err == nil {
		t.Fatal("PostgreSQL dangling provider allocated after committed DROP")
	}
	// Independently observed on pinned PostgreSQL for both CREATE/ALTER and
	// publication orderings. Native rejects publication instead of this edge.
	want := fixture.Diagnostic{
		Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "XX000",
		Message: fmt.Sprintf("could not open relation with OID %d", oid),
	}
	fixture.AssertDiagnostic(t, err, &want)
	fixture.SequenceProbeValue(t, p.Ctx, c, `SELECT count(*) FROM consumer`, 0)
}

// Pinned PostgreSQL does not retain a sequence relation hold for this cooked
// default. These real transaction interleavings are intentionally PG-only; native
// stage-gated tests specify its stronger catalog-integrity publication fence.
func TestReferenceSerialDefaultProviderPublicationHold(t *testing.T) {
	for _, alter := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			t.Run(fmt.Sprintf("alter=%t/commit=%t", alter, commit), func(t *testing.T) {
				p := fixture.NewPair(t)
				a, b := fixture.LockConnections(t, p.Ctx, p.Postgres)
				fixture.LockExec(t, p.Ctx, a, `CREATE SEQUENCE q`, 0)
				if alter {
					fixture.LockExec(t, p.Ctx, a, `CREATE TABLE consumer(id SERIAL)`, 0)
				}
				oid := ownedTableReferenceOID(t, p.Ctx, a, "q")
				holder, err := a.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer holder.Rollback()
				fixture.LockExec(t, p.Ctx, holder, serialReferenceDefinition(alter), 0)
				serialReferenceNoProviderHold(t, p, fixture.LockPID(t, p.Ctx, b), oid)
				var holderPID int
				if err := holder.QueryRowContext(p.Ctx, `SELECT pg_backend_pid()`).Scan(&holderPID); err != nil {
					t.Fatal(err)
				}
				serialReferenceNoProviderHold(t, p, holderPID, oid)
				result := ownedTableReferenceExec(t, p.Ctx, b, `DROP SEQUENCE q RESTRICT`, true)(p.Ctx)
				if result.Err != nil {
					t.Fatal("independent DROP completed before definition endpoint", result.Err)
				}
				fixture.LockAffected(t, result.Result, 0)
				if commit {
					err = holder.Commit()
				} else {
					err = holder.Rollback()
				}
				if err != nil {
					t.Fatal(err)
				}
				if commit {
					serialReferenceProviderFinal(t, p, a, oid, false, true)
					return
				}
				if !alter {
					fixture.LockExec(t, p.Ctx, a, `CREATE TABLE consumer(id SERIAL)`, 0)
				}
				fixture.LockExec(t, p.Ctx, a, `INSERT INTO consumer DEFAULT VALUES`, 1)
				fixture.SequenceProbeValue(t, p.Ctx, a, `SELECT id FROM consumer`, 1)
			})
		}
	}
}

func TestReferenceSerialDefaultProviderDropFirst(t *testing.T) {
	for _, alter := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			t.Run(fmt.Sprintf("alter=%t/commit=%t", alter, commit), func(t *testing.T) {
				p := fixture.NewPair(t)
				a, b := fixture.LockConnections(t, p.Ctx, p.Postgres)
				fixture.LockExec(t, p.Ctx, a, `CREATE SEQUENCE q`, 0)
				if alter {
					fixture.LockExec(t, p.Ctx, a, `CREATE TABLE consumer(id SERIAL)`, 0)
				}
				oid := ownedTableReferenceOID(t, p.Ctx, a, "q")
				bp := fixture.LockPID(t, p.Ctx, b)
				holder, err := a.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer holder.Rollback()
				fixture.LockExec(t, p.Ctx, holder, `DROP SEQUENCE q RESTRICT`, 0)
				result := ownedTableReferenceExec(t, p.Ctx, b, serialReferenceDefinition(alter), true)(p.Ctx)
				if result.Err != nil {
					t.Fatal("independent definition completed before DROP endpoint", result.Err)
				}
				fixture.LockAffected(t, result.Result, 0)
				serialReferenceNoProviderHold(t, p, bp, oid)
				if commit {
					err = holder.Commit()
				} else {
					err = holder.Rollback()
				}
				if err != nil {
					t.Fatal(err)
				}
				serialReferenceProviderFinal(t, p, b, oid, !commit, commit)
			})
		}
	}
}
