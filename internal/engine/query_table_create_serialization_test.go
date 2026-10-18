package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/ids"
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
			sourceRef := e.domain.Ref(e.state.epoch, sourceDescriptor.address)
			session := sessionStart(t, e, nil)
			op := transactionOperation(t, session)
			isolation := "serializable"
			if _, err := e.applySetting(op, session.settings, e.settingsDefinitions["default_transaction_isolation"], &isolation, false); err != nil {
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
			node := e.serializable.nodes[scope.txRef]
			if node == nil || !node.captured || scope.owner.state != TxImplicit {
				t.Fatal("missing actual implicit serializable consumer")
			}
			e.mu.Lock()
			source, err := e.captureQuerySourcesLocked(scope, []queryLeaf{{ref: sourceRef, relation: sourceDescriptor, storage: e.storage[sourceRef.Address]}}, false)
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
			project.child = &sourceNode{querySchema: project.Schema(), ref: sourceRef}
			usage := project.Schema()[0].Usage
			project.expressions = []BoundExpr{columnExpression{expressionInfo: expressionInfo{usage: usage, properties: ExprProperties{RowDependent: true}}, origin: sourceRef, scope: 1, frame: 1, slot: 0}}
			predicates := 0
			project.child = &filterNode{querySchema: project.Schema(), child: project.child, predicate: queryProbe(testUsage(e, sqltype.Bool), func(c *EvalContext) (sqltype.Value, error) {
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
			if _, ok := node.reads[sourceRef.Address]; !ok {
				t.Fatal("owned runtime source omitted serializable read evidence")
			}
			var peer *serializableTransaction
			if tc.doomed {
				peer = serializableUnitCycle(t, e, node)
				e.mu.Lock()
				err = e.serializablePrecommitLocked(peer.ref)
				if err == nil {
					e.serializableCommittedLocked(peer.ref, e.commitSeq)
				}
				e.mu.Unlock()
				if err != nil || !node.doomed {
					t.Fatal("actual cycle failed to doom consumer", err)
				}
			} else {
				peer = serializableUnitNode(t, e, true)
			}
			stage, d := queryCreateStage(t, e, "copy", 1)
			defer e.DiscardCatalogStage(stage)
			types := queryCreateTypes(e)
			beforeFrontier, beforeOrder := e.commitSeq, e.serializable.order
			beforeRows, beforeVersions := e.nextRow, e.nextVersion
			if err = e.stageQueryTableRows(op.Context(), stage, d, rows, scope.txRef); err != nil {
				outcome = err
				t.Fatal(err)
			}
			outcome = e.publishQueryTable(scope, stage)
			if tc.doomed {
				serializableUnitDiagnostic(t, outcome, "Canceled on identification as a pivot, during commit attempt.")
				queryCreateAssertAbsent(t, e, d)
				queryCreateAssertTypes(t, e, types)
				if scope.completed || node.committed || e.commitSeq != beforeFrontier || e.serializable.order != beforeOrder {
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
				if !scope.completed || !node.committed || node.commitFrontier != wantedFrontier || node.order != beforeOrder+1 || e.serializable.order != beforeOrder+1 || e.commitSeq != wantedFrontier {
					t.Fatal("successful producer did not finalize once at its installation frontier", node, e.commitSeq, e.serializable.order)
				}
				if len(e.storage[d.address].versions) != wanted {
					t.Fatal("installed row count changed")
				}
			}
			if e.nextRow != beforeRows+uint64(wanted) || e.nextVersion != beforeVersions+uint64(wanted) || predicates != 3 {
				t.Fatal("identity reservation or producer demand replayed")
			}
			afterOrder, afterFrontier := e.serializable.order, e.commitSeq
			if got := scope.Finish(outcome); got != outcome {
				t.Fatal("Finish changed publication outcome", got, outcome)
			}
			if got := scope.Finish(outcome); got != outcome {
				t.Fatal("repeated Finish changed publication outcome", got, outcome)
			}
			if e.serializable.order != afterOrder || e.commitSeq != afterFrontier || !node.ended || peer.ended || e.serializable.nodes[node.ref] != node {
				t.Fatal("Finish replayed commit or discarded overlapping evidence")
			}
			if _, ok := node.reads[sourceRef.Address]; !ok {
				t.Fatal("Finish lost retained source evidence")
			}
		})
	}
}
