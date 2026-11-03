package engine

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/util"
)

func decodeRestoreGin(r *restoreRun, comment string, pos parser.Position) (bool, error) {
	record, found, err := index.DecodeGinIndex(comment)
	if found {
		if err == nil {
			r.ginIndexes = append(r.ginIndexes, record)
		}
		return true, err
	}
	row, found, err := index.DecodeGinRow(comment)
	if found {
		if err == nil {
			r.ginRows = append(r.ginRows, index.PositionedGinRow{Record: row, End: pos.Offset + len(comment)})
		}
		return true, err
	}
	if strings.HasPrefix(strings.TrimSpace(comment), "stuntgres-gin-") {
		return true, fmt.Errorf("stuntgres: unsupported native GIN metadata")
	}
	return false, nil
}
func ginRestoreRelation(view catalog.Read, schema, name string) *catalog.Relation {
	for _, descriptor := range view.Descriptors() {
		relation, ok := descriptor.(*catalog.Relation)
		if ok && relation.Name() == name {
			namespace, ok := view.Namespace(relation.Namespace())
			if ok && namespace.Name() == schema {
				return relation
			}
		}
	}
	return nil
}
func prepareRestoreGinRow(r *restoreRun, op *session.Operation, statement parser.Statement) error {
	insertion, ok := statement.(*parser.Insert)
	if !ok {
		return nil
	}
	record, ok := r.ginRowPairs[insertion]
	if !ok {
		return nil
	}
	r.stage.Mu.Lock()
	defer r.stage.Mu.Unlock()
	target := ginRestoreRelation(r.stage.Catalog.Current(), record.Schema, record.Table)
	if target == nil || r.stage.Storage.Relations[target.Address()] == nil {
		return fmt.Errorf("stuntgres: missing native GIN row target")
	}
	replay := core.OperationReplay(op)
	if !replay.StagingEffects || !replay.InsertAuthority || replay.GinInsert != nil {
		return fmt.Errorf("stuntgres: invalid native GIN row capture")
	}
	r.cur.ginInsert = core.RestoreInsertCapture{Statement: insertion, Target: r.stage.Catalog.Domain.Ref(r.stage.Catalog.State.Epoch(), target.Address()), Store: r.stage.Storage.Relations[target.Address()]}
	replay.GinInsert = &r.cur.ginInsert
	if err := op.Context().Err(); err != nil {
		replay.GinInsert = nil
		r.cur.ginInsert = core.RestoreInsertCapture{}
		return err
	}
	return nil
}
func afterRestoreGinRow(r *restoreRun, op *session.Operation, statement parser.Statement, _ control.Outcome) error {
	insertion, ok := statement.(*parser.Insert)
	if !ok {
		return nil
	}
	record, ok := r.ginRowPairs[insertion]
	if !ok {
		return nil
	}
	core.OperationReplay(op).GinInsert = nil
	r.stage.Mu.Lock()
	defer r.stage.Mu.Unlock()
	target := ginRestoreRelation(r.stage.Catalog.Current(), record.Schema, record.Table)
	capture := &r.cur.ginInsert
	defer func() { *capture = core.RestoreInsertCapture{} }()
	if err := op.Context().Err(); err != nil {
		return err
	}
	if target == nil || capture.Statement != insertion || capture.Target.Domain() != r.stage.Catalog.Domain || capture.Target.Epoch != r.stage.Catalog.State.Epoch() || capture.Target.Address != target.Address() || r.stage.Storage.Relations[target.Address()] != capture.Store {
		return fmt.Errorf("stuntgres: missing native GIN inserted target")
	}
	row, version, accepted := capture.Identity()
	if !accepted {
		return fmt.Errorf("stuntgres: native GIN row did not produce one destination identity")
	}
	identity := index.RowIdentity{Row: row, Version: version}
	if r.ginIdentities == nil {
		r.ginIdentities = make(map[[2]string]map[int]index.RowIdentity)
	}
	key := [2]string{record.Schema, record.Table}
	rows := r.ginIdentities[key]
	if rows == nil {
		rows = make(map[int]index.RowIdentity)
		r.ginIdentities[key] = rows
	}
	if _, exists := rows[record.Ordinal]; exists {
		return fmt.Errorf("stuntgres: duplicate native GIN row ordinal")
	}
	rows[record.Ordinal] = identity
	return op.Context().Err()
}
func finishRestoreGin(r *restoreRun, ctx context.Context, op *session.Operation) error {
	if len(r.ginIndexes) == 0 && len(r.ginRows) == 0 {
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.stage.Mu.Lock()
	view := r.stage.Catalog.Current()
	roots, stores, frontier := r.stage.Storage.IndexRoots, r.stage.Storage.Relations, r.stage.Visibility.CommitSeq()
	r.stage.Mu.Unlock()
	next := maps.Clone(roots)
	seen := make(map[ids.ObjectAddress]bool, len(r.ginIndexes))
	parents := make(map[[2]string]bool)
	var rebuiltParents map[ids.ObjectAddress]bool
	for _, record := range r.ginIndexes {
		target := ginRestoreRelation(view, record.Schema, record.Name)
		parent := ginRestoreRelation(view, record.ParentSchema, record.Parent)
		if target == nil || parent == nil || target.SQLIndex == nil || target.SQLIndex.Method != catalog.GinMethod || target.SQLIndex.Parent != parent.Address() || seen[target.Address()] {
			return fmt.Errorf("stuntgres: invalid native GIN index target")
		}
		seen[target.Address()] = true
		key := [2]string{record.ParentSchema, record.Parent}
		parents[key] = true
		rows := r.ginIdentities[key]
		store := stores[parent.Address()]
		if store == nil {
			return fmt.Errorf("stuntgres: missing native GIN parent storage")
		}
		actual := make(map[index.RowIdentity]bool, len(store.Versions))
		for _, row := range store.Versions {
			if visibility.LiveVersion(row, frontier, store.Ends, nil) {
				actual[index.RowIdentity{Row: row.Row, Version: row.Version}] = true
			}
		}
		if len(actual) != len(rows) {
			return fmt.Errorf("stuntgres: incomplete native GIN relational row inventory")
		}
		for ordinal := 1; ordinal <= len(rows); ordinal++ {
			identity, ok := rows[ordinal]
			if !ok || !actual[identity] {
				return fmt.Errorf("stuntgres: invalid native GIN relational row identity")
			}
			delete(actual, identity)
		}
		pending := make(map[index.RowIdentity]bool, len(record.Pending))
		for _, ordinal := range record.Pending {
			identity, ok := rows[ordinal]
			if !ok || pending[identity] {
				return fmt.Errorf("stuntgres: invalid native GIN pending ordinal")
			}
			pending[identity] = true
		}
		if parent.RelationKind == catalog.MaterializedViewRelation && !rebuiltParents[parent.Address()] {
			// CREATE INDEX replay preceded snapshot population. Matching a
			// subsequent concurrent refresh also needs the actual unique roots,
			// not just the GIN portions reconstructed below.
			for address, sibling := range view.Relations() {
				definition := sibling.SQLIndex
				if definition == nil || definition.Parent != parent.Address() || !definition.Ready || !definition.Live {
					continue
				}
				rebuilt, err := buildRestoreMaterializedIndex(r, ctx, op, parent, sibling, store)
				if err != nil {
					return err
				}
				next[address] = rebuilt
			}
			if rebuiltParents == nil {
				rebuiltParents = make(map[ids.ObjectAddress]bool)
			}
			rebuiltParents[parent.Address()] = true
		}
		source := next[target.Address()]
		root, err := index.RestoreGinSplit(ctx, view, target, source, pending, sqltype.InputEnvironment{})
		if err != nil {
			return err
		}
		next[target.Address()] = root
	}
	for _, descriptor := range view.Descriptors() {
		target, ok := descriptor.(*catalog.Relation)
		if ok && target.SQLIndex != nil && target.SQLIndex.Method == catalog.GinMethod && !seen[target.Address()] {
			return fmt.Errorf("stuntgres: missing native GIN split record")
		}
	}
	for parent := range r.ginIdentities {
		if !parents[parent] {
			return fmt.Errorf("stuntgres: native GIN row inventory without index")
		}
	}
	if err := util.ExecutionCheckpoint(util.ActivateExecutionPause(ctx), "gin.restore.prepared"); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.stage.Mu.Lock()
	defer r.stage.Mu.Unlock()
	if _, err := r.stage.AuthorizationLocked(op); err != nil {
		return err
	}
	r.stage.Storage.ReplaceIndexRootsLocked(next)
	return nil
}

// Materialized snapshot rows are installed after CREATE INDEX replay. Produce
// every affected physical sibling root from the actual typed destination heap,
// including the unique entries that concurrent refresh uses as match authority.
func buildRestoreMaterializedIndex(r *restoreRun, ctx context.Context, op *session.Operation, parent, target *catalog.Relation, store *storage.Relation) (root *storage.IndexRoot, err error) {
	scope, err := r.stage.StartStatement(op)
	if err != nil {
		return nil, err
	}
	defer func() {
		err = scope.Finish(err)
		if err != nil {
			root = nil
		}
	}()
	r.stage.Mu.Lock()
	authority, err := r.stage.AuthorizationLocked(op)
	r.stage.Mu.Unlock()
	if err != nil {
		return nil, err
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: scope.ActivateRuntime(ctx), Host: core.HostOf(scope), Input: scope.Input, Output: scope.Output, Catalog: authority.Catalog}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	evaluation.RoutineAuthority = &authority
	return index.BuildRelationIndexRoot(&evaluation, parent, target, store, nil)
}
