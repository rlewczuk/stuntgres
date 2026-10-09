//go:build reference

package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

func serialReferenceDefinition(alter bool) string {
	if alter {
		return `ALTER TABLE consumer ALTER id SET DEFAULT nextval('q')`
	}
	return `CREATE TABLE consumer(id INT DEFAULT nextval('q'))`
}

func serialReferenceNoProviderHold(t *testing.T, p *referencePair, pid int, oid int64) {
	t.Helper()
	var count int
	if err := referenceAdmin.QueryRowContext(p.ctx, `SELECT count(*) FROM pg_locks WHERE pid=$1 AND locktype='relation' AND relation=$2::oid`, pid, oid).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("unexpected PostgreSQL provider relation hold", pid, oid, count)
	}
}

func serialReferenceProviderFinal(t *testing.T, p *referencePair, c *sql.Conn, oid int64, exists bool, dangling bool) {
	t.Helper()
	var present bool
	if err := c.QueryRowContext(p.ctx, `SELECT EXISTS(SELECT 1 FROM pg_class WHERE oid=$1::oid)`, oid).Scan(&present); err != nil || present != exists {
		t.Fatal(present, exists, err)
	}
	var count int
	if err := c.QueryRowContext(p.ctx, `SELECT count(*) FROM pg_attrdef a JOIN pg_depend d ON d.classid='pg_attrdef'::regclass AND d.objid=a.oid WHERE a.adrelid='consumer'::regclass AND a.adnum=1 AND d.refclassid='pg_class'::regclass AND d.refobjid=$1::oid AND d.deptype='n'`, oid).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("captured PostgreSQL default provider dependency", count)
	}
	_, err := c.ExecContext(p.ctx, `INSERT INTO consumer DEFAULT VALUES`)
	if !dangling {
		if err != nil {
			t.Fatal(err)
		}
		sequenceProbeValue(t, p.ctx, c, `SELECT id FROM consumer`, 1)
		return
	}
	if err == nil {
		t.Fatal("PostgreSQL dangling provider allocated after committed DROP")
	}
	// Independently observed on pinned PostgreSQL for both CREATE/ALTER and
	// publication orderings. Native rejects publication instead of this edge.
	want := referenceDiagnostic{
		Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "XX000",
		Message: fmt.Sprintf("could not open relation with OID %d", oid),
	}
	referenceAssertDiagnostic(t, err, &want)
	sequenceProbeValue(t, p.ctx, c, `SELECT count(*) FROM consumer`, 0)
}

// Pinned PostgreSQL does not retain a sequence relation hold for this cooked
// default. These real transaction interleavings are intentionally PG-only; native
// stage-gated tests specify its stronger catalog-integrity publication fence.
func TestReferenceSerialDefaultProviderPublicationHold(t *testing.T) {
	for _, alter := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			t.Run(fmt.Sprintf("alter=%t/commit=%t", alter, commit), func(t *testing.T) {
				p := newReferencePair(t)
				a, b := referenceLockConnections(t, p.ctx, p.postgres)
				referenceLockExec(t, p.ctx, a, `CREATE SEQUENCE q`, 0)
				if alter {
					referenceLockExec(t, p.ctx, a, `CREATE TABLE consumer(id SERIAL)`, 0)
				}
				oid := ownedTableReferenceOID(t, p.ctx, a, "q")
				holder, err := a.BeginTx(p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer holder.Rollback()
				referenceLockExec(t, p.ctx, holder, serialReferenceDefinition(alter), 0)
				serialReferenceNoProviderHold(t, p, referenceLockPID(t, p.ctx, b), oid)
				var holderPID int
				if err := holder.QueryRowContext(p.ctx, `SELECT pg_backend_pid()`).Scan(&holderPID); err != nil {
					t.Fatal(err)
				}
				serialReferenceNoProviderHold(t, p, holderPID, oid)
				result := ownedTableReferenceExec(t, p.ctx, b, `DROP SEQUENCE q RESTRICT`, true)(p.ctx)
				if result.err != nil {
					t.Fatal("independent DROP completed before definition endpoint", result.err)
				}
				referenceLockAffected(t, result.result, 0)
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
					referenceLockExec(t, p.ctx, a, `CREATE TABLE consumer(id SERIAL)`, 0)
				}
				referenceLockExec(t, p.ctx, a, `INSERT INTO consumer DEFAULT VALUES`, 1)
				sequenceProbeValue(t, p.ctx, a, `SELECT id FROM consumer`, 1)
			})
		}
	}
}

func TestReferenceSerialDefaultProviderDropFirst(t *testing.T) {
	for _, alter := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			t.Run(fmt.Sprintf("alter=%t/commit=%t", alter, commit), func(t *testing.T) {
				p := newReferencePair(t)
				a, b := referenceLockConnections(t, p.ctx, p.postgres)
				referenceLockExec(t, p.ctx, a, `CREATE SEQUENCE q`, 0)
				if alter {
					referenceLockExec(t, p.ctx, a, `CREATE TABLE consumer(id SERIAL)`, 0)
				}
				oid := ownedTableReferenceOID(t, p.ctx, a, "q")
				bp := referenceLockPID(t, p.ctx, b)
				holder, err := a.BeginTx(p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer holder.Rollback()
				referenceLockExec(t, p.ctx, holder, `DROP SEQUENCE q RESTRICT`, 0)
				result := ownedTableReferenceExec(t, p.ctx, b, serialReferenceDefinition(alter), true)(p.ctx)
				if result.err != nil {
					t.Fatal("independent definition completed before DROP endpoint", result.err)
				}
				referenceLockAffected(t, result.result, 0)
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
