package engine

import (
	"fmt"
	"strconv"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// The provider is the sole executable child. Its lexical children retain the
// selected call's ordered, already-coerced operands for SQL rendering.
type selectedOperatorExpression struct {
	expressionInfo
	operator *operatorDescriptor
	ref      ids.ObjectRef
	provider BoundExpr
}

func (e selectedOperatorExpression) Eval(ctx ExprContext) (sqltype.Value, error) {
	return e.provider.Eval(ctx)
}

func (b *expressionBinder) bindBuiltinOperator(r parser.OperatorExpr, policy expressionPolicy) (BoundExpr, error) {
	if r.SQLSimilar {
		return b.bindSimilar(r)
	}
	if subquery, ok := rowSubqueryComparison(r); ok {
		return b.bindSubquery(subquery)
	}
	name := r.Name.Parts[len(r.Name.Parts)-1].Text
	switch {
	case rangeSharedOperator(r):
		return b.bindRangeShared(r)
	case name == "||":
		return b.bindStringConcat(r)
	case name == "^@":
		return b.bindStringPrefix(r)
	case likeName(name):
		return b.bindLike(r)
	case len(r.Args) == 2 && functions.PosixName(name):
		return b.bindPosix(r)
	case arrayMembershipName(name):
		return b.bindArrayMembership(r)
	case jsonExtractionName(name):
		return b.bindJSONExtraction(r)
	case jsonbExistenceName(name):
		return b.bindJSONBExistence(r)
	case name == "#-":
		return b.bindJSONBPathDelete(r)
	case bitwiseName(name):
		return b.bindBitwise(r)
	case arithmeticName(name) || symbolicArithmeticName(name):
		return b.bindArithmetic(r)
	default:
		return b.bindComparison(r)
	}
}
func builtinOperatorSpelling(name string) bool {
	_, comparison := comparisonOperator(name)
	return comparison || arithmeticName(name) || bitwiseName(name) || name == "||" || name == "^@" || likeName(name) || functions.PosixName(name) || arrayMembershipName(name) || jsonExtractionName(name) || jsonbExistenceName(name) || name == "#-" || name == "@>" || name == "<@" || name == "&&" || name == "<<" || name == ">>" || name == "&<" || name == "&>" || name == "-|-"
}

type operatorCandidateSet struct {
	catalog     CatalogRead
	descriptors []*operatorDescriptor
	hasCustom   bool
	// Presence is independent of materialization for seed-only binary comparisons.
	available bool
}

func (b *expressionBinder) operatorCandidates(name parser.ObjectName, unary bool) (operatorCandidateSet, error) {
	auth, err := b.bindingAuthorization()
	if err != nil {
		return operatorCandidateSet{}, err
	}
	path, err := b.engine.lookupNamespaces(auth, name, operatorLookup)
	if err != nil {
		return operatorCandidateSet{}, err
	}
	spelling := name.Parts[len(name.Parts)-1].Text
	if _, comparison := comparisonOperator(spelling); !unary && comparison {
		available, custom := false, false
		for i := 0; i < path.count() && !custom; i++ {
			for _, address := range auth.catalog.state.operatorNames.buckets[operatorNameKey{path.at(i), spelling}] {
				d := auth.catalog.state.descriptors[address].(*operatorDescriptor)
				if d.left == 0 {
					continue
				}
				available = true
				if !d.seeded() {
					custom = true
					break
				}
			}
		}
		if !custom {
			return operatorCandidateSet{catalog: auth.catalog, available: available}, nil
		}
		// Even a masked custom descriptor requires ordered signature masking below.
	}
	var candidates []*operatorDescriptor
	seen := make(map[operatorSignature]bool)
	custom := false
	for i := range path.count() {
		for _, address := range auth.catalog.state.operatorNames.buckets[operatorNameKey{path.at(i), spelling}] {
			d := auth.catalog.state.descriptors[address].(*operatorDescriptor)
			if (d.left == 0) != unary {
				continue
			}
			key := operatorSignature{d.left, d.right}
			if seen[key] {
				continue
			}
			seen[key] = true
			candidates = append(candidates, d)
			custom = custom || !d.seeded()
		}
	}
	return operatorCandidateSet{catalog: auth.catalog, descriptors: candidates, hasCustom: custom, available: len(candidates) != 0}, nil
}

func (b *expressionBinder) selectCatalogOperator(set operatorCandidateSet, raw parser.OperatorExpr, left, right *sqltype.Descriptor, unary bool) (*operatorDescriptor, error) {
	candidates := set.descriptors
	signatures := make([]sqltype.OperatorInputs, len(candidates))
	for i, d := range candidates {
		signatures[i] = sqltype.OperatorInputs{Left: sqltype.ID(d.left), Right: sqltype.ID(d.right)}
	}
	selected, err := sqltype.ResolveOperator(signatures, left, right, unary)
	diagnosticLeft := left
	if unary {
		diagnosticLeft = right
	}
	if err != nil {
		return nil, operatorExpressionError(err, diagnosticLeft, right, raw)
	}
	d := candidates[selected]
	if d.function == 0 && !d.seeded() {
		message := operatorInputName(set.catalog, d.left, d.right, raw.Name)
		diagnostic := sqlError("42883", "operator is only a shell: "+message).(*sqlstate.Error)
		if raw.Pos.Character > 0 {
			diagnostic.Position = strconv.Itoa(raw.Pos.Character)
		}
		return nil, diagnostic
	}
	return d, nil
}

func requireBuiltinComparison(d *operatorDescriptor, pos parser.Position) error {
	if !d.seeded() {
		return expressionError("0A000", "custom operators are unsupported in this comparison context", pos)
	}
	return nil
}

func (b *expressionBinder) bindCatalogOperator(raw parser.OperatorExpr, policy expressionPolicy) (BoundExpr, error) {
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
	if operator, ok := comparisonOperator(name); ok && len(raw.Args) == 2 {
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
		if _, comparison := comparisonOperator(name); comparison {
			return b.bindComparisonCandidates(raw, &set)
		}
		return b.bindBuiltinOperator(raw, policy)
	}
	operands := make([]comparisonOperand, len(raw.Args))
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
	targets := []ids.OID{d.right}
	if len(operands) == 2 {
		targets = []ids.OID{d.left, d.right}
	}
	args := make([]BoundExpr, len(operands))
	for i, o := range operands {
		target := b.typ(sqltype.ID(targets[i]))
		if target.Concrete() {
			args[i], err = b.coerceConditional(o, target.Usage(), name)
		} else {
			source := o.descriptor()
			if source == nil && len(operands) == 2 {
				source = operands[1-i].descriptor()
				if source != nil && (targets[i] == 2283 || targets[i] == 5077) && source.BaseType().IsArray() {
					source = source.BaseType().Element()
				}
			}
			if source == nil {
				return nil, expressionError("42804", "could not determine polymorphic type because input has type unknown", raw.Pos)
			}
			args[i], err = b.finalizeOperand(o, source)
		}
		if err != nil {
			return nil, err
		}
	}
	if d.seeded() {
		// Seed metadata is not an invocation descriptor. Dispatch the selected
		// builtin through its existing real expression provider, reusing the
		// operands already bound and coerced by catalog resolution.
		previous := b.selectedOperatorArgs
		b.selectedOperatorArgs = args
		defer func() { b.selectedOperatorArgs = previous }()
		raw.Args = make([]parser.Expr, len(args))
		for i := range args {
			raw.Args[i] = parser.ParameterExpr{Number: -i - 1, Pos: args[i].SourcePosition()}
		}
		return b.bindBuiltinOperator(raw, policy)
	}
	routine, _ := set.catalog.state.descriptors[ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: d.function}].(*procedureDescriptor)
	if routine == nil {
		return nil, fmt.Errorf("stuntgres: missing operator implementation")
	}
	provider, err := b.bindSelectedOperatorRoutine(routine, args, raw.Pos)
	if err != nil {
		return nil, err
	}
	ref := b.engine.catalog.domain.Ref(set.catalog.Epoch(), d.address)
	b.dependency(ref, d.revision)
	info := boundExpressionInfo(provider)
	info.Pos = raw.Pos
	// Admission belongs to the actual provider only, not twice to the wrapper.
	info.RoutineReference = nil
	return selectedOperatorExpression{expressionInfo: info, operator: d, ref: ref, provider: provider}, nil
}

