package partitioncmds_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/sqlstate"
)

func TestInheritanceUsingIndexPreparedNotNull(t *testing.T) {
	for _, only := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "only"}[only], func(t *testing.T) {
			e := engine.New()
			for _, query := range []string{"CREATE TABLE p(a INT CONSTRAINT source_nn NOT NULL)", "CREATE TABLE c(extra TEXT) INHERITS(p)", "CREATE TABLE leaf() INHERITS(c)", "CREATE UNIQUE INDEX ci ON c(a) INCLUDE(extra)"} {
				ownershipUnitExec(t, e, query)
			}
			before := retiredUnitTable(t, e, "c")
			leaf := retiredUnitTable(t, e, "leaf")
			attribute := before.Attributes[before.Index["a"]]
			fact := before.Constraints.NotNull[0]
			origin := before.Inheritance.NotNull[attribute]
			if origin.Local || len(origin.Origins) != 1 || before.Inheritance.Columns[attribute].Local {
				t.Fatal("fixture did not start with inherited-only NN/attribute", origin)
			}
			target := "c"
			if only {
				target = "ONLY c"
			}
			ownershipUnitExec(t, e, "ALTER TABLE "+target+" ADD CONSTRAINT attached PRIMARY KEY USING INDEX ci")
			after := retiredUnitTable(t, e, "c")
			next := after.Inheritance.NotNull[attribute]
			if !next.Local || !reflect.DeepEqual(next.Origins, origin.Origins) || after.Constraints.NotNull[0].OID != fact.OID || after.Constraints.NotNull[0].Name != fact.Name || after.Inheritance.Columns[attribute].Local {
				t.Fatal("implicit KEY NN did not localize exactly the existing constraint", next, after.Constraints.NotNull)
			}
			if retiredUnitTable(t, e, "leaf") != leaf {
				t.Fatal("existing root NN merge visited/retagged descendant")
			}
			if len(after.Constraints.Unique) != 1 || !after.Constraints.Unique[0].Primary || !reflect.DeepEqual(after.Constraints.Unique[0].Attributes, []int32{attribute}) {
				t.Fatal("attachment did not use KEY-only physical identity", after.Constraints.Unique)
			}
		})
	}
}

func TestInheritanceUsingIndexPreparedNamespace(t *testing.T) {
	e := engine.New()
	for _, query := range []string{"CREATE TABLE p(a INT)", "CREATE TABLE c(extra TEXT) INHERITS(p)", "CREATE UNIQUE INDEX original ON p(a)", "INSERT INTO p VALUES(1)", "INSERT INTO c(a,extra) VALUES(2,'child')"} {
		ownershipUnitExec(t, e, query)
	}
	index := retiredUnitTable(t, e, "original")
	ownershipUnitExec(t, e, "ALTER TABLE p ADD CONSTRAINT renamed PRIMARY KEY USING INDEX original,ADD CONSTRAINT sees_renamed CHECK('renamed'::regclass IS NOT NULL)")
	parent, child, renamed := retiredUnitTable(t, e, "p"), retiredUnitTable(t, e, "c"), retiredUnitTable(t, e, "renamed")
	if renamed.Address() != index.Address() || renamed.SQLIndex.Constraint == nil || len(parent.Constraints.Unique) != 1 || len(child.Constraints.Unique) != 0 || len(parent.Constraints.NotNull) != 1 || len(child.Constraints.NotNull) != 1 || len(parent.Constraints.Check) != 1 || len(child.Constraints.Check) != 1 {
		t.Fatal("prepared physical rename and recursive NN/CHECK effects diverged", parent.Constraints.Unique, child.Constraints.Unique)
	}
	if parent.Inheritance != nil && !parent.Inheritance.NotNull[parent.Constraints.NotNull[0].Attribute].Local {
		t.Fatal("physical root NN lost its local contribution")
	}
}

