package viewcmds_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// Exercise the real ordered closure and stage, not a second policy traversal.
// Duplicate policies and a column edge share one owner; another owner retains
// its columns. Nil scope models Commit/drained retirement's existing authority.
func TestRLSPolicyCascadeLockTargets(t *testing.T) {
	for _, authority := range []string{"sql", "nil_scope"} {
		t.Run(authority, func(t *testing.T) {
			for _, shape := range []string{"policy_only", "column_and_policy"} {
				t.Run(shape, func(t *testing.T) {
					e := engine.New()
					op, done := enginetest.RoleOperation(t, e, "postgres")
					defer done()
					enginetest.RoleExecute(t, e, op, "CREATE DOMAIN policy_domain AS INTEGER")
					columnType := "INTEGER"
					if shape == "column_and_policy" {
						columnType = "policy_domain"
					}
					enginetest.RoleExecute(t, e, op, "CREATE TABLE t(id "+columnType+", keep INTEGER)")
					enginetest.RoleExecute(t, e, op, "CREATE TABLE u(id INTEGER, keep INTEGER)")
					for _, name := range []string{"t", "u"} {
						enginetest.RoleExecute(t, e, op, "CREATE POLICY p ON "+name+" USING(id::policy_domain>0)")
						enginetest.RoleExecute(t, e, op, "CREATE POLICY q ON "+name+" WITH CHECK(id::policy_domain>0)")
						enginetest.RoleExecute(t, e, op, "CREATE POLICY retained ON "+name+" USING(keep>0)")
					}
					base := e.CatalogRead()
					owners := []*catalog.Relation{
						base.MustRelation(enginetest.RoleTableRef(t, e, "t").Address),
						base.MustRelation(enginetest.RoleTableRef(t, e, "u").Address),
					}
					providers := base.Lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "policy_domain"})
					if len(providers) != 1 {
						t.Fatal(providers)
					}
					var scope *core.StatementScope
					if authority == "sql" {
						tx, err := e.Begin(op, session.TxOptions{})
						if err != nil {
							t.Fatal(err)
						}
						defer func() {
							if _, err := e.Rollback(op, tx); err != nil {
								t.Error(err)
							}
							enginetest.LockEmpty(t, e)
						}()
						scope = &core.StatementScope{Op: op, Owner: op.Session.Transactions, TxRef: tx, LockTicket: op.Session.Transactions.Ticket}
					}
					stage := e.BeginCatalogStage(base)
					defer e.DiscardCatalogStage(stage)
					projected := 0
					project := func(old, next *catalog.Relation) error {
						projected++
						if old != owners[0] || shape != "column_and_policy" || len(next.Columns) != 1 || next.Columns[0].Name != "keep" {
							t.Fatal("policy lock target became a column projection", old.Name(), next.Columns)
						}
						return nil
					}
					if err := core.StageExpressionDependencies(context.Background(), stage, providers, parser.DropCascade, scope, nil, project, ids.ObjectAddress{}); err != nil {
						t.Fatal(err)
					}
					wantProjected := 0
					if shape == "column_and_policy" {
						wantProjected = 1
					}
					if projected != wantProjected {
						t.Fatal("wrong projection count", projected)
					}
					for _, old := range owners {
						next, ok := stage.Get(old.Address()).(*catalog.Relation)
						if !ok || len(next.Security.Policies) != 1 || next.Security.Policies[0].Name != "retained" {
							t.Fatal("wrong surviving policy owner", old.Name(), next)
						}
						if _, removed := stage.Removal(old.Address()); removed {
							t.Fatal("lock target became a physical deletion", old.Name())
						}
						if len(old.Security.Policies) != 3 || e.CatalogRead().State() != base.State() {
							t.Fatal("staging mutated published roots")
						}
					}
					if scope == nil {
						enginetest.LockEmpty(t, e)
					} else {
						lockState := e.Locks.Inspect(scope.LockTicket)
						resources := len(lockState.Journals[scope.LockTicket])
						valid := true
						for _, old := range owners {
							ref := e.Catalog.Domain.Ref(base.Epoch(), old.Address())
							entry := lockState.Resources[lock.Resource{ObjectRef: ref}]
							valid = valid && entry != nil && entry.Holders[scope.LockTicket].Mode == lock.AccessExclusive && len(entry.Pending) == 0
						}
						if !valid || resources != len(owners) {
							t.Fatal("missing/doubled captured owner grants", valid, resources)
						}
					}
				})
			}
		})
	}
}
