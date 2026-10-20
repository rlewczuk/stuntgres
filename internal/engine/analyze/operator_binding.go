package analyze

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (b *Binder) bindBuiltinOperator(r parser.OperatorExpr, policy ExpressionPolicy) (expr.Expr, error) {
	if r.SQLSimilar {
		return functions.BindSimilar(b, r)
	}
	if subquery, ok := rowSubqueryComparison(r); ok {
		return b.bindSubquery(subquery)
	}
	name := r.Name.Parts[len(r.Name.Parts)-1].Text
	switch {
	case rangeSharedOperator(r):
		return b.bindRangeShared(r)
	case name == "||":
		return b.BindStringConcat(r)
	case name == "^@":
		return b.bindStringPrefix(r)
	case likeName(name):
		return bindLike(b, r)
	case len(r.Args) == 2 && functions.PosixName(name):
		return functions.BindPosix(b, r)
	case ArrayMembershipName(name):
		return b.bindArrayMembership(r)
	case functions.JSONExtractionName(name):
		return functions.BindJSONExtraction(b, r)
	case functions.JSONBExistenceName(name):
		return functions.BindJSONBExistence(b, r)
	case functions.JSONBPathOperatorName(name) && len(b.Clause.SelectedOperatorArgs) == 2 &&
		b.Clause.SelectedOperatorArgs[0].ResultUsage().Type.ID() == sqltype.JSONB &&
		b.Clause.SelectedOperatorArgs[1].ResultUsage().Type.ID() == sqltype.JSONPath:
		return functions.BindJSONBPathOperator(b, r)
	case name == "#-":
		return functions.BindJSONBPathDelete(b, r)
	case bitwiseName(name):
		return b.BindBitwise(r)
	case arithmeticName(name) || symbolicArithmeticName(name):
		return b.BindArithmetic(r)
	default:
		return b.BindComparison(r)
	}
}
func builtinOperatorSpelling(name string) bool {
	_, comparison := ComparisonOperator(name)
	return comparison || arithmeticName(name) || bitwiseName(name) || name == "||" || name == "^@" || likeName(name) || functions.PosixName(name) || ArrayMembershipName(name) || functions.JSONExtractionName(name) || functions.JSONBExistenceName(name) || name == "#-" || name == "@>" || name == "<@" || name == "&&" || name == "<<" || name == ">>" || name == "&<" || name == "&>" || name == "-|-"
}

type operatorCandidateSet struct {
	catalog     catalog.Read
	descriptors []*catalog.Operator
	hasCustom   bool
	// Presence is independent of materialization for seed-only binary comparisons.
	available bool
}

func (b *Binder) operatorCandidates(name parser.ObjectName, unary bool) (operatorCandidateSet, error) {
	auth, err := b.BindingAuthorization()
	if err != nil {
		return operatorCandidateSet{}, err
	}
	path, err := b.Host.LookupNamespaces(auth, name, OperatorLookup)
	if err != nil {
		return operatorCandidateSet{}, err
	}
	view := auth.ReadCatalog()
	spelling := name.Parts[len(name.Parts)-1].Text
	if _, comparison := ComparisonOperator(spelling); !unary && comparison {
		available, custom := false, false
		for i := 0; i < path.Count() && !custom; i++ {
			for _, address := range view.State().OperatorNames().Buckets[catalog.OperatorNameKey{path.At(i), spelling}] {
				d := view.MustOperator(address)
				if d.Left == 0 {
					continue
				}
				available = true
				if !routine.OperatorSeeded(d) {
					custom = true
					break
				}
			}
		}
		if !custom {
			return operatorCandidateSet{catalog: view, available: available}, nil
		}
		// Even a masked custom descriptor requires ordered signature masking below.
	}
	var candidates []*catalog.Operator
	seen := make(map[OperatorSignature]bool)
	custom := false
	for i := range path.Count() {
		for _, address := range view.State().OperatorNames().Buckets[catalog.OperatorNameKey{path.At(i), spelling}] {
			d := view.MustOperator(address)
			if (d.Left == 0) != unary {
				continue
			}
			key := OperatorSignature{Left: d.Left, Right: d.Right}
			if seen[key] {
				continue
			}
			seen[key] = true
			candidates = append(candidates, d)
			custom = custom || !routine.OperatorSeeded(d)
		}
	}
	return operatorCandidateSet{catalog: view, descriptors: candidates, hasCustom: custom, available: len(candidates) != 0}, nil
}

