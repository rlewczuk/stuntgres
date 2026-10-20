package exec

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

type ForeignQuerySource struct {
	Wrapper        catalog.ForeignDataWrapper
	Request        catalog.ForeignRequest
	Columns        []catalog.Column
	ImportValue    ForeignValueImporter
	ExportValue    ForeignValueExporter
	Executions     []*ForeignExecution
	lifetimeOwner  *ForeignScanOwner
	LifetimeClosed bool
	closeError     error
}

type ForeignExecution struct {
	owner         any
	active, ended bool
	Scan          catalog.ForeignScan
	Plan          catalog.ForeignPlan
	Slots         []int
	planned       bool
	closeError    error
}

func CopyForeignRequest(request catalog.ForeignRequest) catalog.ForeignRequest {
	request.Columns = slices.Clone(request.Columns)
	for i := range request.Columns {
		request.Columns[i].Options = slices.Clone(request.Columns[i].Options)
	}
	request.RequiredAttributes = slices.Clone(request.RequiredAttributes)
	request.WrapperOptions = slices.Clone(request.WrapperOptions)
	request.ServerOptions = slices.Clone(request.ServerOptions)
	request.TableOptions = slices.Clone(request.TableOptions)
	request.MappingOptions = slices.Clone(request.MappingOptions)
	request.Parameters.Values = CopyForeignCells(request.Parameters.Values)
	request.Parameters.Correlation = CopyForeignCells(request.Parameters.Correlation)
	if request.Identity != nil {
		targets := *request.Identity
		targets.Names = slices.Clone(targets.Names)
		request.Identity = &targets
	}
	return request
}

