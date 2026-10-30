package analyze

import (
	"context"
	"fmt"
	"sort"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/util"
	"stuntgres/internal/workadmission"
)

type ExpressionContext uint8

const (
	ProjectionContext ExpressionContext = iota
	FilterContext
	AssignmentContext
	FunctionArgumentContext
)

type ExpressionPolicy struct {
	Context          ExpressionContext
	Expected         sqltype.Usage
	Mode             sqltype.CoercionContext
	Column           string
	booleanConstruct string
	// Only width_bucket's threshold argument consumes existing enum arrays.
	// This is not permission for generic casts, storage or domain declarations.
	thresholdArrayInput bool
	// Selected string INSERT rows analyze occurrences before target assignment.
	deferAssignment bool
	// Operand skips the enclosing WHERE BOOL check while binding predicate children.
	operand bool
}

// Binder binds the expressions of one statement. Host is the engine seam
// (12c); Ctx, Expected and Input are the statement inputs; Dependencies is
// the statement's dependency manifest. Everything else lives in the value
// groups of binder_groups.go.
type Binder struct {
	Host              Host
	Ctx               context.Context
	Expected          Expected
	Input             sqltype.InputEnvironment
	Dependencies      []catalog.ExpressionDependency
	dependencyPurpose catalog.DependencyPurpose
	PL                *proc.PLFrame // Private reached-expression lexical inputs; never retained.
	plAssignmentDatum *PLAssignment
	plDatums          []plCallDatum
	plCallOutputs     []PLCallOutput
	plAssignmentQuery *parser.Select
	normalizingSyntax map[StoredExpressionSyntaxKey]bool

	Target         targetContext
	Params         parameterState
	Consumer       ConsumerContext
	Capture        ViewCapture
	Stored         StoredSyntaxState
	Prepared       PreparedCaptures
	Clause         ClauseState
	Grouping       GroupingState
	Query          QueryScopeState
	cte            cteState
	foldExpansions map[expr.FoldSourceOccurrence]uint32
	foldSource     expr.FoldSourceOccurrence
}

func NewBinder(ctx context.Context, host Host, input sqltype.InputEnvironment, expected Expected) *Binder {
	// Domain/default compilation can enter from active runtime conversion.
	// Binding owns neither application workload callbacks nor execution pauses.
	ctx = util.DeactivateExecutionPause(workadmission.Suspend(ctx))
	b := &Binder{Host: host, Ctx: ctx, Params: parameterState{Parameters: make(map[int]sqltype.Usage)}, Input: input, Expected: expected}
	host.CaptureEnumOperationSafety(&b.Input)
	if expected.Held {
		b.SeedParameters(expected.Parameters.Types)
		if expected.RefreshTemporals {
			for _, capture := range expected.Temporals {
				if !RefreshableTemporal(capture) {
					b.Prepared.Temporals = append(b.Prepared.Temporals, capture)
				}
			}
		}
	}
	return b
}

func (b *Binder) SeedParameters(types []sqltype.Usage) {
	b.Params.formalParameters = types
	currentType := RecordTypeMapper(func(t *sqltype.Descriptor) *sqltype.Descriptor { return b.Type(t.ID()) })
	for i, usage := range types {
		if usage.Type != nil {
			usage.Type = currentType(usage.Type)
		}
		b.Params.Parameters[i+1] = usage
	}
}

// Binding must retain the caller's catalog/path authority, including a private
// CREATE candidate, read through the operation's definition reader. Execution
// still opens providers through live authorization.
func (b *Binder) BindingAuthorization() (Authority, error) {
	return b.Host.BindingAuthority(b.Query.Auth)
}
func (b *Binder) Type(id sqltype.ID) *sqltype.Descriptor {
	d, _ := b.definitionType(id)
	return d
}
func (b *Binder) dependency(ref ids.ObjectRef, revision ids.DefinitionRevision) {
	b.captureDependency(ref, revision, b.dependencyPurpose)
}

func (b *Binder) captureDependency(ref ids.ObjectRef, revision ids.DefinitionRevision, purpose catalog.DependencyPurpose) {
	for i, d := range b.Dependencies {
		if d.Ref != ref || d.Revision != revision {
			continue
		}
		if d.Purpose == purpose || d.Purpose == catalog.DefinitionDependency && purpose == catalog.OperandDependency {
			return
		}
		if d.Purpose == catalog.OperandDependency && purpose == catalog.DefinitionDependency {
			b.Dependencies[i].Purpose = purpose
			return
		}
	}
	b.Dependencies = append(b.Dependencies, catalog.ExpressionDependency{Ref: ref, Revision: revision, Purpose: purpose})
}
func (b *Binder) BindingCatalog() catalog.Read {
	if b.Query.Auth != nil {
		return b.Query.Auth.ReadCatalog()
	}
	return b.Host.CurrentCatalog()
}

