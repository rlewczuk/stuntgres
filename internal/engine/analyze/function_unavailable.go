package analyze

import (
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Release identities are diagnostic evidence only: a viable signature here
// reports a native capability boundary, never an executable provider or pg_proc
// row. Missing names and incompatible signatures retain ordinary resolution errors.
func (b *Binder) unavailableFunctionError(raw parser.FunctionExpr, visible bool) error {
	identities := catalog.BuiltinRoutineIdentitiesNamed(raw.Name.Parts[len(raw.Name.Parts)-1].Text)
	if !visible || len(identities) == 0 || len(raw.ArgNames) != 0 || raw.Variadic || len(raw.Args) > 100 {
		return b.functionSignatureError(raw)
	}
	operands := make([]ComparisonOperand, len(raw.Args))
	names := make([]string, len(raw.Args))
	for i, arg := range raw.Args {
		operand, err := b.comparisonOperand(arg)
		if err != nil {
			return err
		}
		operands[i] = operand
		names[i] = "unknown"
		if typ := operand.descriptor(); typ != nil {
			names[i] = b.DiagnosticTypeName(typ)
		}
	}
	for _, identity := range identities {
		if b.unavailableSignatureMatches(identity.Signature, routine.IdentityDefaults(identity.ID), operands) {
			return expr.ErrorAt("0A000", "functions are unsupported", raw.Pos)
		}
	}
	return b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
}

func (b *Binder) unavailableSignatureMatches(signature catalog.ProcedureSignature, defaults uint8, operands []ComparisonOperand) bool {
	if signature == "" {
		return len(operands) == 0
	}
	arity := strings.Count(string(signature), ",") + 1
	if len(operands) > arity || arity-len(operands) > int(defaults) {
		return false
	}
	i := 0
	for part := range strings.SplitSeq(string(signature), ",") {
		if i == len(operands) {
			return true // Remaining trailing formals have declared defaults.
		}
		id, err := strconv.ParseUint(part, 10, 32)
		if err != nil {
			return false
		}
		target := b.Type(sqltype.ID(id))
		if target == nil || !target.Concrete() {
			return false
		}
		if operands[i].descriptor() != nil {
			if err := catalog.CanCoerce(b.BindingCatalog(), operands[i].Node.ResultUsage(), target.Usage(), sqltype.Implicit); err != nil {
				return false
			}
		}
		i++
	}
	return true
}
