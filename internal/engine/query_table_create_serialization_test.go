package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// An actual owned source is demanded through a row-dependent predicate. The
// empty cases are not NO DATA or a folded constant-false query. Tracker
// participants, retained read evidence, catalog installation and Finish all
// use their existing owners rather than a simulated publication tracker.
func TestQueryTableCreateSerializationFrontier(t *testing.T) {
	for _, tc := range []struct {
		name          string
		empty, doomed bool
	}{
		{"healthy_empty", true, false}, {"doomed_empty", true, true},
		{"healthy_data", false, false}, {"doomed_data", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New()
			project, anonymous := queryCreateBoundSource(t, e, 1)
			sourceStage, sourceDescriptor := queryCreateStage(t, e, "source", 1)
			if err := e.stageQueryTableRows(context.Background(), sourceStage, sourceDescriptor, anonymous.anonymous, ids.TxRef{}); err != nil {
				t.Fatal(err)
			}
			queryCreatePublish(t, e, sourceStage)
			sourceRef := e.catalog.Domain.Ref(e.catalog.state.Epoch(), sourceDescriptor.Address())
			session := sessionStart(t, e, nil)
			op := transactionOperation(t, session)
			isolation := "serializable"
			if _, err := e.applySetting(op, session.settings, e.settings.Definitions()["default_transaction_isolation"], &isolation, false); err != nil {
				t.Fatal(err)
			}
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			var outcome error
			defer func() { _ = scope.Finish(outcome) }()
			statement := &parser.CreateQueryTable{Query: &parser.Select{}}
			if err = scope.gate(statement); err != nil {
				outcome = err
				t.Fatal(err)
			}
			if err = scope.snapshotPhase(statement); err != nil {
				outcome = err
				t.Fatal(err)
			}
			node := e.visibility.Serializable.Nodes[scope.txRef]
			if node == nil || !node.Captured || scope.owner.state != TxImplicit {
				t.Fatal("missing actual implicit serializable consumer")
			}
			e.mu.Lock()
			source, err := e.captureQuerySourcesLocked(scope, []queryLeaf{{ref: sourceRef, relation: sourceDescriptor, storage: e.storage.Relations[sourceRef.Address]}}, false)
			e.mu.Unlock()
			if err != nil {
				outcome = err
				t.Fatal(err)
			}
			if err = selectQuerySources(op.Context(), source); err != nil {
				source.close()
				outcome = err
				t.Fatal(err)
			}
			project.Child = &plan.SourceNode{QuerySchema: project.Schema(), Ref: sourceRef}
			usage := project.Schema()[0].Usage
			project.Expressions = []expr.Expr{plan.ColumnExpr{Info: expr.Info{Usage: usage, Property: expr.Properties{RowDependent: true}}, Origin: sourceRef, Scope: 1, Frame: 1, Slot: 0}}
			predicates := 0
			project.Child = &plan.FilterNode{QuerySchema: project.Schema(), Child: project.Child, Predicate: queryProbe(testUsage(e, sqltype.Bool), func(c *EvalContext) (sqltype.Value, error) {
				predicates++
				value, _ := c.Row[0].Integer()
				threshold := int64(2)
				if tc.empty {
					threshold = 4
				}
				return sqltype.Boolean(testUsage(e, sqltype.Bool).Type, value >= threshold), nil
			})}
			result, count, err := finalizeQuery(op.Context(), project, source, nil)
			if err != nil {
				outcome = err
				t.Fatal(err)
			}
			rows, err := result.(*finalRows).takeTableRows(op.Context())
			wanted := 2
			if tc.empty {
				wanted = 0
			}
			if err != nil || count != wanted || len(rows) != wanted || predicates != 3 {
				outcome = err
				t.Fatal(count, len(rows), predicates, err)
			}
			if _, ok := node.Reads[sourceRef.Address]; !ok {
				t.Fatal("owned runtime source omitted serializable read evidence")
			}
			var peer *visibility.SerializableTx
			if tc.doomed {
				peer = serializableUnitCycle(t, e, node)
				e.mu.Lock()
				err = e.visibility.SerializablePrecommitLocked(peer.Ref)
				if err == nil {
					e.visibility.SerializableCommittedLocked(peer.Ref, e.visibility.CommitSeq())
				}
				e.mu.Unlock()
				if err != nil || !node.Doomed {
					t.Fatal("actual cycle failed to doom consumer", err)
				}
			} else {
				peer = serializableUnitNode(t, e, true)
			}
			stage, d := queryCreateStage(t, e, "copy", 1)
			defer e.DiscardCatalogStage(stage)
			types := queryCreateTypes(e)
			beforeFrontier, beforeOrder := e.visibility.CommitSeq(), e.visibility.SerializableOrder()
			beforeRows, beforeVersions := e.storage.RowFrontier(), e.storage.VersionFrontier()
			if err = e.stageQueryTableRows(op.Context(), stage, d, rows, scope.txRef); err != nil {
				outcome = err
				t.Fatal(err)
			}
			outcome = e.publishQueryTable(scope, stage)
			if tc.doomed {
				serializableUnitDiagnostic(t, outcome, "Canceled on identification as a pivot, during commit attempt.")
				queryCreateAssertAbsent(t, e, d)
				queryCreateAssertTypes(t, e, types)
				if scope.completed || node.Committed || e.visibility.CommitSeq() != beforeFrontier || e.visibility.SerializableOrder() != beforeOrder {
					t.Fatal("doomed producer crossed installation fence")
				}
			} else {
				if outcome != nil {
					t.Fatal(outcome)
				}
				wantedFrontier := beforeFrontier
				if wanted != 0 {
					wantedFrontier++
				}
				if !scope.completed || !node.Committed || node.CommitFrontier != wantedFrontier || node.Order != beforeOrder+1 || e.visibility.SerializableOrder() != beforeOrder+1 || e.visibility.CommitSeq() != wantedFrontier {
					t.Fatal("successful producer did not finalize once at its installation frontier", node, e.visibility.CommitSeq(), e.visibility.SerializableOrder())
				}
				if len(e.storage.Relations[d.Address()].Versions) != wanted {
					t.Fatal("installed row count changed")
				}
			}
			if e.storage.RowFrontier() != beforeRows+uint64(wanted) || e.storage.VersionFrontier() != beforeVersions+uint64(wanted) || predicates != 3 {
				t.Fatal("identity reservation or producer demand replayed")
			}
			afterOrder, afterFrontier := e.visibility.SerializableOrder(), e.visibility.CommitSeq()
			if got := scope.Finish(outcome); got != outcome {
				t.Fatal("Finish changed publication outcome", got, outcome)
			}
			if got := scope.Finish(outcome); got != outcome {
				t.Fatal("repeated Finish changed publication outcome", got, outcome)
			}
			if e.visibility.SerializableOrder() != afterOrder || e.visibility.CommitSeq() != afterFrontier || !node.Ended || peer.Ended || e.visibility.Serializable.Nodes[node.Ref] != node {
				t.Fatal("Finish replayed commit or discarded overlapping evidence")
			}
			if _, ok := node.Reads[sourceRef.Address]; !ok {
				t.Fatal("Finish lost retained source evidence")
			}
		})
	}
}