func TestInheritanceUsingIndexPreparedDiagnostics(t *testing.T) {
	for _, kind := range []string{"rename_collision", "later_check", "skip_then_later_check"} {
		for _, canceled := range []bool{false, true} {
			t.Run(kind+"/"+map[bool]string{false: "ordered_notice_error", true: "child_admission_cancellation"}[canceled], func(t *testing.T) {
				e := engine.New()
				for _, query := range []string{"CREATE TABLE p(a INT)", "CREATE TABLE c() INHERITS(p)", "CREATE UNIQUE INDEX original ON p(a)", "CREATE TABLE collision(x INT)", "INSERT INTO p VALUES(1)", "INSERT INTO c VALUES(2)"} {
					ownershipUnitExec(t, e, query)
				}
				before := e.Catalog.State
				parent, index := retiredUnitTable(t, e, "p"), retiredUnitTable(t, e, "original")
				parentStore, indexRoot := e.Storage.Relations[parent.Address()], e.Storage.IndexRoots[index.Address()]
				waiter := enginetest.StartSession(t, e, nil)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				query := `ALTER TABLE p ADD CONSTRAINT collision PRIMARY KEY USING INDEX original`
				code, message, notice := "42P07", `relation "collision" already exists`, `ALTER TABLE / ADD CONSTRAINT USING INDEX will rename index "original" to "collision"`
				if kind != "rename_collision" {
					query = `ALTER TABLE p ADD CONSTRAINT attached PRIMARY KEY USING INDEX original,ADD CONSTRAINT invalid CHECK(missing>0)`
					code, message, notice = "42703", `column "missing" does not exist`, `ALTER TABLE / ADD CONSTRAINT USING INDEX will rename index "original" to "attached"`
				}
				if kind == "skip_then_later_check" {
					query = `ALTER TABLE p DROP CONSTRAINT IF EXISTS absent,ADD CONSTRAINT attached PRIMARY KEY USING INDEX original,ADD CONSTRAINT invalid CHECK(missing>0)`
				}
				command := enginetest.Statement(t, query)
				var notices []sqlstate.Error
				execute := func() error {
					op, err := waiter.Acquire(ctx)
					if err != nil {
						return err
					}
					_, err = e.Execute(op, command, 0, nil)
					notices = op.DetachNotices()
					op.Done()
					return err
				}
				if canceled {
					holder := enginetest.StartSession(t, e, nil)
					tx := enginetest.TransactionBegin(t, holder)
					ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "c")
					if err := e.Locks.Acquire(context.Background(), holder.Transactions.Ticket, lock.Resource{ObjectRef: ref}, lock.AccessShare); err != nil {
						t.Fatal(err)
					}
					done := make(chan error, 1)
					go func() { done <- execute() }()
					// USING's prefix waits after capture; a CHECK list can require its
					// preparatory child grant first. Neither reaches the notice's phase.
					enginetest.LockPending(t, e, 1)
					cancel()
					enginetest.LockAwait(t, done, context.Canceled)
					enginetest.LockPending(t, e, 0)
					if len(notices) != 0 {
						t.Fatal("prepared rename escaped before its provider phase", notices)
					}
					enginetest.TransactionEnd(t, holder, tx, false)
				} else {
					err := execute()
					var diagnostic *sqlstate.Error
					if !errors.As(err, &diagnostic) || diagnostic.Code != code || diagnostic.Message != message {
						t.Fatal("actual provider did not surface its captured error", err)
					}
					expected := []sqlstate.Error{}
					if kind == "skip_then_later_check" {
						expected = append(expected, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `constraint "absent" of relation "p" does not exist, skipping`})
					}
					expected = append(expected, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: notice})
					if !reflect.DeepEqual(notices, expected) {
						t.Fatal("provider notice/error ordering changed", notices, expected)
					}
				}
				if e.Catalog.State != before || e.Storage.Relations[parent.Address()] != parentStore || e.Storage.IndexRoots[index.Address()] != indexRoot || retiredUnitTable(t, e, "original") != index || retiredUnitTable(t, e, "p") != parent {
					t.Fatal("failed/canceled prepared attachment published metadata or rows")
				}
				enginetest.LockEmpty(t, e)
			})
		}
	}
}

func ownershipUnitExec(t *testing.T, e *core.Engine, q string) {
	t.Helper()
	if _, err := enginetest.Execute(e, context.Background(), enginetest.Statement(t, q), nil); err != nil {
		t.Fatal(q, err)
	}
}