func CopyForeignCells(cells []driver.Value) []driver.Value {
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

func (f *ForeignQuerySource) Plan(ctx context.Context, request catalog.ForeignRequest, execution *ForeignExecution) error {
	size, err := f.Wrapper.GetForeignRelSize(ctx, CopyForeignRequest(request))
	if err != nil {
		return ForeignCallbackError(err)
	}
	if !finiteForeignNumber(size.Rows) || !finiteForeignNumber(size.Width) {
		return fmt.Errorf("stuntgres: invalid foreign relation size")
	}
	paths, err := f.Wrapper.GetForeignPaths(ctx, CopyForeignRequest(request), size)
	if err != nil {
		return ForeignCallbackError(err)
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
	plan, err := f.Wrapper.GetForeignPlan(ctx, CopyForeignRequest(request), paths[selected])
	if err != nil {
		return ForeignCallbackError(err)
	}
	if err = foreignLayout(request, plan.Attributes); err != nil {
		return err
	}
	plan.Attributes = slices.Clone(plan.Attributes)
	execution.Plan = plan
	execution.planned = true
	execution.Slots = make([]int, len(plan.Attributes))
	for i, attribute := range plan.Attributes {
		for slot, column := range request.Columns {
			if column.Attribute == attribute {
				execution.Slots[i] = slot
				break
			}
		}
	}
	return nil
}

func ForeignParameters(ctx context.Context, state *State, f *ForeignQuerySource) (catalog.ForeignScanParameters, error) {
	var parameters catalog.ForeignScanParameters
	export := func(values []sqltype.Value) ([]driver.Value, error) {
		out := make([]driver.Value, len(values))
		for i, value := range values {
			cell, err := f.ExportValue(ctx, value, state.Output)
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
	parameters.Values, err = export(state.Parameters)
	if err != nil {
		return parameters, err
	}
	correlation := state.Invocation
	if correlation == nil {
		correlation = state.Outer
	}
	if correlation != nil {
		parameters.Correlation, err = export(correlation.Row)
	}
	return parameters, err
}

func (execution *ForeignExecution) end(ctx context.Context) error {
	if execution.ended {
		return execution.closeError
	}
	execution.ended = true
	execution.active = false
	if execution.Scan != nil {
		execution.closeError = ForeignCallbackError(execution.Scan.EndForeignScan(context.WithoutCancel(ctx)))
		execution.Scan = nil
	}
	execution.Plan = catalog.ForeignPlan{}
	execution.Slots = nil
	execution.owner = nil
	return execution.closeError
}

func OpenForeignStage(ctx context.Context, state *State, source *Source, node *plan.SourceNode) (Stage, error) {
	f := source.Foreign
	if f.LifetimeClosed {
		return Stage{}, fmt.Errorf("stuntgres: foreign scan owner has ended")
	}
	var execution *ForeignExecution
	for _, candidate := range f.Executions {
		if candidate.owner == state.foreignOwner && !candidate.active && !candidate.ended {
			execution = candidate
			break
		}
	}
	if execution == nil {
		execution = &ForeignExecution{owner: state.foreignOwner}
		f.Executions = append(f.Executions, execution)
	}
	execution.active = true
	out := Stage{Source: source, Columns: node.Schema(), Selection: []int{}}
	started := false
	start := func(ctx context.Context) error {
		if host := source.LiveHost(); host != nil {
			host.ReleaseReplacementRead()
		}
		parameters, err := ForeignParameters(ctx, state, f)
		if err != nil {
			return err
		}
		request := CopyForeignRequest(f.Request)
		request.Parameters = parameters
		if !execution.planned {
			if err = f.Plan(ctx, request, execution); err != nil {
				return err
			}
		}
		if host := source.LiveHost(); host != nil {
			request.Transaction, err = host.EnlistForeign(ctx, f.Wrapper, request, false)
			if err != nil {
				return err
			}
		}
		if execution.Scan == nil {
			scan, err := f.Wrapper.BeginForeignScan(ctx, request, execution.Plan)
			execution.Scan = scan
			if err != nil {
				return ForeignCallbackError(err)
			}
			if scan == nil {
				return fmt.Errorf("stuntgres: foreign-data wrapper returned a nil scan")
			}
		} else {
			if err = execution.Scan.ReScanForeignScan(ctx, parameters); err != nil {
				return ForeignCallbackError(err)
			}
		}
		started = true
		return nil
	}
	out.Produce = func(ctx context.Context, out *Stage) (bool, error) {
		if host := source.LiveHost(); host != nil {
			host.ReleaseReplacementRead()
		}
		if !started {
			if err := start(ctx); err != nil {
				return false, err
			}
		}
		if host := source.LiveHost(); host != nil {
			if err := host.TouchForeignScan(ctx, f); err != nil {
				return false, err
			}
		}
		row, present, err := execution.Scan.IterateForeignScan(ctx)
		if err != nil || !present {
			return false, ForeignCallbackError(err)
		}
		if len(row) != len(execution.Plan.Attributes) {
			return false, fmt.Errorf("stuntgres: foreign scan row width does not match its plan")
		}
		input, output := state.Input, state.Output
		if host := source.LiveHost(); host != nil {
			input, output = host.Environments()
		}
		values, err := f.ImportValues(ctx, row, execution.Slots, input, output, node)
		if err != nil {
			return false, err
		}
		index := len(source.Anonymous)
		source.Anonymous = append(source.Anonymous, values)
		out.Selection = append(out.Selection, index)
		return true, nil
	}
	out.Finish = func() error { execution.active = false; return nil }
	out.Reset = func(ctx context.Context, out *Stage) (bool, error) {
		if host := source.LiveHost(); host != nil {
			host.ReleaseReplacementRead()
		}
		if started {
			if host := source.LiveHost(); host != nil {
				if err := host.TouchForeignScan(ctx, f); err != nil {
					return false, err
				}
			}
			parameters, err := ForeignParameters(ctx, state, f)
			if err != nil {
				return false, err
			}
			if err = execution.Scan.ReScanForeignScan(ctx, parameters); err != nil {
				return false, ForeignCallbackError(err)
			}
		}
		out.Selection = out.Selection[:0]
		out.Done = false
		return true, nil
	}
	return out, nil
}

// The eager execution owner calls this before successful publication. Failure
// paths call it before releasing sources but preserve the primary failure.
func FinishForeignSources(ctx context.Context, source *Source) error {
	if source == nil {
		return nil
	}
	var failure error
	if source.Foreign != nil {
		if host := source.LiveHost(); host != nil {
			host.ReleaseReplacementRead()
		}
		if err := source.Foreign.lifetimeOwner.finishSource(ctx, source.Foreign); failure == nil {
			failure = err
		}
	}
	for _, child := range source.Sources {
		if child != source {
			if err := FinishForeignSources(ctx, child); failure == nil {
				failure = err
			}
		}
	}
	for _, child := range []*Source{source.NestedInputs, source.Input} {
		if err := FinishForeignSources(ctx, child); failure == nil {
			failure = err
		}
	}
	return failure
}

func (f *ForeignQuerySource) columnDiagnostic(err error, node *plan.SourceNode, slot int) error {
	var record *sqlstate.Error
	if !errors.As(err, &record) {
		return err
	}
	column := f.Columns[slot].Name
	if slot < len(node.ColumnAliases) {
		column = node.ColumnAliases[slot]
	}
	relation := node.RangeName
	if relation == "" {
		relation = f.Request.Table
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
type ForeignScanOwner struct {
	sources    []*ForeignQuerySource
	closeError error
}

func (owner *ForeignScanOwner) Add(source *ForeignQuerySource) {
	source.lifetimeOwner = owner
	owner.sources = append(owner.sources, source)
}

func (owner *ForeignScanOwner) finishSource(ctx context.Context, source *ForeignQuerySource) error {
	if source.LifetimeClosed {
		return source.closeError
	}
	source.LifetimeClosed = true
	for _, execution := range source.Executions {
		if err := execution.end(ctx); source.closeError == nil {
			source.closeError = err
		}
	}
	if owner.closeError == nil {
		owner.closeError = source.closeError
	}
	return source.closeError
}

func (owner *ForeignScanOwner) Finish(ctx context.Context) error {
	if owner == nil {
		return nil
	}
	for _, source := range owner.sources {
		owner.finishSource(ctx, source)
	}
	return owner.closeError
}