func (b *Binder) selectCatalogOperator(set operatorCandidateSet, raw parser.OperatorExpr, left, right *sqltype.Descriptor, unary bool) (*catalog.Operator, error) {
	candidates := set.descriptors
	signatures := make([]sqltype.OperatorInputs, len(candidates))
	for i, d := range candidates {
		signatures[i] = sqltype.OperatorInputs{Left: sqltype.ID(d.Left), Right: sqltype.ID(d.Right)}
	}
	selected, err := sqltype.ResolveOperator(signatures, left, right, unary)
	diagnosticLeft := left
	if unary {
		diagnosticLeft = right
	}
	if err != nil {
		return nil, functions.OperatorError(err, diagnosticLeft, right, raw)
	}
	d := candidates[selected]
	if d.Function == 0 && !routine.OperatorSeeded(d) {
		message := routine.OperatorInputName(set.catalog, d.Left, d.Right, raw.Name)
		diagnostic := sqlError("42883", "operator is only a shell: "+message).(*sqlstate.Error)
		if raw.Pos.Character > 0 {
			diagnostic.Position = strconv.Itoa(raw.Pos.Character)
		}
		return nil, diagnostic
	}
	return d, nil
}

func requireBuiltinComparison(d *catalog.Operator, pos parser.Position) error {
	if !routine.OperatorSeeded(d) {
		return expr.ErrorAt("0A000", "custom operators are unsupported in this comparison context", pos)
	}
	return nil
}

func (b *Binder) BindCatalogOperator(raw parser.OperatorExpr, policy ExpressionPolicy) (expr.Expr, error) {
	if len(raw.Name.Parts) == 0 || len(raw.Args) < 1 || len(raw.Args) > 2 {
		return nil, fmt.Errorf("stuntgres: invalid operator expression")
	}
	if raw.SQLSimilar {
		return b.bindBuiltinOperator(raw, policy)
	}
	// Preserve fresh constructor field selection before scalar catalog retagging.
	if subquery, ok := rowSubqueryComparison(raw); ok {
		return b.bindSubquery(subquery)
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	if operator, ok := ComparisonOperator(name); ok && len(raw.Args) == 2 {
		if left, ok := raw.Args[0].(parser.RowExpr); ok {
			if right, ok := raw.Args[1].(parser.RowExpr); ok {
				return b.bindRowComparison(raw, operator, left, right)
			}
		}
	}
	set, err := b.operatorCandidates(raw.Name, len(raw.Args) == 1)
	if err != nil {
		return nil, err
	}
	if !set.hasCustom && builtinOperatorSpelling(name) {
		if _, comparison := ComparisonOperator(name); comparison {
			return b.bindComparisonCandidates(raw, &set)
		}
		return b.bindBuiltinOperator(raw, policy)
	}
	operands := make([]ComparisonOperand, len(raw.Args))
	for i, arg := range raw.Args {
		operands[i], err = b.comparisonOperand(arg)
		if err != nil {
			return nil, err
		}
	}
	var left, right *sqltype.Descriptor
	if len(operands) == 1 {
		right = operands[0].descriptor()
	} else {
		left, right = operands[0].descriptor(), operands[1].descriptor()
	}
	d, err := b.selectCatalogOperator(set, raw, left, right, len(operands) == 1)
	if err != nil {
		return nil, err
	}
	targets := []ids.OID{d.Right}
	if len(operands) == 2 {
		targets = []ids.OID{d.Left, d.Right}
	}
	args := make([]expr.Expr, len(operands))
	for i, o := range operands {
		target := b.Type(sqltype.ID(targets[i]))
		if target.Concrete() {
			args[i], err = b.CoerceConditional(o, target.Usage(), name)
		} else {
			source := o.descriptor()
			if source == nil && len(operands) == 2 {
				source = operands[1-i].descriptor()
				if source != nil && (targets[i] == 2283 || targets[i] == 5077) && source.BaseType().IsArray() {
					source = source.BaseType().Element()
				}
			}
			if source == nil {
				return nil, expr.ErrorAt("42804", "could not determine polymorphic type because input has type unknown", raw.Pos)
			}
			args[i], err = b.finalizeOperand(o, source)
		}
		if err != nil {
			return nil, err
		}
	}
	if routine.OperatorSeeded(d) {
		// Seed metadata is not an invocation descriptor. Dispatch the selected
		// builtin through its existing real expression provider, reusing the
		// operands already bound and coerced by catalog resolution.
		previous, previousSyntax := b.Clause.SelectedOperatorArgs, b.Clause.SelectedOperatorSyntax
		b.Clause.SelectedOperatorArgs, b.Clause.SelectedOperatorSyntax = args, raw.Args
		defer func() {
			b.Clause.SelectedOperatorArgs, b.Clause.SelectedOperatorSyntax = previous, previousSyntax
		}()
		raw.Args = make([]parser.Expr, len(args))
		for i := range args {
			raw.Args[i] = parser.ParameterExpr{Number: -i - 1, Pos: args[i].SourcePosition()}
		}
		return b.bindBuiltinOperator(raw, policy)
	}
	routine, _ := set.catalog.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: d.Function})
	if routine == nil {
		return nil, fmt.Errorf("stuntgres: missing operator implementation")
	}
	provider, err := b.BindSelectedOperatorRoutine(routine, args, raw.Pos)
	if err != nil {
		return nil, err
	}
	ref := b.Host.CatalogOwner().Domain.Ref(set.catalog.Epoch(), d.Address())
	b.dependency(ref, d.Revision())
	info := expr.InfoOf(provider)
	info.Pos = raw.Pos
	// Admission belongs to the actual provider only, not twice to the wrapper.
	info.RoutineReference = nil
	return plan.SelectedOperatorExpr{Info: info, Operator: d, Ref: ref, Provider: provider}, nil
}

