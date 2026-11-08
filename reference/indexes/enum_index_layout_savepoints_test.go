package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceEnumIndexLayoutGenerationSavepoints(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprintf("commit=%t", commit), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					c := enumLayoutCase{t, p, backend}
					owner, peer := fixture.LockConnections(t, p.Ctx, backend.DB)
					c.exec(owner, "BEGIN", 0)
					defer owner.ExecContext(p.Ctx, "ROLLBACK")
					c.exec(owner, enumLayoutCreateType, 0)
					c.exec(owner, enumLayoutCreateTable, 0)
					c.exec(owner, enumLayoutInsert, 1, "pending")
					c.exec(owner, enumLayoutCreateIndex, 0)
					id := c.identity(owner)
					c.check(owner, id, false)
					c.absent(peer)
					c.exec(owner, "SAVEPOINT generation", 0)
					c.exec(owner, enumLayoutAddValue, 0)
					c.check(owner, id, true)
					c.exec(owner, enumLayoutAddColumn, 0)
					c.exec(owner, enumLayoutInsert, 1, "pending")
					c.rows(owner, true, [][]any{{"pending", nil}, {"pending", nil}})
					c.exec(owner, "ROLLBACK TO generation", 0)
					c.check(owner, id, false)
					c.rows(owner, false, [][]any{{"pending"}})
					c.run(owner, fixture.Select(`SELECT attname FROM pg_attribute WHERE attrelid='grind_enum_indexed'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum`, []string{"attname"}, []string{"NAME"}, [][]any{{"status"}}))
					c.exec(owner, "ROLLBACK TO generation", 0)
					c.check(owner, id, false)
					c.exec(owner, "RELEASE generation", 0)
					// Retry ADD at the outer level: the same-transaction safe-use exception
					// deliberately does not apply to ADD performed in a child transaction.
					c.exec(owner, enumLayoutAddValue, 0)
					c.check(owner, id, true)
					c.exec(owner, "SAVEPOINT suffix", 0)
					c.exec(owner, enumLayoutAddColumn, 0)
					c.exec(owner, enumLayoutInsert, 1, "inhibited")
					c.exec(owner, `UPDATE grind_enum_indexed SET extra='discarded' WHERE status='pending'`, 1)
					c.exec(owner, "ROLLBACK TO suffix", 0)
					c.check(owner, id, true)
					c.rows(owner, false, [][]any{{"pending"}})
					c.run(owner, fixture.Select(`SELECT attname FROM pg_attribute WHERE attrelid='grind_enum_indexed'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum`, []string{"attname"}, []string{"NAME"}, [][]any{{"status"}}))
					c.exec(owner, "ROLLBACK TO suffix", 0)
					c.exec(owner, enumLayoutAddColumn, 0)
					c.exec(owner, enumLayoutInsert, 1, "inhibited")
					c.exec(owner, "RELEASE suffix", 0)
					c.check(owner, id, true)
					c.rows(owner, true, [][]any{{"pending", nil}, {"inhibited", nil}})
					c.absent(peer)
					if commit {
						c.exec(owner, "COMMIT", 0)
						c.check(peer, id, true)
						c.rows(peer, true, [][]any{{"pending", nil}, {"inhibited", nil}})
					} else {
						c.exec(owner, "ROLLBACK", 0)
						c.absent(peer)
						c.run(peer, fixture.Select(`SELECT count(*) FROM pg_depend WHERE (classid='pg_class'::regclass AND objid IN ($1::oid,$2::oid)) OR (refclassid='pg_type'::regclass AND refobjid=$3::oid)`, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, id.index, id.parent, id.typ))
					}
				})
			}
		})
	}
}

func TestReferenceEnumIndexLayoutGenerationFeatureSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c := enumLayoutCase{t, p, backend}
			owner, peer := fixture.LockConnections(t, p.Ctx, backend.DB)
			tx, err := owner.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			c.exec(tx, enumLayoutCreateType, 0)
			c.exec(tx, enumLayoutCreateTable, 0)
			c.exec(tx, enumLayoutInsert, 1, "pending")
			c.exec(tx, enumLayoutCreateIndex, 0)
			id := c.identity(tx)
			c.exec(tx, enumLayoutAddValue, 0)
			c.exec(tx, enumLayoutAddColumn, 0)
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			c.exec(peer, enumLayoutInsert, 1, "inhibited")
			c.rows(peer, true, [][]any{{"pending", nil}, {"inhibited", nil}})
			c.check(peer, id, true)
		})
	}
}
