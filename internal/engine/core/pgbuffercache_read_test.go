package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

type bufferCapture struct {
	op     *session.Operation
	scope  *core.StatementScope
	source *exec.Source
	root   plan.Node
	closed bool
}

func bufferUnitEngine(t *testing.T) *core.Engine {
	t.Helper()
	e := engine.New()
	enginetest.Run(t, e, "CREATE EXTENSION pg_buffercache")
	return e
}
func bufferUnitCapture(t *testing.T, e *core.Engine, s *session.Session, query string) *bufferCapture {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	capture := &bufferCapture{op: op, scope: scope}
	t.Cleanup(func() { capture.close(t) })
	ast := enginetest.Statement(t, query)
	if _, err := scope.Describe(ast); err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	var binding analyze.Binding
	if err == nil {
		binding, err = core.Describe(op.Ctx, e.AnalysisHost(op), ast, auth, scope.LockedRef, scope.Input, nil, &scope.QueryLockAdmission)
	}
	if err == nil {
		capture.source, err = e.CaptureQuerySourcesLocked(scope, binding.QuerySources, len(binding.QuerySources) == 0)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	capture.root = binding.Query.Root
	return capture
}
func (c *bufferCapture) close(t *testing.T) {
	t.Helper()
	if c.closed {
		return
	}
	c.closed = true
	if c.source != nil {
		c.source.Close()
	}
	if err := c.scope.Finish(nil); err != nil {
		t.Error(err)
	}
	c.op.Done()
}
func bufferUnitCursor(t *testing.T, e *core.Engine, s *session.Session) expr.SourceCursor {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(nil)
	scope.Owner.Mu.Lock()
	core.ScopeAttachTransactionLocked(scope)
	scope.Owner.Mu.Unlock()
	core.SetScopeWorked(scope, true)
	view := e.CatalogRead()
	relation := view.MustRelation(enginetest.CatalogRef(t, e, view, "pg_buffercache").Address)
	shape, ok := view.LookupType(relation.RowType)
	if !ok {
		t.Fatal("ordinary view lost rowtype")
	}
	cursor, err := scope.WriteHost().Contrib().PgBuffercache(shape)
	if err != nil {
		t.Fatal(err)
	}
	return cursor
}
func bufferUnitFacts(t *testing.T, e *core.Engine, s *session.Session) map[ids.OID][2]int64 {
	t.Helper()
	cursor := bufferUnitCursor(t, e, s)
	defer cursor.Close()
	facts := map[ids.OID][2]int64{}
	for {
		row, present, err := cursor.Next(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !present {
			break
		}
		if row.CompositeField(1).IsNull() {
			continue
		}
		node, err := row.CompositeField(1).OID()
		if err != nil {
			t.Fatal(err)
		}
		usage, err := row.CompositeField(7).Integer()
		if err != nil {
			t.Fatal(err)
		}
		pins, err := row.CompositeField(8).Integer()
		if err != nil {
			t.Fatal(err)
		}
		facts[ids.OID(node)] = [2]int64{usage, pins}
	}
	return facts
}
func bufferUnitNode(t *testing.T, e *core.Engine, name string) ids.OID {
	t.Helper()
	view := e.CatalogRead()
	relation := view.MustRelation(enginetest.CatalogRef(t, e, view, name).Address)
	return relation.EffectiveStorageLocator(view)
}
func bufferUnitFact(t *testing.T, e *core.Engine, s *session.Session, node ids.OID, usage, pins int64) {
	t.Helper()
	facts := bufferUnitFacts(t, e, s)
	got, present := facts[node]
	if usage == 0 {
		if present {
			t.Fatal("unreached incarnation occupied", node, got)
		}
	} else if !present || got != [2]int64{usage, pins} {
		t.Fatal("actual source ownership", node, got, present, usage, pins)
	}
}

func TestPgBuffercacheReadAttribution(t *testing.T) {
	t.Run("actual_open_borrowed_invocation_and_rewind", func(t *testing.T) {
		e := bufferUnitEngine(t)
		enginetest.Run(t, e, "CREATE TABLE buffer_owner(k integer)")
		enginetest.Run(t, e, "INSERT INTO buffer_owner VALUES(1),(2)")
		owner, observer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
		capture := bufferUnitCapture(t, e, owner, "SELECT k FROM buffer_owner")
		if capture.source.ReadLease == nil || capture.source.View == nil {
			t.Fatal("admission omitted real storage owner")
		}
		node := bufferUnitNode(t, e, "buffer_owner")
		bufferUnitFact(t, e, observer, node, 0, 0)
		if err := exec.SelectQuerySources(capture.op.Ctx, capture.source); err != nil {
			t.Fatal(err)
		}
		exec.AdmitScan(capture.root, capture.source)
		state := exec.State{Source: capture.source, SourceMode: exec.SourceBorrowed, Retain: true}
		stage, err := exec.OpenNode(capture.op.Ctx, &state, capture.root)
		if err != nil {
			t.Fatal(err)
		}
		defer stage.Close()
		bufferUnitFact(t, e, observer, node, 1, 1)
		if _, err := stage.Rewind(capture.op.Ctx); err != nil {
			t.Fatal(err)
		}
		bufferUnitFact(t, e, observer, node, 1, 1)
		again, err := exec.OpenNode(capture.op.Ctx, &state, capture.root)
		if err != nil {
			t.Fatal(err)
		}
		if err := again.Close(); err != nil {
			t.Fatal(err)
		}
		bufferUnitFact(t, e, observer, node, 2, 1)
		if err := state.FinishSources(nil); err != nil {
			t.Fatal(err)
		}
		bufferUnitFact(t, e, observer, node, 2, 1)
		if err := stage.Close(); err != nil {
			t.Fatal(err)
		}
		state.SourceMode = exec.SourceOwned
		if err := state.FinishSources(context.Canceled); err != context.Canceled {
			t.Fatal("primary failure lost", err)
		}
		bufferUnitFact(t, e, observer, node, 2, 0)
		capture.close(t)
		if capture.source.ReadLease != nil || capture.source.View != nil || capture.source.Host != nil || capture.source.Authority != nil || stage.Source != nil {
			t.Fatal("terminal capture retained live resources")
		}
	})
	t.Run("reached_hierarchy_prefix_only", func(t *testing.T) {
		e := bufferUnitEngine(t)
		for _, q := range []string{"CREATE TABLE buffer_parent(k integer)", "CREATE TABLE buffer_child() INHERITS(buffer_parent)", "INSERT INTO buffer_parent VALUES(1)", "INSERT INTO buffer_child VALUES(2)"} {
			enginetest.Run(t, e, q)
		}
		owner, observer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
		capture := bufferUnitCapture(t, e, owner, "SELECT k FROM buffer_parent")
		if err := exec.SelectQuerySources(capture.op.Ctx, capture.source); err != nil {
			t.Fatal(err)
		}
		if len(capture.source.Inheritance) != 2 {
			t.Fatal("genuine hierarchy fixture", capture.source.Inheritance)
		}
		state := exec.State{Source: capture.source, SourceMode: exec.SourceBorrowed, Retain: true}
		stage, err := exec.OpenNode(capture.op.Ctx, &state, capture.root.(*plan.ProjectNode).Child)
		if err != nil {
			t.Fatal(err)
		}
		defer stage.Close()
		first, second := capture.source.Inheritance[0].Leaf.Relation, capture.source.Inheritance[1].Leaf.Relation
		firstNode, secondNode := first.EffectiveStorageLocator(e.CatalogRead()), second.EffectiveStorageLocator(e.CatalogRead())
		bufferUnitFact(t, e, observer, firstNode, 0, 0)
		row := make([]sqltype.Value, len(stage.Schema()))
		if present, err := stage.Next(capture.op.Ctx, row); err != nil || !present {
			t.Fatal("physical prefix not reached", present, err)
		}
		bufferUnitFact(t, e, observer, firstNode, 1, 1)
		bufferUnitFact(t, e, observer, secondNode, 0, 0)
		if present, err := stage.Next(capture.op.Ctx, row); err != nil || !present {
			t.Fatal("second prefix not reached", present, err)
		}
		bufferUnitFact(t, e, observer, secondNode, 1, 1)
		if _, err := stage.Rewind(capture.op.Ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := stage.Next(capture.op.Ctx, row); err != nil {
			t.Fatal(err)
		}
		bufferUnitFact(t, e, observer, firstNode, 1, 1)
		stage.Close()
		capture.close(t)
		bufferUnitFact(t, e, observer, firstNode, 1, 0)
		bufferUnitFact(t, e, observer, secondNode, 1, 0)
	})
	t.Run("only_consumed_index_heats", func(t *testing.T) {
		e := bufferUnitEngine(t)
		for _, q := range []string{"CREATE TABLE buffer_index_heap(k integer)", "INSERT INTO buffer_index_heap VALUES(1),(2)", "CREATE INDEX buffer_hash ON buffer_index_heap USING hash(k)"} {
			enginetest.Run(t, e, q)
		}
		owner, observer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
		indexNode := bufferUnitNode(t, e, "buffer_hash")
		capture := bufferUnitCapture(t, e, owner, "SELECT k FROM buffer_index_heap")
		if len(capture.source.HashIndexes) != 1 || capture.source.HashIndexes[0].ReadLease == nil {
			t.Fatal("actual index capture missing")
		}
		exec.AdmitScan(capture.root, capture.source)
		state := exec.State{Source: capture.source, Retain: true}
		stage, err := exec.OpenNode(capture.op.Ctx, &state, capture.root)
		if err != nil {
			t.Fatal(err)
		}
		bufferUnitFact(t, e, observer, indexNode, 0, 0)
		stage.Close()
		capture.close(t)
		enginetest.SessionSQL(t, owner, "SET enable_seqscan=off")
		capture = bufferUnitCapture(t, e, owner, "SELECT k FROM buffer_index_heap WHERE k=1")
		if err := exec.SelectQuerySources(capture.op.Ctx, capture.source); err != nil {
			t.Fatal(err)
		}
		exec.AdmitScan(capture.root, capture.source)
		if capture.source.ScanDecision.Method != plan.HashCandidates {
			t.Fatal("fixture did not choose genuine hash consumer", capture.source.ScanDecision)
		}
		state = exec.State{Source: capture.source, HashFilter: exec.HashQueryFilter(capture.root), Retain: true}
		stage, err = exec.OpenNode(capture.op.Ctx, &state, capture.root)
		if err != nil {
			t.Fatal(err)
		}
		bufferUnitFact(t, e, observer, indexNode, 1, 1)
		stage.Close()
		capture.close(t)
		bufferUnitFact(t, e, observer, indexNode, 1, 0)
	})
	t.Run("partition_pruning_reaches_only_selected_storage", func(t *testing.T) {
		e := bufferUnitEngine(t)
		for _, q := range []string{
			"CREATE TABLE buffer_partition(k integer) PARTITION BY RANGE(k)",
			"CREATE TABLE buffer_low PARTITION OF buffer_partition FOR VALUES FROM(0) TO(2)",
			"CREATE TABLE buffer_high PARTITION OF buffer_partition FOR VALUES FROM(2) TO(4)",
			"INSERT INTO buffer_partition VALUES(1),(3)",
		} {
			enginetest.Run(t, e, q)
		}
		owner, observer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
		capture := bufferUnitCapture(t, e, owner, "SELECT k FROM buffer_partition WHERE k=1")
		if capture.source.BranchPruner == nil || len(capture.source.Inheritance) != 2 {
			t.Fatal("missing actual partition/pruning capture")
		}
		if err := exec.SelectQuerySources(capture.op.Ctx, capture.source); err != nil {
			t.Fatal(err)
		}
		state := exec.State{Source: capture.source, SourceMode: exec.SourceBorrowed, Retain: true}
		stage, err := exec.OpenNode(capture.op.Ctx, &state, capture.root)
		if err != nil {
			t.Fatal(err)
		}
		defer stage.Close()
		if err := stage.Drain(capture.op.Ctx); err != nil {
			t.Fatal(err)
		}
		low, high := bufferUnitNode(t, e, "buffer_low"), bufferUnitNode(t, e, "buffer_high")
		bufferUnitFact(t, e, observer, low, 1, 1)
		bufferUnitFact(t, e, observer, high, 0, 0)
		stage.Close()
		capture.close(t)
		bufferUnitFact(t, e, observer, low, 1, 0)
	})
	t.Run("sampling_read_frontier", func(t *testing.T) {
		for _, kind := range []string{"heap", "hierarchy"} {
			t.Run(kind, func(t *testing.T) {
				e := bufferUnitEngine(t)
				enginetest.Run(t, e, "CREATE EXTENSION tsm_system_rows")
				enginetest.Run(t, e, "CREATE TABLE buffer_sample(k integer)")
				enginetest.Run(t, e, "INSERT INTO buffer_sample VALUES(1)")
				nodes := []ids.OID{bufferUnitNode(t, e, "buffer_sample")}
				if kind == "hierarchy" {
					enginetest.Run(t, e, "CREATE TABLE buffer_sample_child() INHERITS(buffer_sample)")
					enginetest.Run(t, e, "INSERT INTO buffer_sample_child VALUES(1)")
					nodes = append(nodes, bufferUnitNode(t, e, "buffer_sample_child"))
				}
				owner, observer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
				facts := func(usage, pins int64) {
					t.Helper()
					for _, node := range nodes {
						bufferUnitFact(t, e, observer, node, usage, pins)
					}
				}
				open := func(count string) (*bufferCapture, *exec.State, *exec.Stage) {
					t.Helper()
					capture := bufferUnitCapture(t, e, owner, "SELECT k FROM buffer_sample TABLESAMPLE system_rows("+count+")")
					if err := exec.SelectQuerySources(capture.op.Ctx, capture.source); err != nil {
						t.Fatal(err)
					}
					project, ok := capture.root.(*plan.ProjectNode)
					if !ok {
						t.Fatalf("sample fixture lost project: %T", capture.root)
					}
					source, ok := project.Child.(*plan.SourceNode)
					if !ok || source.Sample == nil || capture.source.Count() != len(nodes) {
						t.Fatal("sample fixture did not retain a genuine nonempty physical source", project.Child, capture.source.Count())
					}
					if capture.source.Inheritance == nil {
						if capture.source.ReadLease == nil {
							t.Fatal("sample lost admitted physical read eligibility")
						}
					} else {
						for _, segment := range capture.source.Inheritance {
							if segment.Prefix.ReadLease == nil {
								t.Fatal("sample lost physical hierarchy eligibility")
							}
						}
					}
					exec.AdmitScan(capture.root, capture.source)
					state := &exec.State{Source: capture.source, SourceMode: exec.SourceBorrowed, Retain: true}
					stage, err := exec.OpenNode(capture.op.Ctx, state, source)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() {
						if err := stage.Close(); err != nil {
							t.Error(err)
						}
					})
					return capture, state, &stage
				}
				rows := func(capture *bufferCapture, stage *exec.Stage, want int) {
					t.Helper()
					row := make([]sqltype.Value, len(stage.Schema()))
					got := 0
					for {
						present, err := stage.Next(capture.op.Ctx, row)
						if err != nil {
							t.Fatal(err)
						}
						if !present {
							break
						}
						value, err := row[0].Integer()
						if err != nil || value != 1 {
							t.Fatal("sample returned a nonfixture row", row, err)
						}
						got++
					}
					if got != want {
						t.Fatal("sample physical relation cardinality", got, want)
					}
				}

				cold, coldState, zero := open("0")
				facts(0, 0) // Initializing a zero sample must not open a read lease.
				rows(cold, zero, 0)
				if _, err := zero.Rewind(cold.op.Ctx); err != nil {
					t.Fatal(err)
				}
				rows(cold, zero, 0)
				facts(0, 0)
				if err := zero.Close(); err != nil {
					t.Fatal(err)
				}
				if err := coldState.FinishSources(nil); err != nil {
					t.Fatal(err)
				}
				cold.close(t)
				facts(0, 0)

				positive, positiveState, sampled := open("1")
				facts(1, 1) // Positive sampling actually consumes each physical heap.
				rows(positive, sampled, len(nodes))
				if _, err := sampled.Rewind(positive.op.Ctx); err != nil {
					t.Fatal(err)
				}
				rows(positive, sampled, len(nodes))
				facts(1, 1) // Same-stage rewind is not another read invocation.
				project := positive.root.(*plan.ProjectNode)
				fresh, err := exec.OpenNode(positive.op.Ctx, positiveState, project.Child)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := fresh.Close(); err != nil {
						t.Error(err)
					}
				})
				rows(positive, &fresh, len(nodes))
				facts(2, 1) // A fresh opening increments usage, not distinct backend pins.
				if err := fresh.Close(); err != nil {
					t.Fatal(err)
				}
				if err := sampled.Close(); err != nil {
					t.Fatal(err)
				}
				if err := positiveState.FinishSources(nil); err != nil {
					t.Fatal(err)
				}
				facts(2, 1) // Successful borrowing does not release the capture owner's pin.
				positive.close(t)
				facts(2, 0)

				warm, warmState, warmZero := open("0")
				facts(2, 0) // Warm zero sampling must not increment usage or acquire a pin.
				rows(warm, warmZero, 0)
				if _, err := warmZero.Rewind(warm.op.Ctx); err != nil {
					t.Fatal(err)
				}
				rows(warm, warmZero, 0)
				facts(2, 0)
				if err := warmZero.Close(); err != nil {
					t.Fatal(err)
				}
				if err := warmState.FinishSources(nil); err != nil {
					t.Fatal(err)
				}
				warm.close(t)
				facts(2, 0)

				later, laterState, laterPositive := open("1")
				rows(later, laterPositive, len(nodes))
				facts(3, 1)
				if err := laterPositive.Close(); err != nil {
					t.Fatal(err)
				}
				if err := laterState.FinishSources(nil); err != nil {
					t.Fatal(err)
				}
				later.close(t)
				facts(3, 0)
			})
		}
	})
}
