package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"math"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// The root adapter supplies its existing parameter/result codec bridges. These
// are internal engine inputs, not application callbacks or alternate codecs.
type ForeignValueImporter func(context.Context, driver.Value, sqltype.Usage, sqltype.InputEnvironment) (sqltype.Value, error)

type ForeignValueExporter func(context.Context, sqltype.Value, sqltype.OutputEnvironment) (driver.Value, error)

type foreignQuerySource struct {
	wrapper        catalog.ForeignDataWrapper
	request        catalog.ForeignRequest
	columns        []catalog.Column
	importValue    ForeignValueImporter
	exportValue    ForeignValueExporter
	executions     []*foreignExecution
	lifetimeOwner  *foreignScanOwner
	lifetimeClosed bool
	closeError     error
}

type foreignExecution struct {
	owner         any
	active, ended bool
	scan          catalog.ForeignScan
	plan          catalog.ForeignPlan
	slots         []int
	planned       bool
	closeError    error
}

func copyForeignRequest(request catalog.ForeignRequest) catalog.ForeignRequest {
	request.Columns = slices.Clone(request.Columns)
	for i := range request.Columns {
		request.Columns[i].Options = slices.Clone(request.Columns[i].Options)
	}
	request.RequiredAttributes = slices.Clone(request.RequiredAttributes)
	request.WrapperOptions = slices.Clone(request.WrapperOptions)
	request.ServerOptions = slices.Clone(request.ServerOptions)
	request.TableOptions = slices.Clone(request.TableOptions)
	request.MappingOptions = slices.Clone(request.MappingOptions)
	request.Parameters.Values = copyForeignCells(request.Parameters.Values)
	request.Parameters.Correlation = copyForeignCells(request.Parameters.Correlation)
	if request.Identity != nil {
		targets := *request.Identity
		targets.Names = slices.Clone(targets.Names)
		request.Identity = &targets
	}
	return request
}

func copyForeignCells(cells []driver.Value) []driver.Value {
	out := slices.Clone(cells)
	for i, value := range out {
		if bytes, ok := value.([]byte); ok {
			out[i] = slices.Clone(bytes)
		}
	}
	return out
}

func finiteForeignNumber(n float64) bool { return n >= 0 && !math.IsNaN(n) && !math.IsInf(n, 0) }

