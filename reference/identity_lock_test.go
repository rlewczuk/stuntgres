//go:build reference

package reference_test

import (
	"context"
	"testing"
)

func TestReferenceIdentitySequenceGrantFreshness(t *testing.T) {
	for _, action := range []string{`ALTER TABLE i ALTER id SET CACHE 2`, `ALTER TABLE i ALTER id RESTART WITH 20`, `ALTER TABLE i ALTER id DROP IDENTITY`} {
		t.Run(action, func(t *testing.T) {
			p := newReferencePair(t)
			a, b := referenceLockConnections(t, p.ctx, p.postgres)
			referenceLockExec(t, p.ctx, a, `CREATE TABLE i(id INT GENERATED ALWAYS AS IDENTITY)`, 0)
			oid := ownedTableReferenceOID(t, p.ctx, a, "i_id_seq")
			ap, bp := referenceLockPID(t, p.ctx, a), referenceLockPID(t, p.ctx, b)
			holder, err := a.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Rollback()
			referenceLockExec(t, p.ctx, holder, `ALTER SEQUENCE i_id_seq INCREMENT 3 RESTART WITH 10`, 0)
			execute := ownedTableReferenceExec(t, p.ctx, b, action, false)
			ctx, cancel := context.WithCancel(p.ctx)
			defer cancel()
			await := referenceLockOperation(t, ctx, execute)
			mode := "ShareRowExclusiveLock"
			if action == `ALTER TABLE i ALTER id DROP IDENTITY` {
				mode = "AccessExclusiveLock"
			}
			ownedTableReferenceQueue(t, p.ctx, bp, ap, oid, mode)
			if err := holder.Commit(); err != nil {
				t.Fatal(err)
			}
			result := await()
			if result.err != nil {
				t.Fatal(result.err)
			}
			referenceLockAffected(t, result.result, 0)
			if action == `ALTER TABLE i ALTER id DROP IDENTITY` {
				referenceLockExec(t, p.ctx, b, `INSERT INTO i VALUES(55)`, 1)
				ownedTableReferenceMissing(t, p.ctx, b, "i_id_seq")
			} else {
				referenceLockExec(t, p.ctx, b, `INSERT INTO i DEFAULT VALUES`, 1)
				want := int64(10)
				if action == `ALTER TABLE i ALTER id RESTART WITH 20` {
					want = 20
				}
				sequenceProbeValue(t, p.ctx, b, `SELECT id FROM i`, want)
				var increment int64
				if err := b.QueryRowContext(p.ctx, `SELECT increment_by FROM pg_sequences WHERE schemaname='public' AND sequencename='i_id_seq'`).Scan(&increment); err != nil || increment != 3 {
					t.Fatal(increment, err)
				}
			}
		})
	}
}
