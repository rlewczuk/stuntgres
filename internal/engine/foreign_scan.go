package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"math"
	"slices"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
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

// Capture only metadata while locked. Planning, source admission, conversion,
// restart and teardown all run from internal demand after the mutex is released.
func (e *Engine) captureForeignSourceLocked(scope *StatementScope, ref ids.ObjectRef, relation *catalog.Relation, invoker authorization.PrincipalRef, uses []int, mark plan.RowMark) (*querySource, error) {
	if mark.Mode != 0 {
		return nil, sqlError("0A000", "foreign row locking is unsupported in embedded mode")
	}
	auth, err := e.authorizationLocked(scope.op)
	if err != nil {
		return nil, err
	}
	if invoker != (authorization.PrincipalRef{}) {
		auth.State.CurrentUser = invoker
	}
	server, ok := auth.Catalog.Foreign(relation.Foreign.Server)
	if !ok {
		return nil, ids.ErrCatalogReplaced
	}
	wrapper, ok := auth.Catalog.Foreign(server.Wrapper)
	if !ok {
		return nil, ids.ErrCatalogReplaced
	}
	handler, err := foreignHandlerFor(auth.Catalog, wrapper)
	if err != nil {
		return nil, err
	}
	mapping, err := foreignMappingFor(auth, server)
	if err != nil {
		return nil, err
	}
	role, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	if err != nil {
		return nil, err
	}
	request := catalog.ForeignRequest{Schema: auth.Catalog.Get(relation.Namespace()).Name(), Table: relation.Name(), Role: role.Name, TableOID: uint32(relation.Address().ObjectID), RoleOID: uint32(role.Ref.ID()), WrapperOptions: slices.Clone(wrapper.Options), ServerOptions: slices.Clone(server.Options), TableOptions: slices.Clone(relation.Foreign.Options), MappingOptions: slices.Clone(mapping.Options)}
	request.ServerOID = uint32(server.Address().ObjectID)
	request.MappingOID = uint32(mapping.Address().ObjectID)
	request.HandlerOID = uint32(wrapper.Handler.ObjectID)
	for i, column := range relation.Columns {
		request.Columns = append(request.Columns, catalog.ForeignColumn{Attribute: relation.Attributes[i], Name: column.Name, TypeName: column.Usage.Type.Name(), TypeOID: uint32(column.Usage.Type.ID()), TypeModifier: column.Usage.Typmod, CollationOID: column.Usage.Collation.ID, Nullable: column.Nullable, Options: slices.Clone(relation.Foreign.Columns[relation.Attributes[i]])})
	}
	request.RequiredAttributes, err = foreignRequiredAttributes(relation, uses)
	if err != nil {
		return nil, err
	}
	if e.foreignValueImporter == nil || e.foreignValueExporter == nil {
		return nil, fmt.Errorf("stuntgres: foreign driver value codecs are not installed")
	}
	f := &foreignQuerySource{wrapper: handler.Wrapper, request: request, columns: slices.Clone(relation.Columns), importValue: e.foreignValueImporter, exportValue: e.foreignValueExporter}
	scope.ownForeignSource(f)
	return &querySource{ref: ref, anonymous: [][]sqltype.Value{}, foreign: f}, nil
}

// Logical virtual-column ACL uses remain unchanged. Only their existing bound
// expression's base inputs belong in the external row layout.
func foreignRequiredAttributes(relation *catalog.Relation, uses []int) ([]int32, error) {
	count := len(uses)
	if uses == nil {
		count = len(relation.Columns)
	}
	required := make([]int32, 0, count)
	add := func(slot int) {
		attribute := relation.Attributes[slot]
		if !slices.Contains(required, attribute) {
			required = append(required, attribute)
		}
	}
	for i := range count {
		slot := i
		if uses != nil {
			slot = uses[i]
		}
		if slot < 0 || slot >= len(relation.Attributes) {
			return nil, fmt.Errorf("stuntgres: invalid foreign required attribute")
		}
		if generation, ok := relation.ColumnMeta.Generations[relation.Attributes[slot]]; ok && generation.Kind == catalog.GenerationVirtual {
			walkBoundExpression(generation.Planned, func(node expr.Expr) {
				if column, ok := node.(plan.ColumnExpr); ok {
					add(column.Slot)
				}
			})
		} else {
			add(slot)
		}
	}
	return required, nil
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