// Provider dispatch is constrained by the selected routine's exact declaration.
// Synthetic argument slots are binder-local, cannot occur in parsed SQL, and
// are restored even if the selected provider rejects an unsupported signature.
func (b *expressionBinder) bindSelectedOperatorRoutine(d *procedureDescriptor, args []BoundExpr, pos parser.Position) (BoundExpr, error) {
	previous := b.selectedOperatorArgs
	b.selectedOperatorArgs = args
	defer func() { b.selectedOperatorArgs = previous }()
	rawArgs := make([]parser.Expr, len(args))
	for i := range args {
		rawArgs[i] = parser.ParameterExpr{Number: -i - 1, Pos: args[i].SourcePosition()}
	}
	var node BoundExpr
	var err error
	if d.builtin != nil && d.builtin.providers&operatorRoutineProvider != 0 {
		found := false
		for key, declaration := range builtinOperatorRoutineByDeclaration {
			if declaration != d.builtin {
				continue
			}
			if len(args) == 1 && key.left != 0 || len(args) == 2 && key.left == 0 {
				continue
			}
			signature := strconv.FormatUint(uint64(key.right), 10)
			if key.left != 0 {
				signature = strconv.FormatUint(uint64(key.left), 10) + "," + signature
			}
			if procedureSignature(signature) != d.signature {
				continue
			}
			raw := parser.OperatorExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "pg_catalog", Pos: pos}, {Text: key.name, Pos: pos}}}, Args: rawArgs, Pos: pos}
			node, err = b.bindBuiltinOperator(raw, expressionPolicy{context: projectionContext})
			found = true
			break
		}
		if !found {
			return nil, sqlError("0A000", "operator implementation has no registered provider")
		}
	} else {
		namespace := b.bindingCatalog().state.descriptors[d.namespace].Name()
		raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: namespace, Pos: pos}, {Text: d.name, Pos: pos}}}, Args: rawArgs, Pos: pos}
		node, err = b.bindFunctionProvider(raw)
	}
	if err != nil {
		return nil, err
	}
	actual, err := expressionRoutineDescriptor(node)
	if err != nil {
		return nil, err
	}
	if actual == nil || actual.address != d.address {
		return nil, sqlError("0A000", "operator implementation has no registered provider")
	}
	return node, nil
}
