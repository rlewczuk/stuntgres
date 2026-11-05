package tablecmds

import (
	"fmt"
	"math"

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

// Negotiation retains command-local capability, not a catalog callback or sample.
// Neither negotiation nor acquisition holds engine/replacement locks.
type analyzeForeignSource struct {
	relation  *catalog.Relation
	wrapper   catalog.ForeignDataWrapper
	info      catalog.ForeignAnalyzeInfo
	qualified string
}

func captureAnalyzeForeign(e *core.Engine, s *core.StatementScope, auth core.AuthorizationView, relation *catalog.Relation) (*analyzeForeignSource, error) {
	e.Mu.Lock()
	current, err := e.AuthorizationLocked(s.Op)
	if err == nil && (current.Catalog.State() != auth.Catalog.State() || current.Roles != auth.Roles) {
		err = ids.ErrCatalogReplaced
	}
	if err == nil {
		err = s.Op.Context().Err()
	}
	if err != nil {
		e.Mu.Unlock()
		return nil, err
	}
	server, ok := current.Catalog.Foreign(relation.Foreign.Server)
	if !ok {
		e.Mu.Unlock()
		return nil, ids.ErrCatalogReplaced
	}
	wrapper, ok := current.Catalog.Foreign(server.Wrapper)
	if !ok {
		e.Mu.Unlock()
		return nil, ids.ErrCatalogReplaced
	}
	handler, err := fdw.HandlerFor(current.Catalog, wrapper, current.RestrictedRelations())
	if err != nil {
		e.Mu.Unlock()
		return nil, err
	}
	selection := exec.SelectForeignHandler(current.Catalog, wrapper.Handler)
	var request catalog.ForeignRequest
	if _, registeredAnalyze := handler.Wrapper.(catalog.ForeignAnalyzeProvider); registeredAnalyze {
		if err := core.CheckRestoreEffect(s.Op); err != nil {
			e.Mu.Unlock()
			return nil, err
		}
		// Negotiation needs owner metadata, not an acquisition mapping.
		_, request, err = fdw.AnalyzeRequest(current.View, relation.Owner, relation, current.RestrictedRelations())
		if err != nil {
			e.Mu.Unlock()
			return nil, err
		}
	}
	e.Mu.Unlock()
	core.HostOf(s).ReleaseReplacementRead()
	// Invoke the genuine handler before testing its optional analysis methods;
	// its trusted entry unwinds before any provider callback.
	methods, err := selection.Methods(core.HostOf(s))
	if err != nil {
		return nil, err
	}
	provider, supported := methods.(catalog.ForeignAnalyzeProvider)
	if !supported {
		return nil, nil
	}
	info, supported, err := provider.AnalyzeForeignTable(s.Op.Context(), exec.CopyForeignRequest(request))
	if err != nil {
		return nil, exec.ForeignCallbackError(err)
	}
	// An unsupported answer is still subject to cancellation/replacement fences.
	e.Mu.Lock()
	current, err = e.AuthorizationLocked(s.Op)
	if err == nil && (current.Catalog.State() != auth.Catalog.State() || current.Roles != auth.Roles) {
		err = ids.ErrCatalogReplaced
	}
	if err == nil {
		err = s.Op.Context().Err()
	}
	e.Mu.Unlock()
	if err != nil || !supported {
		return nil, err
	}
	if info.AcquireSampleRows == nil || !validForeignEstimate(info.TotalPages) {
		return nil, fmt.Errorf("stuntgres: invalid foreign ANALYZE capability")
	}
	return &analyzeForeignSource{relation: relation, wrapper: methods, info: info, qualified: request.Schema + "." + request.Table}, nil
}

type analyzeForeignSample struct {
	catalog.ForeignSample
	source *exec.ForeignQuerySource
	slots  []int
}

func (f *analyzeForeignSource) acquire(e *core.Engine, s *core.StatementScope, auth core.AuthorizationView, target int, verbose bool) (*analyzeForeignSample, error) {
	e.Mu.Lock()
	current, err := e.AuthorizationLocked(s.Op)
	if err == nil && (current.Catalog.State() != auth.Catalog.State() || current.Roles != auth.Roles) {
		err = ids.ErrCatalogReplaced
	}
	if err == nil {
		err = s.Op.Context().Err()
	}
	if err != nil {
		e.Mu.Unlock()
		return nil, err
	}
	importer, exporter, codecs := e.Foreign.Codecs()
	if !codecs {
		e.Mu.Unlock()
		return nil, fmt.Errorf("stuntgres: foreign value codecs are not installed")
	}
	_, request, err := fdw.ScanRequest(current.View, f.relation.Owner, f.relation, nil, current.RestrictedRelations())
	e.Mu.Unlock()
	if err != nil {
		return nil, err
	}
	core.HostOf(s).ReleaseReplacementRead()
	if verbose {
		s.Op.EmitNotice(session.LevelInfo, sqlstate.Error{Severity: "INFO", SeverityUnlocalized: "INFO", Code: "00000", Message: fmt.Sprintf(`analyzing "%s"`, f.qualified)})
	}
	sample, err := f.info.AcquireSampleRows(s.Op.Context(), exec.CopyForeignRequest(request), target)
	if err != nil {
		return nil, exec.ForeignCallbackError(err)
	}
	if err := s.Op.Context().Err(); err != nil {
		return nil, err
	}
	if len(sample.Rows) > target || !validForeignEstimate(sample.TotalRows) || !validForeignEstimate(sample.TotalDeadRows) || !validForeignEstimate(sample.TotalPages) {
		return nil, fmt.Errorf("stuntgres: invalid foreign ANALYZE sample")
	}
	e.Mu.Lock()
	current, err = e.AuthorizationLocked(s.Op)
	if err == nil && (current.Catalog.State() != auth.Catalog.State() || current.Roles != auth.Roles) {
		err = ids.ErrCatalogReplaced
	}
	e.Mu.Unlock()
	if err != nil {
		return nil, err
	}
	slots := make([]int, len(request.RequiredAttributes))
	for i, attribute := range request.RequiredAttributes {
		slots[i] = f.relation.IndexForAttribute(attribute)
	}
	return &analyzeForeignSample{ForeignSample: sample, source: &exec.ForeignQuerySource{Wrapper: f.wrapper, Request: request, Columns: f.relation.Columns, ImportValue: importer, ExportValue: exporter}, slots: slots}, nil
}

func (sample *analyzeForeignSample) each(evaluation *exec.Context, yield func([]sqltype.Value) error) error {
	for _, row := range sample.Rows {
		if err := evaluation.Context.Err(); err != nil {
			return err
		}
		values, err := sample.source.ImportValues(evaluation.Context, row, sample.slots, evaluation.Input, evaluation.Output, nil, nil)
		if err != nil {
			return err
		}
		if err := yield(values); err != nil {
			return err
		}
	}
	return evaluation.Context.Err()
}

// A direct pass owns its imported immutable values until every definition has
// built. Scalar completion, definition construction and delta acceptance are
// separate boundaries; none reacquires or reimports the provider's sample.
type analyzeForeignPass struct {
	evaluation exec.Context
	rows       [][]sqltype.Value
	statistics *storage.RelationStatistics
	foreign    *storage.ForeignAnalyzeStatistics
}

func (r *analyzeRun) collectForeign(f *analyzeForeignSource, options table.AnalyzeOptions) (*analyzeForeignPass, error) {
	e, s, auth := r.e, r.s, r.auth
	relation := f.relation
	columns := r.columns
	sample, err := f.acquire(e, s, auth, r.sampleTarget(), options.Verbose)
	if err != nil {
		return nil, err
	}
	for i := range columns {
		columns[i].SamplePopulation = &sample.TotalRows
	}
	if sample.TotalRows == 0 {
		columns = nil
	}
	pass := &analyzeForeignPass{evaluation: exec.Context{EvalStatement: exec.EvalStatement{Context: s.ActivateRuntime(s.Op.Context()), Host: core.HostOf(s), Catalog: auth.Catalog, Input: s.Input, Output: s.Output}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &r.auth}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}}
	// Domain input belongs to this detached pass's captured live authority,
	// not the statement's uninitialized generic utility-domain evaluator.
	pass.evaluation.Input.DomainChecks = &pass.evaluation
	if len(r.extended) != 0 && sample.TotalRows > 0 && len(sample.Rows) != 0 {
		pass.rows = make([][]sqltype.Value, 0, len(sample.Rows))
	}
	r.progress.SampleStart(len(sample.Rows))
	stats, count, err := storage.CollectStatistics(pass.evaluation.Context, columns, func(yield func([]sqltype.Value) error) error {
		completed := 0
		err := sample.each(&pass.evaluation, func(values []sqltype.Value) error {
			if err := yield(values); err != nil {
				return err
			}
			// ImportValues owns the immutable cells, never provider scratch.
			if pass.rows != nil {
				pass.rows = append(pass.rows, values)
			}
			completed++
			r.progress.SampleDone(completed, len(sample.Rows))
			return nil
		})
		if err == nil && completed != 0 {
			r.progress.Phase("computing statistics")
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	if stats != nil {
		for key, fact := range stats.Columns {
			for _, option := range relation.ColumnMeta.Statistics[key.Attribute].Options {
				if option.Name == "n_distinct" {
					if distinct, _ := catalog.ParseColumnDistinct(option.Value); distinct != 0 {
						fact.DistinctOverride = distinct
					}
				}
			}
		}
	}
	pass.statistics = stats
	pass.foreign = &storage.ForeignAnalyzeStatistics{SampleRows: count, TotalRows: sample.TotalRows, TotalDeadRows: sample.TotalDeadRows, TotalPages: sample.TotalPages}
	return pass, nil
}

// Build one whole definition after scalar completion. Expressions are evaluated
// once per accepted row and shared by all requested kinds through the collector.
func (pass *analyzeForeignPass) buildExtended(input *capturedExtendedInput) (*storage.ExtendedStatistics, error) {
	if err := pass.evaluation.Context.Err(); err != nil {
		return nil, err
	}
	captured := input.ExtendedInput
	captured.SamplePopulation = &pass.foreign.TotalRows
	collector, err := storage.NewExtendedCollector(captured)
	if err != nil {
		return nil, err
	}
	for _, row := range pass.rows {
		if err := pass.evaluation.Context.Err(); err != nil {
			return nil, err
		}
		if err := input.add(&pass.evaluation, collector, row); err != nil {
			return nil, err
		}
	}
	return collector.Finish(pass.evaluation.Context)
}

// Acceptance owns a complete typed definition delta, not a published statistic.
// The target's later publication gate still rechecks authority for all deltas.
func (r *analyzeRun) acceptExtended(key storage.ExtendedStatisticsKey, fact *storage.ExtendedStatistics) error {
	r.e.Mu.Lock()
	defer r.e.Mu.Unlock()
	if err := r.authorityLocked(); err != nil {
		return err
	}
	if fact != nil {
		r.extendedDelta[key] = fact
	}
	return nil
}
