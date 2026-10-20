package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestForeignActionsExclusionCompletion(t *testing.T) {
	for _, worked := range []string{"no-row-query", "unchanged-parent-key"} {
		t.Run(worked, func(t *testing.T) {
			e := New()
			creator, control, deferred := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
			for _, q := range []string{"CREATE TABLE p(id INT PRIMARY KEY)", "CREATE TABLE c(pid INT REFERENCES p ON UPDATE CASCADE)", "INSERT INTO p VALUES(1),(2)"} {
				sessionSQL(t, creator, q)
			}
			var clauses []string
			for i := 0; i < 32; i++ {
				clauses = append(clauses, fmt.Sprintf("CONSTRAINT ex_%d EXCLUDE USING hash(k WITH =) DEFERRABLE INITIALLY DEFERRED", i))
			}
			sessionSQL(t, creator, "CREATE TABLE guarded(k INT,"+strings.Join(clauses, ",")+")")
			cr, dr := transactionBegin(t, control), transactionBegin(t, deferred)
			t.Cleanup(func() { transactionEnd(t, control, cr, false); transactionEnd(t, deferred, dr, false) })
			sessionSQL(t, control, "SET CONSTRAINTS ALL IMMEDIATE")
			sessionSQL(t, control, "INSERT INTO guarded VALUES(2)")
			sessionSQL(t, deferred, "INSERT INTO guarded VALUES(1),(1)")
			if len(ownerExtensionOrZero(deferred.transactions).exclusionModes.events) != 32 || len(ownerExtensionOrZero(control.transactions).exclusionModes.events) != 0 {
				t.Fatal("fixture did not create real retained deferred versus settled work")
			}
			cq, dq := "SELECT 1", "SELECT 1"
			if worked == "unchanged-parent-key" {
				cq = "UPDATE p SET id=id WHERE id=1"
				dq = "UPDATE p SET id=id WHERE id=2"
			}
			ca, da := statement(t, cq), statement(t, dq)
			complete := func(s *Session, action bool) {
				op, err := s.Acquire(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				ast := ca
				if action {
					ast = da
				}
				result, err := e.Execute(op, ast, 0, nil)
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
			baseline := testing.AllocsPerRun(20, func() { complete(control, false) })
			retained := testing.AllocsPerRun(20, func() { complete(deferred, true) })
			// The checker needs one remaining-event vector for the one incumbent
			// deferred pass. Whole real completion otherwise has the same statement,
			// private relation inventory and no action-created exclusion demand.
			if retained > baseline+1 {
				t.Errorf("retained deferred completion allocations %.0f exceed control %.0f plus one incumbent event vector", retained, baseline)
			}
			op, err := deferred.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			result, err := e.Execute(op, statement(t, "SET CONSTRAINTS ALL IMMEDIATE"), 0, nil)
			op.Done()
			var d *sqlstate.Error
			if result.Rows != nil || !errors.As(err, &d) || d.Code != "23P01" {
				t.Fatal("completion prematurely checked or lost genuine deferred violation", result, err)
			}
			// The prior worked commands neither publish deferred data nor cascade.
			transactionRows(t, creator, "SELECT id FROM p ORDER BY id", 1, 2)
			transactionRows(t, creator, "SELECT k FROM guarded ORDER BY k")
		})
	}
}