func (b *Binder) Info(u sqltype.Usage, pos parser.Position, p expr.Properties) expr.Info {
	view := b.BindingCatalog()
	address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(u.Type.ID())}
	revision := ids.DefinitionRevision(1)
	if descriptor, ok := b.definitionDescriptor(address); ok {
		revision = descriptor.Revision()
	}
	b.dependency(b.Host.CatalogOwner().Domain.Ref(view.Epoch(), address), revision)
	if u.Collation.ID != 0 {
		address := ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(u.Collation.ID)}
		if d, ok := b.definitionDescriptor(address); ok {
			b.dependency(b.Host.CatalogOwner().Domain.Ref(view.Epoch(), address), d.Revision())
		}
	}
	source := b.foldSource
	if source == (expr.FoldSourceOccurrence{}) {
		source = expr.FoldSourceOccurrence{Frame: b.FrameID(), Position: pos}
	}
	if b.foldExpansions == nil {
		b.foldExpansions = make(map[expr.FoldSourceOccurrence]uint32)
	}
	b.foldExpansions[source]++
	return expr.Info{Usage: u, Pos: pos, Property: p, Source: source, Occurrence: b.foldExpansions[source]}
}
func (b *Binder) Constrain(n int, u sqltype.Usage, pos parser.Position) error {
	if n <= 0 || b.Params.ClosedRoutineFormals && n > len(b.Params.formalParameters) {
		return expr.ErrorAt("42P02", fmt.Sprintf("there is no parameter $%d", n), pos)
	}
	if old, ok := b.Params.Parameters[n]; ok && old.Type != nil && old.Type != u.Type {
		if b.Params.UpdateParameters {
			// UPDATE uses occurrence-sensitive inference diagnostics; other
			// assignment consumers retain their selected 0A000 boundary.
			_, err := b.CoerceParameter(plan.ParameterExpr{Info: expr.Info{Pos: pos}, Number: n}, u)
			return err
		}
		return sqlError("0A000", "parameter reused with different destination types")
	}
	b.Params.Parameters[n] = u.Type.Usage()
	return nil
}
func (b *Binder) Finish() (ParameterInfo, error) {
	// Retained unknown ANY and null-test occurrences share binding order.
	// Diagnose occurrence mismatches before gaps or unresolved shared slots.
	for _, p := range b.Params.AnyParameters {
		if b.Params.Parameters[p.Number].Type != nil {
			return ParameterInfo{}, expr.ErrorAt("42P08", fmt.Sprintf("could not determine data type of parameter $%d", p.Number), p.Pos)
		}
	}
	keys := make([]int, 0, len(b.Params.Parameters))
	for n := range b.Params.Parameters {
		keys = append(keys, n)
	}
	sort.Ints(keys)
	for i, n := range keys {
		if n != i+1 || b.Params.Parameters[n].Type == nil {
			return ParameterInfo{}, sqlError("42P18", fmt.Sprintf("could not determine data type of parameter $%d", i+1))
		}
	}
	info := ParameterInfo{Count: len(keys), Types: make([]sqltype.Usage, len(keys))}
	for i, n := range keys {
		info.Types[i] = b.Params.Parameters[n]
		if err := b.CaptureTypeDiagnosticInput(info.Types[i].Type); err != nil {
			return ParameterInfo{}, err
		}
	}
	return info, nil
}

