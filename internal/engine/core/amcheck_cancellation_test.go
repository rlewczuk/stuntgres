package core_test

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"runtime"
	"strings"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// Cancellation comes from the real context after the installed call reaches an
// actual comparison, without a production observer or a timing assumption.
type amcheckComparisonContext struct {
	context.Context
	cancel       context.CancelFunc
	entered      bool
	work         func()
	match        string
	skip         int
	directCaller string
	atEntry      bool
}

func (c *amcheckComparisonContext) Err() error {
	if err := c.Context.Err(); err != nil {
		return err
	}
	var pcs [24]uintptr
	count := runtime.Callers(2, pcs[:])
	frames := runtime.CallersFrames(pcs[:count])
	previous := ""
	for {
		frame, more := frames.Next()
		match := c.match
		if match == "" {
			match = "index.Ordering.CompareKeys"
		}
		if !c.entered && strings.Contains(frame.Function, match) && (!c.atEntry || previous == "") && (c.directCaller == "" || strings.HasSuffix(previous, c.directCaller)) {
			if c.skip > 0 {
				c.skip--
				break
			}
			c.entered = true
			if c.work != nil {
				c.work()
			}
			if c.cancel != nil {
				c.cancel()
			}
			break
		}
		if !more {
			break
		}
		previous = frame.Function
	}
	return c.Context.Err()
}
func TestAmcheckCancellation(t *testing.T) {
	e, d, root := amcheckUnitRoot(t, false)
	n := amcheckUnitExpression(t, e, true)
	reader, ticket := blockingUnitOwner(t, e)
	scope := pgrowlocksUnitScope(t, e, reader)
	scope.RoutineDependencies = []catalog.ExpressionDependency{{Ref: n.Ref, Revision: n.Procedure.Revision()}}
	e.Mu.Lock()
	auth, err := core.ScopeWriteAuthorizationLocked(scope, e)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	scope.RoutineAuth = auth
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	active := &amcheckComparisonContext{Context: ctx, cancel: cancel}
	scope.Op.Ctx = active
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: active, Host: core.HostOf(scope), Catalog: auth.Catalog, Input: scope.Input, Output: scope.Output}}
	_, err = n.Eval(&evaluation)
	if !active.entered || !errors.Is(err, context.Canceled) {
		t.Fatal("installed comparison not canceled", active.entered, err)
	}
	for _, resource := range []lock.Resource{{ObjectRef: e.Catalog.Domain.Ref(auth.Catalog.Epoch(), d.Address())}, {ObjectRef: e.Catalog.Domain.Ref(auth.Catalog.Epoch(), d.SQLIndex.Parent)}} {
		if e.Locks.HasRelationMode(ticket, resource, lock.Share) || e.Locks.HasRelationMode(ticket, resource, lock.AccessShare) {
			t.Fatal("active cancellation leaked function locks")
		}
	}
	if len(root.Entries) != 5 || e.Storage.IndexRoots[d.Address()] != root {
		t.Fatal("cancellation changed root")
	}
}

func TestAmcheckCoherentCapture(t *testing.T) {
	e, d, root := amcheckUnitRoot(t, false)
	n := amcheckUnitExpression(t, e, false, true, true)
	reader, _ := blockingUnitOwner(t, e)
	sessionSQL(t, reader, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ")
	before := sessionSQL(t, reader, "SELECT n FROM t")
	if rows := enginetest.Tuples(before); len(rows) != 5 {
		t.Fatal(rows)
	}
	scope := pgrowlocksUnitScope(t, e, reader)
	scope.RoutineDependencies = []catalog.ExpressionDependency{{Ref: n.Ref, Revision: n.Procedure.Revision()}}
	e.Mu.Lock()
	auth, err := core.ScopeWriteAuthorizationLocked(scope, e)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	scope.RoutineAuth = auth
	active := &amcheckComparisonContext{Context: context.Background(), work: func() { enginetest.Run(t, e, "INSERT INTO t VALUES(4,'d')") }}
	scope.Op.Ctx = active
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: active, Host: core.HostOf(scope), Catalog: auth.Catalog, Input: scope.Input, Output: scope.Output}}
	value, err := n.Eval(&evaluation)
	if err != nil || value.IsNull() || !active.entered {
		t.Fatal("coherent old root/store was rejected by ordinary publication", value, err)
	}
	if e.Storage.IndexRoots[d.Address()] == root || len(e.Storage.IndexRoots[d.Address()].Entries) != 6 || len(root.Entries) != 5 {
		t.Fatal("writer did not publish independently or mutated old root")
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	scope.Op.Done()
	if rows := enginetest.Tuples(sessionSQL(t, reader, "SELECT n FROM t")); len(rows) != 5 {
		t.Fatal("verification overwrote outer RR snapshot", rows)
	}
}

