package engine

import (
	"context"
	"testing"

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
					e := New()
					op, done := roleOperation(t, e, "postgres")
					defer done()
					roleExecute(t, e, op, "CREATE DOMAIN policy_domain AS INTEGER")
					columnType := "INTEGER"
					if shape == "column_and_policy" {
						columnType = "policy_domain"
					}
					roleExecute(t, e, op, "CREATE TABLE t(id "+columnType+", keep INTEGER)")
					roleExecute(t, e, op, "CREATE TABLE u(id INTEGER, keep INTEGER)")
					for _, name := range []string{"t", "u"} {
						roleExecute(t, e, op, "CREATE POLICY p ON "+name+" USING(id::policy_domain>0)")
						roleExecute(t, e, op, "CREATE POLICY q ON "+name+" WITH CHECK(id::policy_domain>0)")
						roleExecute(t, e, op, "CREATE POLICY retained ON "+name+" USING(keep>0)")
					}
					base := e.CatalogRead()
					owners := []*relationDescriptor{
						base.state.descriptors[roleTableRef(t, e, "t").Address].(*relationDescriptor),
						base.state.descriptors[roleTableRef(t, e, "u").Address].(*relationDescriptor),
					}
					providers := base.Lookup(NameKey{ClassID: TypeClassID, Namespace: ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}, Name: "policy_domain"})
					if len(providers) != 1 {
						t.Fatal(providers)
					}
					var scope *StatementScope
					if authority == "sql" {
						tx, err := e.Begin(op, TxOptions{})
						if err != nil {
							t.Fatal(err)
						}
						defer func() {
							if _, err := e.Rollback(op, tx); err != nil {
								t.Error(err)
							}
							lockEmpty(t, e)
						}()
						scope = &StatementScope{op: op, owner: op.session.transactions, txRef: tx, ticket: op.session.transactions.ticket}
					}
					stage := e.BeginCatalogStage(base)
					defer e.DiscardCatalogStage(stage)
					projected := 0
					project := func(old, next *relationDescriptor) error {
						projected++
						if old != owners[0] || shape != "column_and_policy" || len(next.columns) != 1 || next.columns[0].Name != "keep" {
							t.Fatal("policy lock target became a column projection", old.name, next.columns)
						}
						return nil
					}
					if err := stageExpressionDependencies(context.Background(), stage, providers, parser.DropCascade, scope, nil, project, ObjectAddress{}); err != nil {
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
						next, ok := stage.descriptors[old.address].(*relationDescriptor)
						if !ok || len(next.policies) != 1 || next.policies[0].Name != "retained" {
							t.Fatal("wrong surviving policy owner", old.name, next)
						}
						if _, removed := stage.removals[old.address]; removed {
							t.Fatal("lock target became a physical deletion", old.name)
						}
						if len(old.policies) != 3 || e.CatalogRead().state != base.state {
							t.Fatal("staging mutated published roots")
						}
					}
					if scope == nil {
						lockEmpty(t, e)
					} else {
						e.locks.mu.Lock()
						resources := len(scope.ticket.resources)
						valid := true
						for _, old := range owners {
							ref := ObjectRef{owner: e.owner, Epoch: base.Epoch(), Address: old.address}
							entry := e.locks.entries[lockResource{ObjectRef: ref}]
							valid = valid && entry != nil && entry.holders[scope.ticket].mode == accessExclusive && len(entry.pending) == 0
						}
						e.locks.mu.Unlock()
						if !valid || resources != len(owners) {
							t.Fatal("missing/doubled captured owner grants", valid, resources)
						}
					}
				})
			}
		})
	}
}