// Output fallback belongs to an unresolved occurrence, not its subsequently
// constrained shared slot. These private provisional nodes never escape describe.
func (b *Binder) FinalizeProjection(node expr.Expr) (expr.Expr, error) {
	p, ok := node.(plan.ParameterExpr)
	if !ok || p.Usage.Type != nil {
		return node, nil
	}
	return b.CoerceParameter(p, b.Type(sqltype.Text).Usage())
}
func (b *Binder) column(raw parser.ColumnExpr) (node expr.Expr, err error) {
	defer func() {
		if err == nil || !b.Params.ClosedRoutineFormals {
			return
		}
		diagnostic, ok := err.(*sqlstate.Error)
		if !ok || diagnostic.Code != "42703" && diagnostic.Code != "42P01" {
			return
		}
		parts := raw.Name.Parts
		if len(parts) != 1 && (len(parts) != 2 || parts[0].Text != b.Params.RoutineName) {
			return
		}
		for i, name := range b.Params.RoutineNames {
			if name != "" && name == parts[len(parts)-1].Text {
				node, err = b.Bind(parser.ParameterExpr{Number: i + 1, Pos: raw.Pos}, ExpressionPolicy{})
				if err == nil && b.Consumer.frozenDefinition {
					if b.Stored.Syntax == nil {
						b.Stored.Syntax = make(map[StoredExpressionSyntaxKey]parser.Expr)
					}
					b.Stored.Syntax[b.storedExpressionSyntaxKey(raw)] = parser.ParameterExpr{Number: i + 1, Pos: raw.Pos}
				}
				return
			}
		}
	}()
	if b.Clause.DomainValueUsage != nil && len(raw.Name.Parts) == 1 && raw.Name.Parts[0].Text == "value" {
		return plan.DomainValueExpr{Info: b.Info(*b.Clause.DomainValueUsage, raw.Pos, expr.Properties{DomainDependent: true})}, nil
	}
	if b.Target.GeneratedOwner != nil {
		name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
		owner := b.Target.GeneratedOwner
		qualified := len(raw.Name.Parts) == 1 || len(raw.Name.Parts) == 2 && raw.Name.Parts[0].Text == owner.Name() || len(raw.Name.Parts) == 3 && raw.Name.Parts[1].Text == owner.Name() && raw.Name.Parts[0].Text == b.Query.Namespace.Qualifiers[0].Schema
		if qualified {
			if name == "tableoid" {
				ref := b.Query.Sources[0].Ref
				return plan.TableOIDExpr{Info: b.Info(b.Type(sqltype.Oid).Usage(), raw.Pos, expr.Properties{RowDependent: true}), Owner: ref}, nil
			}
			if catalog.IsSystemColumn(name) {
				return nil, expr.ErrorAt("42P10", fmt.Sprintf(`cannot use system column "%s" in column generation expression`, name), raw.Pos)
			}
		}
		node, err := b.queryColumn(raw)
		if err != nil {
			// A bare relation name is a whole-row reference only after ordinary
			// column lookup has failed; a same-named attribute takes precedence.
			if name == owner.Name() && len(raw.Name.Parts) == 1 {
				diagnostic := expr.ErrorAt("42P17", "cannot use whole-row variable in column generation expression", raw.Pos).(*sqlstate.Error)
				diagnostic.Detail = "This would cause the generated column to depend on its own value."
				return nil, diagnostic
			}
			return nil, err
		}
		if _, whole := node.(plan.RowExpr); whole {
			diagnostic := expr.ErrorAt("42P17", "cannot use whole-row variable in column generation expression", raw.Pos).(*sqlstate.Error)
			diagnostic.Detail = "This would cause the generated column to depend on its own value."
			return nil, diagnostic
		}
		if column, ok := node.(plan.ColumnExpr); ok {
			if _, generated := owner.ColumnMeta.Generations[column.Origin.Address.SubID]; generated {
				diagnostic := expr.ErrorAt("42P17", fmt.Sprintf(`cannot use generated column "%s" in column generation expression`, name), raw.Pos).(*sqlstate.Error)
				diagnostic.Detail = "A generated column cannot reference another generated column."
				return nil, diagnostic
			}
		}
		return node, nil
	}
	if b.Consumer.CheckConsumer && b.Clause.DomainValueUsage == nil && len(raw.Name.Parts) > 0 {
		name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
		switch name {
		case "ctid", "xmin", "cmin", "xmax", "cmax":
			return nil, expr.ErrorAt("42P10", `system column "`+name+`" reference in check constraint is invalid`, raw.Pos)
		}
	}
	if b.Target.returningAliases != nil {
		return b.returningColumn(raw)
	}
	return b.queryColumn(raw)
}

func (b *Binder) useColumnOf(relation *catalog.Relation, i int) ids.ObjectRef {
	ref := b.Target.Ref
	ref.Address.SubID = relation.Attributes[i]
	b.dependency(ref, relation.Revision())
	used := false
	for _, slot := range b.Target.Uses {
		if slot == i {
			used = true
			break
		}
	}
	if !used {
		b.Target.Uses = append(b.Target.Uses, i)
	}
	b.Info(relation.Columns[i].Usage, parser.Position{Offset: -1}, expr.Properties{})
	return ref
}
func (b *Binder) columnAt(i int, pos parser.Position) expr.Expr {
	return b.columnAtOf(b.Target.Relation, i, pos)
}

func (b *Binder) columnAtOf(relation *catalog.Relation, i int, pos parser.Position) expr.Expr {
	ref := b.useColumnOf(relation, i)
	column := plan.ColumnExpr{Info: expr.Info{Usage: relation.Columns[i].Usage, Pos: pos, Property: expr.Properties{RowDependent: true}}, Origin: ref, Scope: 1, Frame: 1, Slot: i}
	if b.Target.GeneratedOwner == nil && !b.Consumer.CopyWhere {
		column = column.WithGeneration(relation, i, 0)
	}
	return column
}
