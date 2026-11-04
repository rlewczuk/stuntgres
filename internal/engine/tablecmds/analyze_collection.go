package tablecmds

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func analyzeColumns(s *core.StatementScope, relation *catalog.Relation, target analyzeTarget) ([]storage.StatisticsColumn, error) {
	selected := make(map[string]bool, len(target.columns))
	for _, name := range target.columns {
		if _, ok := relation.Index[name.Text]; !ok {
			return nil, core.SQLError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, name.Text, relation.Name()))
		}
		if selected[name.Text] {
			return nil, core.SQLError("42701", fmt.Sprintf(`column "%s" of relation "%s" appears more than once`, name.Text, relation.Name()))
		}
		selected[name.Text] = true
	}
	columns := make([]storage.StatisticsColumn, 0, len(relation.Columns))
	for slot, column := range relation.Columns {
		attribute := relation.Attributes[slot]
		if len(selected) != 0 && !selected[column.Name] || relation.ColumnMeta.Generations[attribute].Kind == catalog.GenerationVirtual {
			continue
		}
		target := s.Op.StatisticsTarget
		if config := relation.ColumnMeta.Statistics[attribute]; config.TargetSet {
			target = int(config.Target)
		}
		if target < 0 || target > 10000 {
			return nil, fmt.Errorf("stuntgres: invalid statistics target")
		}
		// Extended simple keys use the same admitted scalar set. A disabled
		// scalar retains its old facts and cannot admit an extended object.
		if target == 0 {
			continue
		}
		columns = append(columns, storage.StatisticsColumn{Key: storage.StatisticsKey{Attribute: attribute}, Usage: column.Usage, Slot: slot, Target: target})
	}
	return columns, nil
}

func (r *analyzeRun) close() {
	for _, segment := range r.segments {
		if segment.source != nil {
			segment.source.Close()
		}
	}
}

// Capture each native source once. Its view owns the statement's genuine visible
// roots, including own replacement substitution, independently of query caches.
func (r *analyzeRun) captureNative(relations []*catalog.Relation) error {
	if len(relations) == 0 {
		return nil
	}
	r.e.Mu.Lock()
	if err := r.authorityLocked(); err != nil {
		r.e.Mu.Unlock()
		return err
	}
	refs := make([]ids.ObjectRef, len(relations))
	segments := make([]*analyzeSegment, len(relations))
	for i, relation := range relations {
		store := r.e.PrivateStorageLocked(r.s.PendingCatalog(), relation.Address())
		if store == nil {
			r.e.Mu.Unlock()
			return ids.ErrCatalogReplaced
		}
		layout, err := catalog.InheritanceQueryLayout(r.relation, relation)
		if err != nil {
			r.e.Mu.Unlock()
			return err
		}
		refs[i] = r.auth.Ref(relation.Address())
		segments[i] = &analyzeSegment{relation: relation, store: store, layout: layout, dead: store.Maintenance.Dead}
	}
	view, err := r.s.CaptureMaintenanceDataViewsLocked(r.e, refs)
	r.e.Mu.Unlock()
	if err != nil {
		return err
	}
	defer view.Release()
	for i, segment := range segments {
		source, err := exec.CaptureSource(r.s.Op.Context(), view, refs[i])
		if err != nil {
			return err
		}
		segment.source = source
		r.segments = append(r.segments, segment)
	}
	return nil
}

// The same projected accepted row enters scalar and extended collectors. Mixed
// sampling changes the population estimate, never literal input multiplicity.
func (r *analyzeRun) collect(inherited bool, visits []*analyzeSegment, sampled *float64) (*storage.RelationStatistics, uint64, error) {
	core.HostOf(r.s).ReleaseReplacementRead()
	columns := r.columns
	for i := range columns {
		columns[i].Key.Inherited = inherited
		columns[i].SamplePopulation = sampled
	}
	collectors := make([]*storage.ExtendedCollector, len(r.extended))
	for i := range r.extended {
		input := r.extended[i].ExtendedInput
		input.Key.Inherited = inherited
		input.SamplePopulation = sampled
		var err error
		collectors[i], err = storage.NewExtendedCollector(input)
		if err != nil {
			return nil, 0, err
		}
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: r.s.ActivateRuntime(r.s.Op.Context()), Host: core.HostOf(r.s), Input: r.s.Input, Output: r.s.Output, Catalog: r.auth.Catalog}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	evaluation.RoutineAuthority = &r.auth
	// Imported cells and expression coercions share this pass's captured
	// catalog/routine authority and its already activated runtime context.
	evaluation.Input.DomainChecks = &evaluation
	stats, count, err := storage.CollectStatistics(r.s.Op.Context(), columns, func(yield func([]sqltype.Value) error) error {
		for _, segment := range visits {
			var scratch []sqltype.Value
			if segment.layout != nil {
				scratch = make([]sqltype.Value, len(segment.layout))
			}
			consume := func(values []sqltype.Value) error {
				if segment.layout != nil {
					for slot, physical := range segment.layout {
						scratch[slot] = values[physical]
					}
					values = scratch
				}
				for i, collector := range collectors {
					if err := r.extended[i].add(&evaluation, collector, values); err != nil {
						return err
					}
				}
				return yield(values)
			}
			if segment.foreign != nil {
				if segment.sample != nil {
					if err := segment.sample.each(&evaluation, consume); err != nil {
						return err
					}
				}
				continue
			}
			n := segment.source.Count()
			k := n
			if sampled != nil {
				k = segment.accepted
			}
			for j := range k {
				if err := r.s.Op.Context().Err(); err != nil {
					return err
				}
				ordinal := j
				if k < n {
					ordinal = analyzeMidpoint(j, n, k)
				}
				if err := consume(segment.source.VersionAt(segment.source.RawIndex(ordinal)).Values); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	if count != 0 {
		for _, warning := range r.extendedWarnings {
			r.s.Op.EmitNotice(session.LevelWarning, warning)
		}
	}
	if stats != nil {
		optionName := "n_distinct"
		if inherited {
			optionName = "n_distinct_inherited"
		}
		for key, fact := range stats.Columns {
			if inherited {
				fact.Hierarchy = r.hierarchy
			}
			for _, option := range r.relation.ColumnMeta.Statistics[key.Attribute].Options {
				if option.Name == optionName {
					if distinct, _ := catalog.ParseColumnDistinct(option.Value); distinct != 0 {
						fact.DistinctOverride = distinct
					}
				}
			}
		}
	}
	for i, collector := range collectors {
		fact, err := collector.Finish(r.s.Op.Context())
		if err != nil {
			return nil, 0, err
		}
		if fact != nil {
			key := r.extended[i].Key
			key.Inherited = inherited
			if inherited {
				fact.Hierarchy = r.hierarchy
			}
			r.extendedDelta[key] = fact
		}
	}
	return stats, count, nil
}