// Provider dispatch is constrained by the selected routine's exact declaration.
// Synthetic argument slots are binder-local, cannot occur in parsed SQL, and
// are restored even if the selected provider rejects an unsupported signature.
func (b *Binder) BindSelectedOperatorRoutine(d *catalog.Procedure, args []expr.Expr, pos parser.Position) (expr.Expr, error) {
	previous, previousSyntax := b.Clause.SelectedOperatorArgs, b.Clause.SelectedOperatorSyntax
	// This entry receives bound operands, not source recipes. Clear any
	// enclosing SQL-call syntax rather than attributing it to these slots.
	b.Clause.SelectedOperatorArgs, b.Clause.SelectedOperatorSyntax = args, nil
	defer func() {
		b.Clause.SelectedOperatorArgs, b.Clause.SelectedOperatorSyntax = previous, previousSyntax
	}()
	rawArgs := make([]parser.Expr, len(args))
	for i := range args {
		rawArgs[i] = parser.ParameterExpr{Number: -i - 1, Pos: args[i].SourcePosition()}
	}
	var node expr.Expr
	var err error
	if d.Builtin != nil && d.Builtin.Providers&functions.OperatorRoutineProvider != 0 {
		found := false
		for key, declaration := range routine.OperatorRoutines() {
			if declaration != d.Builtin {
				continue
			}
			if len(args) == 1 && key.Left != 0 || len(args) == 2 && key.Left == 0 {
				continue
			}
			signature := strconv.FormatUint(uint64(key.Right), 10)
			if key.Left != 0 {
				signature = strconv.FormatUint(uint64(key.Left), 10) + "," + signature
			}
			if catalog.ProcedureSignature(signature) != d.Signature {
				continue
			}
			raw := parser.OperatorExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "pg_catalog", Pos: pos}, {Text: key.Name, Pos: pos}}}, Args: rawArgs, Pos: pos}
			node, err = b.bindBuiltinOperator(raw, ExpressionPolicy{Context: ProjectionContext})
			found = true
			break
		}
		if !found {
			return nil, sqlError("0A000", "operator implementation has no registered provider")
		}
	} else {
		namespace := b.BindingCatalog().Get(d.Namespace()).Name()
		raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: namespace, Pos: pos}, {Text: d.Name(), Pos: pos}}}, Args: rawArgs, Pos: pos}
		node, err = b.BindFunctionProvider(raw)
	}
	if err != nil {
		return nil, err
	}
	actual, err := routine.ExpressionRoutineDescriptor(node)
	if err != nil {
		return nil, err
	}
	if actual == nil || actual.Address() != d.Address() {
		return nil, sqlError("0A000", "operator implementation has no registered provider")
	}
	return node, nil
}
