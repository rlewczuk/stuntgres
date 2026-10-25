package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
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
			e := engine.New()
			project, anonymous := queryCreateBoundSource(t, e, 1)
			sourceStage, sourceDescriptor := queryCreateStage(t, e, "source", 1)
			if err := e.StageQueryTableRows(context.Background(), sourceStage, sourceDescriptor, anonymous.Anonymous, ids.TxRef{}); err != nil {
				t.Fatal(err)
			}
			queryCreatePublish(t, e, sourceStage)
			sourceRef := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), sourceDescriptor.Address())
			sess := sessionStart(t, e, nil)
			op := transactionOperation(t, sess)
			isolation := "serializable"
			if _, err := core.EngineApplySetting(e, op, sess.Settings, e.Settings.Definitions()["default_transaction_isolation"], &isolation, false); err != nil {
				t.Fatal(err)
			}
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			var outcome error
			defer func() { _ = scope.Finish(outcome) }()
			statement := &parser.CreateQueryTable{Query: &parser.Select{}}
			if err = scope.Gate(statement); err != nil {
				outcome = err
				t.Fatal(err)
			}
			if err = core.ScopeSnapshotPhase(scope, statement); err != nil {
				outcome = err
				t.Fatal(err)
			}
			node := e.Visibility.Serializable.Nodes[scope.TxRef]
			if node == nil || !node.Captured || scope.Owner.State != session.TxImplicit {
				t.Fatal("missing actual implicit serializable consumer")
			}
			e.Mu.Lock()
			source, err := e.CaptureQuerySourcesLocked(scope, []exec.Leaf{{Ref: sourceRef, Relation: sourceDescriptor, Storage: e.Storage.Relations[sourceRef.Address]}}, false)
			e.Mu.Unlock()
			if err != nil {
				outcome = err
				t.Fatal(err)
			}
			if err = exec.SelectQuerySources(op.Context(), source); err != nil {
				source.Close()
				outcome = err
				t.Fatal(err)
			}
			project.Child = &plan.SourceNode{QuerySchema: project.Schema(), Ref: sourceRef}
			usage := project.Schema()[0].Usage
			project.Expressions = []expr.Expr{plan.ColumnExpr{Info: expr.Info{Usage: usage, Property: expr.Properties{RowDependent: true}}, Origin: sourceRef, Scope: 1, Frame: 1, Slot: 0}}
			predicates := 0
			project.Child = &plan.FilterNode{QuerySchema: project.Schema(), Child: project.Child, Predicate: queryProbe(enginetest.Usage(e, sqltype.Bool), func(c *exec.Context) (sqltype.Value, error) {
				predicates++
				value, _ := c.Row[0].Integer()
				threshold := int64(2)
				if tc.empty {
					threshold = 4
				}
				return sqltype.Boolean(enginetest.Usage(e, sqltype.Bool).Type, value >= threshold), nil
			})}
			result, count, err := exec.Finalize(op.Context(), project, source, nil)
			if err != nil {
				outcome = err
				t.Fatal(err)
			}
			rows, err := result.(*exec.FinalRows).TakeTableRows(op.Context())
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
				e.Mu.Lock()
				err = e.Visibility.SerializablePrecommitLocked(peer.Ref)
				if err == nil {
					e.Visibility.SerializableCommittedLocked(peer.Ref, e.Visibility.CommitSeq())
				}
				e.Mu.Unlock()
				if err != nil || !node.Doomed {
					t.Fatal("actual cycle failed to doom consumer", err)
				}
			} else {
				peer = serializableUnitNode(t, e, true)
			}
			stage, d := queryCreateStage(t, e, "copy", 1)
			defer e.DiscardCatalogStage(stage)
			types := queryCreateTypes(e)
			beforeFrontier, beforeOrder := e.Visibility.CommitSeq(), e.Visibility.SerializableOrder()
			beforeRows, beforeVersions := e.Storage.RowFrontier(), e.Storage.VersionFrontier()
			if err = e.StageQueryTableRows(op.Context(), stage, d, rows, scope.TxRef); err != nil {
				outcome = err
				t.Fatal(err)
			}
			outcome = core.EnginePublishQueryTable(e, scope, stage)
			if tc.doomed {
				serializableUnitDiagnostic(t, outcome, "Canceled on identification as a pivot, during commit attempt.")
				queryCreateAssertAbsent(t, e, d)
				queryCreateAssertTypes(t, e, types)
				if scope.Completed || node.Committed || e.Visibility.CommitSeq() != beforeFrontier || e.Visibility.SerializableOrder() != beforeOrder {
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
				if !scope.Completed || !node.Committed || node.CommitFrontier != wantedFrontier || node.Order != beforeOrder+1 || e.Visibility.SerializableOrder() != beforeOrder+1 || e.Visibility.CommitSeq() != wantedFrontier {
					t.Fatal("successful producer did not finalize once at its installation frontier", node, e.Visibility.CommitSeq(), e.Visibility.SerializableOrder())
				}
				if len(e.Storage.Relations[d.Address()].Versions) != wanted {
					t.Fatal("installed row count changed")
				}
			}
			if e.Storage.RowFrontier() != beforeRows+uint64(wanted) || e.Storage.VersionFrontier() != beforeVersions+uint64(wanted) || predicates != 3 {
				t.Fatal("identity reservation or producer demand replayed")
			}
			afterOrder, afterFrontier := e.Visibility.SerializableOrder(), e.Visibility.CommitSeq()
			if got := scope.Finish(outcome); got != outcome {
				t.Fatal("Finish changed publication outcome", got, outcome)
			}
			if got := scope.Finish(outcome); got != outcome {
				t.Fatal("repeated Finish changed publication outcome", got, outcome)
			}
			if e.Visibility.SerializableOrder() != afterOrder || e.Visibility.CommitSeq() != afterFrontier || !node.Ended || peer.Ended || e.Visibility.Serializable.Nodes[node.Ref] != node {
				t.Fatal("Finish replayed commit or discarded overlapping evidence")
			}
			if _, ok := node.Reads[sourceRef.Address]; !ok {
				t.Fatal("Finish lost retained source evidence")
			}
		})
	}
}

func queryCreateBoundSource(t *testing.T, e *core.Engine, width int) (*plan.ProjectNode, *exec.Source) {
	t.Helper()
	schema := make(plan.QuerySchema, width)
	expressions := make([]expr.Expr, width)
	rows := make([][]sqltype.Value, 3)
	for i := range rows {
		rows[i] = make([]sqltype.Value, width)
	}
	for i := range schema {
		usage := enginetest.Usage(e, sqltype.Int4)
		schema[i] = catalog.Column{Name: "i", Usage: usage}
		expressions[i] = plan.ColumnExpr{Info: expr.Info{Usage: usage, Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: 1, Slot: i}
		for j := range rows {
			rows[j][i], _ = sqltype.Integer(usage.Type, int64(j+1))
		}
	}
	source := &exec.Source{Anonymous: rows}
	return &plan.ProjectNode{QuerySchema: schema, Child: &plan.SourceNode{QuerySchema: schema}, Expressions: expressions, Frame: 1}, source
}
