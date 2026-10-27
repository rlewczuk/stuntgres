package tablecmds

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"time"
)

type analyzeSegment struct {
	relation *catalog.Relation
	store    *storage.Relation
	layout   []int
}

func analyzeRelation(e *core.Engine, s *core.StatementScope, target analyzeTarget, options table.AnalyzeOptions) error {
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(s.Op)
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	relation, ok := auth.Catalog.Relation(target.ref.Address)
	if !ok || target.ref.Epoch != auth.Catalog.Epoch() {
		e.Mu.Unlock()
		return ids.ErrCatalogReplaced
	}
	allowed, err := table.VacuumAllowed(s.Op.Context(), auth.View, auth, relation)
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	if !allowed {
		e.Mu.Unlock()
		analyzeWarning(s, "01000", fmt.Sprintf(`permission denied to analyze "%s", skipping it`, relation.Name()))
		return nil
	}
	if table.VacuumForeignTemp(auth.Catalog, s.Op.Session.SessionToken, relation) {
		e.Mu.Unlock()
		return nil
	}
	if relation.Foreign != nil {
		server, ok := auth.Catalog.Foreign(relation.Foreign.Server)
		if !ok {
			e.Mu.Unlock()
			return ids.ErrCatalogReplaced
		}
		wrapper, ok := auth.Catalog.Foreign(server.Wrapper)
		if !ok {
			e.Mu.Unlock()
			return ids.ErrCatalogReplaced
		}
		_, err := fdw.HandlerFor(auth.Catalog, wrapper)
		e.Mu.Unlock()
		if err != nil {
			return err
		}
		analyzeWarning(s, "01000", fmt.Sprintf(`skipping "%s" --- cannot analyze this foreign table`, relation.Name()))
		return nil
	}
	if relation.Foreign != nil || !table.VacuumHeap(relation) && relation.Partition == nil {
		e.Mu.Unlock()
		analyzeWarning(s, "01000", fmt.Sprintf(`skipping "%s" --- cannot analyze non-tables or special system tables`, relation.Name()))
		return nil
	}
	columns := make([]storage.StatisticsColumn, 0, len(relation.Columns))
	selected := make(map[string]bool)
	for _, name := range target.columns {
		if _, ok := relation.Index[name.Text]; !ok {
			e.Mu.Unlock()
			return core.SQLError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, name.Text, relation.Name()))
		}
		if selected[name.Text] {
			e.Mu.Unlock()
			return core.SQLError("42701", fmt.Sprintf(`column "%s" of relation "%s" appears more than once`, name.Text, relation.Name()))
		}
		selected[name.Text] = true
	}
	for slot, column := range relation.Columns {
		if len(target.columns) != 0 && !selected[column.Name] {
			continue
		}
		attribute := relation.Attributes[slot]
		if relation.ColumnMeta.Generations[attribute].Kind == catalog.GenerationVirtual {
			continue
		}
		columns = append(columns, storage.StatisticsColumn{Key: storage.StatisticsKey{Attribute: attribute}, Usage: column.Usage, Slot: slot, Target: s.Op.StatisticsTarget})
	}
	closure, err := analyzeClosure(auth.Catalog, relation, s.Op.Session.SessionToken)
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	capturedCatalog := auth.Catalog.State()
	qualified := relation.Name()
	if ns, ok := auth.Catalog.Namespace(relation.Namespace()); ok {
		qualified = ns.Name() + "." + qualified
	}
	extendedInputs, err := captureExtendedStatistics(s, auth.Catalog, relation, columns)
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	e.Mu.Unlock()
	// Descendant DDL is fenced by real locks. Data roots are then captured in one
	// short metadata window, and all typed comparison/collection runs detached.
	for _, child := range closure {
		if child.Address() != relation.Address() {
			if err := vacuumAcquire(s, auth.Ref(child.Address()), lock.AccessShare, options.SkipLocked); err != nil {
				return err
			}
		}
	}
	e.Mu.Lock()
	current, err := e.AuthorizationLocked(s.Op)
	if err != nil || current.Catalog.State() != capturedCatalog {
		e.Mu.Unlock()
		if err != nil {
			return err
		}
		return ids.ErrCatalogReplaced
	}
	refs := make([]ids.ObjectRef, 0, len(closure))
	segments := make([]analyzeSegment, 0, len(closure))
	for _, physical := range closure {
		if physical.Partition != nil {
			continue
		}
		store := e.PrivateStorageLocked(s.PendingCatalog(), physical.Address())
		if store == nil {
			e.Mu.Unlock()
			return ids.ErrCatalogReplaced
		}
		layout, err := catalog.InheritanceQueryLayout(relation, physical)
		if err != nil {
			e.Mu.Unlock()
			return err
		}
		refs = append(refs, current.Ref(physical.Address()))
		segments = append(segments, analyzeSegment{physical, store, layout})
	}
	view, err := s.CaptureMaintenanceDataViewsLocked(e, refs)
	e.Mu.Unlock()
	if err != nil {
		return err
	}
	defer view.Release()
	if options.Verbose {
		s.Op.EmitNotice(session.LevelInfo, sqlstate.Error{Severity: "INFO", SeverityUnlocalized: "INFO", Code: "00000", Message: fmt.Sprintf(`analyzing "%s"`, qualified)})
	}
	// Reuse the executor's real visible-head selection, including replacement
	// substitution. LiveVersion alone does not suppress a replaced predecessor.
	sources := make([]*exec.Source, len(segments))
	scratch := make([][]sqltype.Value, len(segments))
	for i, segment := range segments {
		if segment.relation.Partition != nil {
			continue
		}
		source, err := exec.CaptureSource(s.Op.Context(), view, auth.Ref(segment.relation.Address()))
		if err != nil {
			return err
		}
		sources[i] = source
		defer source.Close()
		if segment.layout != nil {
			scratch[i] = make([]sqltype.Value, len(segment.layout))
		}
	}
	extendedDelta := make(map[storage.ExtendedStatisticsKey]*storage.ExtendedStatistics)
	var hierarchy *storage.StatisticsHierarchy
	if len(closure) > 1 || relation.Partition != nil {
		hierarchy = &storage.StatisticsHierarchy{Relations: closure, Session: s.Op.Session.SessionToken}
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: s.Op.Context(), Host: core.HostOf(s), Input: s.Input, Output: s.Output, Catalog: auth.Catalog}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	evaluation.RoutineAuthority = &auth
	collect := func(inherited bool) (*storage.RelationStatistics, uint64, error) {
		inputs := columns
		for i := range inputs {
			inputs[i].Key.Inherited = inherited
		}
		collectors := make([]*storage.ExtendedCollector, len(extendedInputs))
		for i := range extendedInputs {
			input := extendedInputs[i].ExtendedInput
			input.Key.Inherited = inherited
			collectors[i], err = storage.NewExtendedCollector(input)
			if err != nil {
				return nil, 0, err
			}
		}
		stats, population, err := storage.CollectStatistics(s.Op.Context(), inputs, func(yield func([]sqltype.Value) error) error {
			for i, segment := range segments {
				if !inherited && segment.relation.Address() != relation.Address() {
					continue
				}
				source := sources[i]
				if source == nil {
					continue
				}
				for ordinal := range source.Count() {
					if err := s.Op.Context().Err(); err != nil {
						return err
					}
					values := source.VersionAt(source.RawIndex(ordinal)).Values
					if segment.layout != nil {
						for slot, physical := range segment.layout {
							scratch[i][slot] = values[physical]
						}
						values = scratch[i]
					}
					for j, collector := range collectors {
						if err := extendedInputs[j].add(&evaluation, collector, values); err != nil {
							return err
						}
					}
					if err := yield(values); err != nil {
						return err
					}
				}
			}
			return nil
		})
		if err != nil {
			return nil, 0, err
		}
		for i, collector := range collectors {
			fact, err := collector.Finish(s.Op.Context())
			if err != nil {
				return nil, 0, err
			}
			if fact != nil {
				key := extendedInputs[i].Key
				key.Inherited = inherited
				if inherited {
					fact.Hierarchy = hierarchy
				}
				extendedDelta[key] = fact
			}
		}
		return stats, population, nil
	}
	var local *storage.RelationStatistics
	var rows uint64
	if relation.Partition == nil {
		local, rows, err = collect(false)
	}
	if err != nil {
		return err
	}
	var delta map[storage.StatisticsKey]*storage.ColumnStatistics
	if local != nil {
		delta = local.Columns
	}
	var inheritedRows uint64
	if len(closure) > 1 || relation.Partition != nil {
		inherited, population, err := collect(true)
		if err != nil {
			return err
		}
		inheritedRows = population
		if inherited != nil {
			for _, fact := range inherited.Columns {
				fact.Hierarchy = hierarchy
			}
			merged := storage.MergeStatistics(local, inherited.Columns)
			delta = merged.Columns
		}
		if relation.Partition != nil {
			rows = population
		}
	}
	completed := time.Now().UTC().Truncate(time.Microsecond)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	current, err = e.AuthorizationLocked(s.Op)
	if err != nil {
		return err
	}
	now, ok := current.Catalog.Relation(relation.Address())
	if !ok || current.Catalog.State() != capturedCatalog || !index.SameRelationRowShape(now, relation) {
		return ids.ErrCatalogReplaced
	}
	for _, segment := range segments {
		if e.PrivateStorageLocked(s.PendingCatalog(), segment.relation.Address()) != segment.store {
			return ids.ErrCatalogReplaced
		}
	}
	allowed, err = table.VacuumAllowed(s.Op.Context(), current.View, current, now)
	if err != nil {
		return err
	}
	if !allowed {
		return core.SQLError("42501", fmt.Sprintf(`permission denied for table %s`, now.Name()))
	}
	if err := s.Op.Context().Err(); err != nil {
		return err
	}
	store := e.PrivateStorageLocked(s.PendingCatalog(), relation.Address())
	if relation.RelationKind.Stored() && store == nil {
		return ids.ErrCatalogReplaced
	}
	originalRelation, _ := e.Catalog.State.Relation(relation.Address())
	previous := session.ResolveRelationMetadata(s.PendingCatalog(), e.Storage.RelationMetadata, relation.Address())
	metadata := &storage.RelationMetadata{}
	if previous != nil {
		*metadata = *previous
	}
	metadata.AnalyzeCount++
	metadata.LastAnalyze = completed
	if !relation.RelationKind.Stored() {
		metadata.Scanned, metadata.Tuples = true, int64(rows)
	}
	if len(closure) > 1 || relation.Partition != nil {
		metadata.InheritedScanned, metadata.InheritedTuples = true, int64(inheritedRows)
	} else {
		metadata.InheritedScanned, metadata.InheritedTuples = false, 0
		clearInherited := func(columns map[storage.StatisticsKey]*storage.ColumnStatistics) {
			for key := range columns {
				if key.Inherited {
					if delta == nil {
						delta = make(map[storage.StatisticsKey]*storage.ColumnStatistics)
					}
					delta[key] = nil
				}
			}
		}
		if previous != nil && previous.Statistics != nil {
			clearInherited(previous.Statistics.Columns)
		}
		if pending := s.PendingCatalog(); pending != nil {
			clearInherited(pending.Statistics[relation.Address()].Columns)
		}
	}
	if s.Owner.InBlock() || s.PendingCatalog() != nil {
		if s.Catalog == nil {
			s.Catalog = s.PendingCatalog().Clone()
		}
		var private *storage.RelationMetadata
		if originalRelation == nil {
			private = metadata
		}
		s.Catalog.RecordStatistics(relation.Address(), e.Storage.Relations[relation.Address()], originalRelation, relation, delta, extendedDelta, private)
	} else {
		metadata.Statistics = storage.MergeStatistics(metadata.Statistics, delta)
		metadata.Statistics = storage.MergeExtendedStatistics(metadata.Statistics, extendedDelta)
	}
	// Existing-object maintenance survives rollback. New-object metadata stays
	// on the actual creation journal until its normal publication boundary.
	if originalRelation != nil {
		if e.Storage.RelationMetadata == nil {
			e.Storage.RelationMetadata = make(map[ids.ObjectAddress]*storage.RelationMetadata)
		}
		e.Storage.RelationMetadata[relation.Address()] = metadata
	}
	maintenance := e.Storage.Relations[relation.Address()]
	if maintenance == nil {
		maintenance = store
	}
	if maintenance != nil {
		maintenance.Maintenance.Scanned = true
		maintenance.Maintenance.Tuples = int64(rows)
		maintenance.Maintenance.Modifications = 0
	}
	if store != nil && store != maintenance {
		store.Maintenance.Scanned = true
		store.Maintenance.Tuples = int64(rows)
		store.Maintenance.Modifications = 0
	}
	return nil
}
