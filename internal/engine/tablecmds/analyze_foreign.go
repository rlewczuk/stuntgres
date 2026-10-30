package tablecmds

import (
	"fmt"
	"math"
	"time"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func validForeignEstimate(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

// Entered with Mu, released on every path. Sampling/import/comparison is detached;
// the final immutable publication uses the ordinary ANALYZE journal owner.
func analyzeForeign(e *core.Engine, s *core.StatementScope, target analyzeTarget, options table.AnalyzeOptions, auth core.AuthorizationView, relation *catalog.Relation) error {
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
	handler, err := fdw.HandlerFor(auth.Catalog, wrapper, auth.RestrictedRelations())
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	provider, supported := handler.Wrapper.(catalog.ForeignAnalyzeProvider)
	unsupported := func() error {
		analyzeWarning(s, "01000", fmt.Sprintf(`skipping "%s" --- cannot analyze this foreign table`, relation.Name()))
		return nil
	}
	if !supported {
		e.Mu.Unlock()
		return unsupported()
	}
	if err := core.CheckRestoreEffect(s.Op); err != nil {
		e.Mu.Unlock()
		return err
	}
	// PostgreSQL samples under the table owner's mapping, not the MAINTAIN actor.
	_, request, err := fdw.AnalyzeRequest(auth.View, relation.Owner, relation, auth.RestrictedRelations())
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	captured := auth.Catalog.State()
	qualified := request.Schema + "." + request.Table
	e.Mu.Unlock()
	core.HostOf(s).ReleaseReplacementRead()
	info, supported, err := provider.AnalyzeForeignTable(s.Op.Context(), exec.CopyForeignRequest(request))
	if err != nil {
		return exec.ForeignCallbackError(err)
	}
	// Fence even an unsupported result: cancellation or replaced authority is
	// not permission to silently publish a warning from a stale activation.
	e.Mu.Lock()
	currentCapability, err := e.AuthorizationLocked(s.Op)
	if err == nil && (currentCapability.Catalog.State() != captured || currentCapability.Roles != auth.Roles) {
		err = ids.ErrCatalogReplaced
	}
	if err == nil {
		err = s.Op.Context().Err()
	}
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	if !supported {
		e.Mu.Unlock()
		return unsupported()
	}
	if info.AcquireSampleRows == nil {
		e.Mu.Unlock()
		return fmt.Errorf("stuntgres: invalid foreign ANALYZE capability")
	}
	selected := make(map[string]bool, len(target.columns))
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
	var columns []storage.StatisticsColumn
	targetRows := 100
	for slot, column := range relation.Columns {
		attribute := relation.Attributes[slot]
		if len(selected) != 0 && !selected[column.Name] || relation.ColumnMeta.Generations[attribute].Kind == catalog.GenerationVirtual {
			continue
		}
		config := relation.ColumnMeta.Statistics[attribute]
		columnTarget := s.Op.StatisticsTarget
		if config.TargetSet {
			columnTarget = int(config.Target)
		}
		if columnTarget == 0 {
			continue
		}
		if columnTarget < 0 || columnTarget > 10000 {
			e.Mu.Unlock()
			return fmt.Errorf("stuntgres: invalid foreign statistics target")
		}
		targetRows = max(targetRows, 300*columnTarget)
		columns = append(columns, storage.StatisticsColumn{Key: storage.StatisticsKey{Attribute: attribute}, Usage: column.Usage, Slot: slot, Target: columnTarget})
	}
	importer, exporter, codecs := e.Foreign.Codecs()
	if !codecs {
		e.Mu.Unlock()
		return fmt.Errorf("stuntgres: foreign value codecs are not installed")
	}
	_, request, err = fdw.ScanRequest(currentCapability.View, relation.Owner, relation, nil, currentCapability.RestrictedRelations())
	e.Mu.Unlock()
	if err != nil {
		return err
	}
	source := &exec.ForeignQuerySource{Wrapper: handler.Wrapper, Request: request, Columns: relation.Columns, ImportValue: importer, ExportValue: exporter}
	if options.Verbose {
		s.Op.EmitNotice(session.LevelInfo, sqlstate.Error{Severity: "INFO", SeverityUnlocalized: "INFO", Code: "00000", Message: fmt.Sprintf(`analyzing "%s"`, qualified)})
	}
	sample, err := info.AcquireSampleRows(s.Op.Context(), exec.CopyForeignRequest(request), targetRows)
	if err != nil {
		return exec.ForeignCallbackError(err)
	}
	if len(sample.Rows) > targetRows || !validForeignEstimate(sample.TotalRows) || !validForeignEstimate(sample.TotalDeadRows) || !validForeignEstimate(sample.TotalPages) {
		return fmt.Errorf("stuntgres: invalid foreign ANALYZE sample")
	}
	for i := range columns {
		columns[i].SamplePopulation = &sample.TotalRows
	}
	if sample.TotalRows == 0 {
		columns = nil
	}
	slots := make([]int, len(request.RequiredAttributes))
	for i, attribute := range request.RequiredAttributes {
		slots[i] = relation.IndexForAttribute(attribute)
	}
	stats, count, err := storage.CollectStatistics(s.Op.Context(), columns, func(yield func([]sqltype.Value) error) error {
		for _, row := range sample.Rows {
			values, err := source.ImportValues(s.Op.Context(), row, slots, s.Input, s.Output, nil, nil)
			if err != nil {
				return err
			}
			if err := yield(values); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	var delta map[storage.StatisticsKey]*storage.ColumnStatistics
	if stats != nil {
		delta = stats.Columns
		for key, fact := range delta {
			for _, option := range relation.ColumnMeta.Statistics[key.Attribute].Options {
				if option.Name == "n_distinct" {
					if distinct, _ := catalog.ParseColumnDistinct(option.Value); distinct != 0 {
						fact.DistinctOverride = distinct
					}
				}
			}
		}
	}
	completed := time.Now().UTC().Truncate(time.Microsecond)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	current, err := e.AuthorizationLocked(s.Op)
	if err != nil {
		return err
	}
	if current.Catalog.State() != captured {
		return ids.ErrCatalogReplaced
	}
	allowed, err := table.VacuumAllowed(s.Op.Context(), current.View, current, relation)
	if err != nil {
		return err
	}
	if !allowed {
		return core.SQLError("42501", fmt.Sprintf(`permission denied for table %s`, relation.Name()))
	}
	if current.Roles != auth.Roles {
		return ids.ErrCatalogReplaced
	}
	if err := s.Op.Context().Err(); err != nil {
		return err
	}
	original, _ := e.Catalog.State.Relation(relation.Address())
	previous := session.ResolveRelationMetadata(s.PendingCatalog(), e.Storage.RelationMetadata, relation.Address())
	metadata := &storage.RelationMetadata{}
	if previous != nil {
		*metadata = *previous
	}
	metadata.Imported = storage.WithoutImportedRelationStatistics(metadata.Imported, storage.RelationPages | storage.RelationTuples)
	if pending := s.PendingCatalog(); pending != nil {
		if s.Catalog == nil { s.Catalog = pending.Clone() }
		s.Catalog.ForgetRelationStatistics(relation.Address(), storage.RelationPages | storage.RelationTuples)
	}
	metadata.ForeignAnalyze = &storage.ForeignAnalyzeStatistics{SampleRows: count, TotalRows: sample.TotalRows, TotalDeadRows: sample.TotalDeadRows, TotalPages: sample.TotalPages}
	metadata.AnalyzeCount++
	metadata.LastAnalyze = completed
	if s.Owner.InBlock() || s.PendingCatalog() != nil {
		if s.Catalog == nil {
			s.Catalog = s.PendingCatalog().Clone()
		}
		var private *storage.RelationMetadata
		if original == nil {
			private = metadata
		}
		s.Catalog.RecordStatistics(relation.Address(), nil, original, relation, delta, nil, private)
	} else {
		metadata.Statistics = storage.MergeStatistics(metadata.Statistics, delta)
	}
	if original != nil {
		if e.Storage.RelationMetadata == nil {
			e.Storage.RelationMetadata = make(map[ids.ObjectAddress]*storage.RelationMetadata)
		}
		e.Storage.RelationMetadata[relation.Address()] = metadata
	}
	return nil
}