func foreignLayout(request catalog.ForeignRequest, attributes []int32) error {
	for i, attribute := range attributes {
		valid := false
		for _, column := range request.Columns {
			if column.Attribute == attribute {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("stuntgres: invalid foreign scan attribute layout")
		}
		for _, prior := range attributes[:i] {
			if prior == attribute {
				return fmt.Errorf("stuntgres: invalid foreign scan attribute layout")
			}
		}
	}
	for _, attribute := range request.RequiredAttributes {
		if !slices.Contains(attributes, attribute) {
			return fmt.Errorf("stuntgres: foreign scan plan omits a required local attribute")
		}
	}
	return nil
}

func (f *foreignQuerySource) plan(ctx context.Context, request catalog.ForeignRequest, execution *foreignExecution) error {
	size, err := f.wrapper.GetForeignRelSize(ctx, copyForeignRequest(request))
	if err != nil {
		return foreignCallbackError(err)
	}
	if !finiteForeignNumber(size.Rows) || !finiteForeignNumber(size.Width) {
		return fmt.Errorf("stuntgres: invalid foreign relation size")
	}
	paths, err := f.wrapper.GetForeignPaths(ctx, copyForeignRequest(request), size)
	if err != nil {
		return foreignCallbackError(err)
	}
	if len(paths) == 0 {
		return fmt.Errorf("stuntgres: foreign-data wrapper returned no scan paths")
	}
	selected := 0
	for i, path := range paths {
		if !finiteForeignNumber(path.StartupCost) || !finiteForeignNumber(path.TotalCost) || path.TotalCost < path.StartupCost {
			return fmt.Errorf("stuntgres: invalid foreign path cost")
		}
		if err = foreignLayout(request, path.Attributes); err != nil {
			return err
		}
		if path.TotalCost < paths[selected].TotalCost {
			selected = i
		}
	}
	plan, err := f.wrapper.GetForeignPlan(ctx, copyForeignRequest(request), paths[selected])
	if err != nil {
		return foreignCallbackError(err)
	}
	if err = foreignLayout(request, plan.Attributes); err != nil {
		return err
	}
	plan.Attributes = slices.Clone(plan.Attributes)
	execution.plan = plan
	execution.planned = true
	execution.slots = make([]int, len(plan.Attributes))
	for i, attribute := range plan.Attributes {
		for slot, column := range request.Columns {
			if column.Attribute == attribute {
				execution.slots[i] = slot
				break
			}
		}
	}
	return nil
}

func foreignParameters(ctx context.Context, state *queryState, f *foreignQuerySource) (catalog.ForeignScanParameters, error) {
	var parameters catalog.ForeignScanParameters
	export := func(values []sqltype.Value) ([]driver.Value, error) {
		out := make([]driver.Value, len(values))
		for i, value := range values {
			cell, err := f.exportValue(ctx, value, state.output)
			if err != nil {
				return nil, err
			}
			if bytes, ok := cell.([]byte); ok {
				cell = slices.Clone(bytes)
			}
			out[i] = cell
		}
		return out, nil
	}
	var err error
	parameters.Values, err = export(state.parameters)
	if err != nil {
		return parameters, err
	}
	correlation := state.invocation
	if correlation == nil {
		correlation = state.outer
	}
	if correlation != nil {
		parameters.Correlation, err = export(correlation.Row)
	}
	return parameters, err
}

func (execution *foreignExecution) end(ctx context.Context) error {
	if execution.ended {
		return execution.closeError
	}
	execution.ended = true
	execution.active = false
	if execution.scan != nil {
		execution.closeError = foreignCallbackError(execution.scan.EndForeignScan(context.WithoutCancel(ctx)))
		execution.scan = nil
	}
	execution.plan = catalog.ForeignPlan{}
	execution.slots = nil
	execution.owner = nil
	return execution.closeError
}

func openForeignStage(ctx context.Context, state *queryState, source *querySource, node *plan.SourceNode) (indexedStage, error) {
	f := source.foreign
	if f.lifetimeClosed {
		return indexedStage{}, fmt.Errorf("stuntgres: foreign scan owner has ended")
	}
	var execution *foreignExecution
	for _, candidate := range f.executions {
		if candidate.owner == state.foreignOwner && !candidate.active && !candidate.ended {
			execution = candidate
			break
		}
	}
	if execution == nil {
		execution = &foreignExecution{owner: state.foreignOwner}
		f.executions = append(f.executions, execution)
	}
	execution.active = true
	out := indexedStage{source: source, schema: node.Schema(), selection: []int{}}
	started := false
	start := func(ctx context.Context) error {
		if host := source.liveHost(); host != nil {
			host.ReleaseReplacementRead()
		}
		parameters, err := foreignParameters(ctx, state, f)
		if err != nil {
			return err
		}
		request := copyForeignRequest(f.request)
		request.Parameters = parameters
		if !execution.planned {
			if err = f.plan(ctx, request, execution); err != nil {
				return err
			}
		}
		if host := source.liveHost(); host != nil {
			request.Transaction, err = host.EnlistForeign(ctx, f.wrapper, request, false)
			if err != nil {
				return err
			}
		}
		if execution.scan == nil {
			scan, err := f.wrapper.BeginForeignScan(ctx, request, execution.plan)
			execution.scan = scan
			if err != nil {
				return foreignCallbackError(err)
			}
			if scan == nil {
				return fmt.Errorf("stuntgres: foreign-data wrapper returned a nil scan")
			}
		} else {
			if err = execution.scan.ReScanForeignScan(ctx, parameters); err != nil {
				return foreignCallbackError(err)
			}
		}
		started = true
		return nil
	}
	out.produce = func(ctx context.Context, out *indexedStage) (bool, error) {
		if host := source.liveHost(); host != nil {
			host.ReleaseReplacementRead()
		}
		if !started {
			if err := start(ctx); err != nil {
				return false, err
			}
		}
		if host := source.liveHost(); host != nil {
			if err := host.TouchForeignScan(ctx, f); err != nil {
				return false, err
			}
		}
		row, present, err := execution.scan.IterateForeignScan(ctx)
		if err != nil || !present {
			return false, foreignCallbackError(err)
		}
		if len(row) != len(execution.plan.Attributes) {
			return false, fmt.Errorf("stuntgres: foreign scan row width does not match its plan")
		}
		input, output := state.input, state.output
		if host := source.liveHost(); host != nil {
			input, output = host.Environments()
		}
		values, err := f.importValues(ctx, row, execution.slots, input, output, node)
		if err != nil {
			return false, err
		}
		index := len(source.anonymous)
		source.anonymous = append(source.anonymous, values)
		out.selection = append(out.selection, index)
		return true, nil
	}
	out.finish = func() error { execution.active = false; return nil }
	out.reset = func(ctx context.Context, out *indexedStage) (bool, error) {
		if host := source.liveHost(); host != nil {
			host.ReleaseReplacementRead()
		}
		if started {
			if host := source.liveHost(); host != nil {
				if err := host.TouchForeignScan(ctx, f); err != nil {
					return false, err
				}
			}
			parameters, err := foreignParameters(ctx, state, f)
			if err != nil {
				return false, err
			}
			if err = execution.scan.ReScanForeignScan(ctx, parameters); err != nil {
				return false, foreignCallbackError(err)
			}
		}
		out.selection = out.selection[:0]
		out.done = false
		return true, nil
	}
	return out, nil
}

// The eager execution owner calls this before successful publication. Failure
// paths call it before releasing sources but preserve the primary failure.
func finishForeignSources(ctx context.Context, source *querySource) error {
	if source == nil {
		return nil
	}
	var failure error
	if source.foreign != nil {
		if host := source.liveHost(); host != nil {
			host.ReleaseReplacementRead()
		}
		if err := source.foreign.lifetimeOwner.finishSource(ctx, source.foreign); failure == nil {
			failure = err
		}
	}
	for _, child := range source.sources {
		if child != source {
			if err := finishForeignSources(ctx, child); failure == nil {
				failure = err
			}
		}
	}
	for _, child := range []*querySource{source.nestedInputs, source.input} {
		if err := finishForeignSources(ctx, child); failure == nil {
			failure = err
		}
	}
	return failure
}

func (f *foreignQuerySource) columnDiagnostic(err error, node *plan.SourceNode, slot int) error {
	var record *sqlstate.Error
	if !errors.As(err, &record) {
		return err
	}
	column := f.columns[slot].Name
	if slot < len(node.ColumnAliases) {
		column = node.ColumnAliases[slot]
	}
	relation := node.RangeName
	if relation == "" {
		relation = f.request.Table
	}
	result := *record
	line := fmt.Sprintf("column \"%s\" of foreign table \"%s\"", column, relation)
	if result.Context != "" {
		result.Context += "\n"
	}
	result.Context += line
	return &result
}

// One statement or logical SQL cursor owns each captured foreign source. A
// finalizer can finish one completed entry early; Finish drains the remaining
// entries through this same authority, never through a second cleanup list.
type foreignScanOwner struct {
	sources    []*foreignQuerySource
	closeError error
}

func (owner *foreignScanOwner) add(source *foreignQuerySource) {
	source.lifetimeOwner = owner
	owner.sources = append(owner.sources, source)
}

func (owner *foreignScanOwner) finishSource(ctx context.Context, source *foreignQuerySource) error {
	if source.lifetimeClosed {
		return source.closeError
	}
	source.lifetimeClosed = true
	for _, execution := range source.executions {
		if err := execution.end(ctx); source.closeError == nil {
			source.closeError = err
		}
	}
	if owner.closeError == nil {
		owner.closeError = source.closeError
	}
	return source.closeError
}

func (owner *foreignScanOwner) finish(ctx context.Context) error {
	if owner == nil {
		return nil
	}
	for _, source := range owner.sources {
		owner.finishSource(ctx, source)
	}
	return owner.closeError
}