func TestAmcheckPrivateCancellation(t *testing.T) {
	for _, lane := range []string{"implicit", "produced", "cross_unique"} {
		t.Run(lane, func(t *testing.T) {
			e, d, root := amcheckUnitRoot(t, true)
			reader, ticket := blockingUnitOwner(t, e)
			sessionSQL(t, reader, "INSERT INTO t VALUES(3,'c'),(4,'d')")
			if lane == "produced" {
				sessionSQL(t, reader, "CREATE INDEX produced_i ON t(lower(s)) WHERE n>1")
				for _, row := range reader.Transactions.Writes[d.SQLIndex.Parent] {
					for _, output := range row.IndexOutputs {
						if output.Index.Name() == "produced_i" {
							d = output.Index
						}
					}
				}
				if d.Name() != "produced_i" {
					t.Fatal("missing actual DDL output")
				}
				root = index.EffectiveRoot(e.Storage.IndexRoots, reader.Transactions.Catalog, d.Address())
			}
			rows := reader.Transactions.Writes[d.SQLIndex.Parent]
			for _, row := range rows {
				output, produced := row.IndexOutputs[d.Address()]
				if lane == "produced" {
					if !produced || !output.Member || !index.OutputMatches(output, d) || index.RowMaintainsIndex(row, d) {
						t.Fatal("fixture lacks pre-index DDL-produced authority")
					}
				} else if produced || !index.RowMaintainsIndex(row, d) {
					t.Fatal("fixture is not the genuine implicit private lane")
				}
			}
			beforeRows := append([]storage.RowVersion(nil), rows...)
			for i := range beforeRows {
				beforeRows[i].Values = append([]sqltype.Value(nil), rows[i].Values...)
				beforeRows[i].IndexOutputs = maps.Clone(rows[i].IndexOutputs)
				for address, output := range beforeRows[i].IndexOutputs {
					output.Keys = append([]sqltype.Value(nil), output.Keys...)
					beforeRows[i].IndexOutputs[address] = output
				}
			}
			beforeRoot := amcheckUnitClone(root)
			beforeRoot.Membership = maps.Clone(root.Membership)
			beforeRoot.RowEntries = maps.Clone(root.RowEntries)
			for i := range beforeRoot.Entries {
				beforeRoot.Entries[i].Keys = append([]sqltype.Value(nil), root.Entries[i].Keys...)
			}
			scope := pgrowlocksUnitScope(t, e, reader)
			n := amcheckUnitExpression(t, e, false, false, lane == "cross_unique")
			scope.RoutineDependencies = []catalog.ExpressionDependency{{Ref: n.Ref, Revision: n.Procedure.Revision()}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			active := &amcheckComparisonContext{Context: ctx, cancel: cancel, match: "ExecutionPreparation).PredicateMatches", directCaller: "exec.EvaluationContext"}
			flags := [3]bool{}
			if lane == "produced" {
				flags[0] = true
				// Each visible committed row reaches the heap recipe once;
				// the next recipe is the first real retained private output.
				active.skip = len(e.Storage.Relations[d.SQLIndex.Parent].Versions)
			}
			if lane == "cross_unique" {
				active.match = "index.Ordering.CompareKeys"
				active.directCaller = ""
				active.atEntry = true
				// Three baseline entries: two ordering comparisons and one
				// non-NULL live uniqueness pair, then overlay-to-root probing.
				active.skip = 3
				flags[1] = true
			}
			scope.Op.Ctx = active
			beforeLocks := e.Locks.Inspect(ticket)
			err := core.HostOf(scope).Amcheck(n, uint32(d.Address().ObjectID), flags)
			if !active.entered || active.skip != 0 || err != context.Canceled {
				t.Fatal("private lane exact cancellation", lane, active.entered, active.skip, err)
			}
			if !reflect.DeepEqual(root, beforeRoot) || !reflect.DeepEqual(rows, beforeRows) || index.EffectiveRoot(e.Storage.IndexRoots, reader.Transactions.Catalog, d.Address()) != root {
				t.Fatal("cancellation changed captured authority")
			}
			if !reflect.DeepEqual(beforeLocks, e.Locks.Inspect(ticket)) {
				t.Fatal("private cancellation changed preexisting lock ownership")
			}
			if scope.Op.Restricted != nil {
				t.Fatal("private cancellation leaked restricted owner")
			}
		})
	}
}
