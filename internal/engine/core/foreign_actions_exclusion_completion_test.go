package core_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/util"
)

func TestForeignActionsExclusionCompletion(t *testing.T) {
	for _, worked := range []string{"no-row-query", "unchanged-parent-key"} {
		t.Run(worked, func(t *testing.T) {
			type fixture struct {
				engine          *core.Engine
				creator, writer *session.Session
			}
			newFixture := func(retained bool) fixture {
				e := engine.New()
				creator, writer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
				for _, q := range []string{"CREATE TABLE p(id INT PRIMARY KEY)", "CREATE TABLE c(pid INT REFERENCES p ON UPDATE CASCADE)", "INSERT INTO p VALUES(1),(2)"} {
					enginetest.SessionSQL(t, creator, q)
				}
				var clauses []string
				for i := range 32 {
					clauses = append(clauses, fmt.Sprintf("CONSTRAINT ex_%d EXCLUDE USING hash(k WITH =) DEFERRABLE INITIALLY DEFERRED", i))
				}
				enginetest.SessionSQL(t, creator, "CREATE TABLE guarded(k INT,"+strings.Join(clauses, ",")+")")
				tx := enginetest.TransactionBegin(t, writer)
				t.Cleanup(func() { enginetest.TransactionEnd(t, writer, tx, false) })
				if retained {
					enginetest.SessionSQL(t, writer, "INSERT INTO guarded VALUES(1),(1)")
				} else {
					enginetest.SessionSQL(t, writer, "SET CONSTRAINTS ALL IMMEDIATE")
					enginetest.SessionSQL(t, writer, "INSERT INTO guarded VALUES(1),(2)")
				}
				return fixture{e, creator, writer}
			}
			control, deferred := newFixture(false), newFixture(true)
			if len(core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(deferred.writer.Transactions)).Events) != 32 || len(core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(control.writer.Transactions)).Events) != 0 {
				t.Fatal("fixture did not create real retained deferred versus settled work")
			}
			events := core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(deferred.writer.Transactions)).Events
			membership := slices.Clone(events)
			// Separate engines permit the same parent key and physical version
			// stream without conflicting row locks. Both fixtures insert two
			// guarded rows: only genuine deferred membership differs, not the
			// heap-header persistent map's hashed predecessor-key sequence.
			checkFrontiers := func() {
				control.engine.Mu.Lock()
				cr, cv := control.engine.Storage.RowFrontier(), control.engine.Storage.VersionFrontier()
				control.engine.Mu.Unlock()
				deferred.engine.Mu.Lock()
				dr, dv := deferred.engine.Storage.RowFrontier(), deferred.engine.Storage.VersionFrontier()
				deferred.engine.Mu.Unlock()
				if cr != dr || cv != dv {
					t.Fatalf("unmatched physical fixture frontiers: control rows/versions %d/%d, retained %d/%d", cr, cv, dr, dv)
				}
			}
			checkFrontiers()
			q := "SELECT 1"
			if worked == "unchanged-parent-key" {
				q = "UPDATE p SET id=id WHERE id=1"
			}
			ast := enginetest.Statement(t, q)
			complete := func(f fixture) {
				op, err := f.writer.Acquire(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				result, err := f.engine.Execute(op, ast, 0, nil)
				if result.Rows != nil {
					closeErr := result.Rows.Close()
					if err == nil {
						err = closeErr
					}
				}
				op.Done()
				if err != nil {
					t.Fatal(err)
				}
			}
			// Follow docs/TESTING.md#allocation-checks: minimum-of-three fixed
			// batches exclude sampled runtime type-assertion cache allocations
			// without changing this whole-completion relative allocation budget.
			baseline, _ := util.AllocationsPerCall(20, func() { complete(control) })
			retained, _ := util.AllocationsPerCall(20, func() { complete(deferred) })
			checkFrontiers()
			after := core.OwnerExtensionConstraintModes(core.OwnerExtensionOrZero(deferred.writer.Transactions)).Events
			if !slices.Equal(after, membership) || len(after) != len(events) || &after[0] != &events[0] {
				t.Fatal("completion changed unrelated deferred event membership or backing")
			}
			// The checker borrows retained membership and leaves deferred events
			// unclaimed. Preserve the original one-allocation relative cap while
			// matching all ordinary physical write work on both sides.
			if retained > baseline+1 {
				t.Errorf("retained deferred completion allocations %d exceed control %d plus one retained-completion allocation", retained, baseline)
			}
			op, err := deferred.writer.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			result, err := deferred.engine.Execute(op, enginetest.Statement(t, "SET CONSTRAINTS ALL IMMEDIATE"), 0, nil)
			op.Done()
			var d *sqlstate.Error
			if result.Rows != nil || !errors.As(err, &d) || d.Code != "23P01" {
				t.Fatal("completion prematurely checked or lost genuine deferred violation", result, err)
			}
			// The prior worked commands neither publish deferred data nor cascade.
			for _, f := range []fixture{control, deferred} {
				enginetest.TransactionRows(t, f.creator, "SELECT id FROM p ORDER BY id", 1, 2)
				enginetest.TransactionRows(t, f.creator, "SELECT k FROM guarded ORDER BY k")
			}
		})
	}
}
