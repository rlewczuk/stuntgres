package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type Binary struct {
	expr.Info
	Plan   sqltype.BinaryPlan
	Args   []expr.Expr
	Syntax parser.FunctionSyntax
}

func (e Binary) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	for _, arg := range e.Args {
		if c.FunctionLiteralNull(arg) {
			return sqltype.Null(e.Usage.Type), nil
		}
	}
	var values [4]sqltype.Value
	for i, arg := range e.Args {
		value, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = value
	}
	if !e.Plan.Immutable() {
		return e.evalConversion(c, values[:len(e.Args)])
	}
	return e.Plan.Evaluate(ctx, values[:len(e.Args)]...)
}
func (e Binary) ValidateSyntax() error {
	if e.Syntax == parser.OrdinaryFunctionSyntax {
		return nil
	}
	if e.Syntax == parser.SQLFunctionSyntax {
		switch e.Plan.Name() {
		case "overlay":
			if len(e.Args) == 3 || len(e.Args) == 4 {
				return nil
			}
		case "substring":
			if len(e.Args) == 2 || len(e.Args) == 3 {
				return nil
			}
		case "position", "btrim", "ltrim", "rtrim":
			if len(e.Args) == 2 {
				return nil
			}
		}
	}
	return fmt.Errorf("stuntgres: invalid stored binary syntax")
}
func (e Binary) Validate() error {
	var usages [4]sqltype.Usage
	for i, arg := range e.Args {
		usages[i] = arg.ResultUsage()
	}
	plan, err := sqltype.BindBinary(e.Plan.Name(), e.Usage, usages[:len(e.Args)]...)
	if err != nil || plan != e.Plan {
		return fmt.Errorf("stuntgres: invalid stored binary provider")
	}
	return nil
}
func (e Binary) Reconstruct(info expr.Info, args []expr.Expr) (Binary, error) {
	var usages [4]sqltype.Usage
	for i, arg := range args {
		usages[i] = arg.ResultUsage()
	}
	plan, err := sqltype.BindBinary(e.Plan.Name(), info.Usage, usages[:len(args)]...)
	if err != nil {
		return e, err
	}
	e.Info, e.Args, e.Plan = info, args, plan
	return e, nil
}

// Binary implements expr.Composite.
var _ expr.Composite = Binary{}

func (e Binary) Children() []expr.Expr { return e.Args }
func (e Binary) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("Binary", children, len(e.Args)); err != nil {
		return nil, err
	}
	return rebuilt(e.Reconstruct(info, children))
}
func NewBinary(info expr.Info, name string, args []expr.Expr, syntax parser.FunctionSyntax) (Binary, error) {
	var usages [4]sqltype.Usage
	for i, arg := range args {
		usages[i] = arg.ResultUsage()
	}
	plan, err := sqltype.BindBinary(name, info.Usage, usages[:len(args)]...)
	if err != nil {
		return Binary{}, err
	}
	node := Binary{info, plan, args, syntax}
	return node, node.ValidateSyntax()
}
func BinaryTextType(d *sqltype.Descriptor) bool {
	if d == nil {
		return true
	}
	switch d.BaseType().ID() {
	case sqltype.Text, sqltype.VarChar, sqltype.BPChar, sqltype.Name, sqltype.Char:
		return true
	}
	return false
}
func BinaryDefaultAllowed(name string, count int) bool {
	return (name == "substring" || name == "substr") && (count == 2 || count == 3)
}

// BinaryBitCandidate identifies only the released measurement/slice overloads.
// Unknowns acquire the BIT family only when a known BIT/VARBIT operand selects it.
func BinaryBitCandidate(name string, variadic bool, args []Argument) bool {
	if variadic {
		return false
	}
	switch name {
	case "bit_count", "length", "bit_length", "octet_length":
		if len(args) != 1 {
			return false
		}
	case "substring":
		if len(args) != 2 && len(args) != 3 {
			return false
		}
	case "overlay":
		if len(args) != 3 && len(args) != 4 {
			return false
		}
	case "position":
		if len(args) != 2 {
			return false
		}
	default:
		return false
	}
	known := false
	for i, arg := range args {
		if BinaryArgumentID(name, i, sqltype.Bit) != sqltype.Bit {
			continue
		}
		if d := arg.Usage.Type; d != nil {
			id := d.BaseType().ID()
			if id != sqltype.Bit && id != sqltype.VarBit {
				return false
			}
			known = true
		}
	}
	return known
}
func binaryConversion(name string) bool {
	return name == "convert" || name == "convert_from" || name == "convert_to" || name == "encode" || name == "decode"
}
func BinaryIntrinsicStable(name string) bool {
	return name == "convert" || name == "convert_from" || name == "convert_to"
}
func BinaryArgumentAnalysis(name string, index int) AnalysisRequest {
	if binaryConversion(name) && BinaryArgumentID(name, index, sqltype.Bytea) != sqltype.Bytea {
		return AnalysisRequest{Intent: StringTextArgument}
	}
	return AnalysisRequest{Intent: StringOtherArgument}
}
func BinaryArgumentID(name string, index int, family sqltype.ID) sqltype.ID {
	switch name {
	case "get_bit", "set_bit":
		if index == 1 {
			if family == sqltype.Bit {
				return sqltype.Int4
			}
			return sqltype.Int8
		}
		if index == 2 {
			return sqltype.Int4
		}
	case "get_byte", "set_byte":
		if index > 0 {
			return sqltype.Int4
		}
	case "convert", "convert_from":
		if index > 0 {
			return sqltype.Name
		}
	case "convert_to":
		if index == 0 {
			return sqltype.Text
		}
		return sqltype.Name
	case "encode":
		if index > 0 {
			return sqltype.Text
		}
	case "decode":
		return sqltype.Text
	}
	if ((name == "substring" || name == "substr") && index >= 1) || (name == "overlay" && index >= 2) {
		return sqltype.Int4
	}
	return family
}
func BinaryResultID(name string, family sqltype.ID) sqltype.ID {
	switch name {
	case "length", "octet_length", "bit_length", "position", "get_bit", "get_byte":
		return sqltype.Int4
	case "crc32", "crc32c", "bit_count":
		return sqltype.Int8
	case "md5", "encode", "convert_from":
		return sqltype.Text
	}
	return family
}

type BinaryDemand uint8

func binaryTypeDemand(id sqltype.ID) BinaryDemand {
	switch id {
	case sqltype.Bytea:
		return 1
	case sqltype.Bit:
		return 2
	case sqltype.Int4:
		return 4
	case sqltype.Int8:
		return 8
	case sqltype.Name:
		return 16
	case sqltype.Text:
		return 32
	case sqltype.VarBit:
		return 64
	}
	return 0
}
func (d BinaryDemand) Needs(id sqltype.ID) bool { return d&binaryTypeDemand(id) != 0 }

type BinaryTypes struct{ Bytea, Bit, VarBit, Integer, Bigint, Name, Text sqltype.Usage }

func (t *BinaryTypes) Usage(id sqltype.ID) sqltype.Usage {
	switch id {
	case sqltype.Bytea:
		return t.Bytea
	case sqltype.Bit:
		return t.Bit
	case sqltype.VarBit:
		return t.VarBit
	case sqltype.Int4:
		return t.Integer
	case sqltype.Int8:
		return t.Bigint
	case sqltype.Name:
		return t.Name
	case sqltype.Text:
		return t.Text
	}
	return sqltype.Usage{}
}
func BinaryArgumentDemand(name string, family sqltype.ID, args []Argument, knownOnly bool) (demand BinaryDemand) {
	for i, arg := range args {
		if !knownOnly || arg.Usage.Type != nil {
			demand |= binaryTypeDemand(BinaryArgumentID(name, i, family))
		}
	}
	return
}
func BinaryResultDemand(name string, family sqltype.ID) BinaryDemand {
	return binaryTypeDemand(BinaryResultID(name, family)) | binaryTypeDemand(sqltype.Text)
}

type BinaryRoute uint8

const (
	BinaryCall BinaryRoute = iota
	BinaryPosixSubstring
	BinarySimilarSubstring
	BinaryTextSubstring
	BinaryTextUnsupported
	BinaryFunctionsUnsupported
)

type BinarySelection struct {
	Route      BinaryRoute
	Family     sqltype.ID
	Resolution Resolution
}
type binarySelectionKind uint8

const (
	binarySelected binarySelectionKind = iota
	binaryCoercionSelection
	binaryAccessSelection
)

type BinaryPreparation struct {
	Selection BinarySelection
	Demand    BinaryDemand
	kind      binarySelectionKind
}

func PrepareBinary(name string, visible, variadic, defaultConsumer bool, args []Argument) BinaryPreparation {
	selected := BinaryPreparation{Selection: BinarySelection{Family: sqltype.Bytea}}
	missing := BinaryPreparation{Selection: BinarySelection{Family: sqltype.Bytea, Resolution: Missing}}
	bits := BinaryBitCandidate(name, variadic, args)
	if bits && name != "bit_count" {
		if !visible {
			return missing
		}
		selected.Selection.Family = sqltype.Bit
		selected.kind = binaryCoercionSelection
		selected.Demand = BinaryArgumentDemand(name, sqltype.Bit, args, true)
		return selected
	}
	switch name {
	case "bit_count", "get_bit", "get_byte", "set_bit", "set_byte":
		arity := 2
		if name == "bit_count" {
			arity = 1
		} else if name == "set_bit" || name == "set_byte" {
			arity = 3
		}
		if !visible || len(args) != arity {
			return missing
		}
		selected.kind = binaryAccessSelection
		for candidate := range 2 {
			if candidate == 1 && name != "bit_count" && name != "get_bit" && name != "set_bit" {
				continue
			}
			for i, arg := range args {
				if arg.Usage.Type != nil {
					selected.Demand |= binaryTypeDemand(binaryAccessArgumentID(name, i, candidate))
				}
			}
		}
		return selected
	}
	if binaryConversion(name) {
		arity := 2
		if name == "convert" {
			arity = 3
		}
		if !visible || len(args) != arity {
			return missing
		}
		selected.kind = binaryCoercionSelection
		selected.Demand = BinaryArgumentDemand(name, sqltype.Bytea, args, true)
		return selected
	}
	switch name {
	case "crc32", "crc32c", "sha224", "sha256", "sha384", "sha512":
		if !visible || len(args) != 1 {
			return missing
		}
		if d := args[0].Usage.Type; d != nil && d.BaseType().ID() != sqltype.Bytea {
			return missing
		}
		return selected
	}
	sliced := name == "substring" || name == "substr"
	if !visible || variadic && !sliced {
		return missing
	}
	arity := 2
	if sliced && len(args) == 3 {
		arity = 3
	}
	textual := len(args) == arity
	for i, arg := range args {
		if BinaryArgumentID(name, i, sqltype.Bytea) == sqltype.Int4 {
			d := arg.Usage.Type
			textual = textual && (d == nil || d.BaseType().ID() == sqltype.Int2 || d.BaseType().ID() == sqltype.Int4)
		} else {
			textual = textual && BinaryTextType(arg.Usage.Type)
		}
	}
	if sliced && len(args) >= 2 && len(args) <= 3 && BinaryTextType(args[0].Usage.Type) {
		regex := name == "substring"
		for _, arg := range args[1:] {
			regex = regex && BinaryTextType(arg.Usage.Type)
		}
		if regex {
			selected.Selection.Route = BinarySimilarSubstring
			if len(args) == 2 {
				selected.Selection.Route = BinaryPosixSubstring
			}
			return selected
		}
		if textual {
			selected.Selection.Route = BinaryTextSubstring
			return selected
		}
	}
	if textual {
		selected.Selection.Route = BinaryTextUnsupported
		return selected
	}
	if len(args) != arity {
		return missing
	}
	if defaultConsumer {
		selected.Selection.Route = BinaryFunctionsUnsupported
		return selected
	}
	known := false
	for _, arg := range args {
		known = known || arg.Usage.Type != nil
	}
	if !known {
		return missing
	}
	selected.kind = binaryCoercionSelection
	selected.Demand = BinaryArgumentDemand(name, sqltype.Bytea, args, true)
	return selected
}
func binaryAccessArgumentID(name string, index, candidate int) sqltype.ID {
	if candidate == 1 {
		if index == 0 {
			return sqltype.Bit
		}
		return sqltype.Int4
	}
	return BinaryArgumentID(name, index, sqltype.Bytea)
}
func ResolveBinary(prepared BinaryPreparation, name string, args []Argument, types *BinaryTypes) BinarySelection {
	selection := prepared.Selection
	if selection.Resolution != Resolved || selection.Route != BinaryCall {
		return selection
	}
	switch prepared.kind {
	case binaryCoercionSelection:
		for i, arg := range args {
			if arg.Usage.Type != nil {
				if _, err := sqltype.PlanCoercion(arg.Usage, types.Usage(BinaryArgumentID(name, i, selection.Family)), sqltype.Implicit); err != nil {
					selection.Resolution = Missing
					return selection
				}
			}
		}
	case binaryAccessSelection:
		candidates := [2]bool{true, name == "bit_count" || name == "get_bit" || name == "set_bit"}
		exact := [2]int{}
		for candidate := range candidates {
			if !candidates[candidate] {
				continue
			}
			for i, arg := range args {
				d := arg.Usage.Type
				if d == nil {
					continue
				}
				target := binaryAccessArgumentID(name, i, candidate)
				if d.BaseType().ID() == target {
					exact[candidate]++
				}
				if _, err := sqltype.PlanCoercion(arg.Usage, types.Usage(target), sqltype.Implicit); err != nil {
					candidates[candidate] = false
					break
				}
			}
		}
		if candidates[0] && candidates[1] {
			if exact[0] > exact[1] {
				candidates[1] = false
			} else if exact[1] > exact[0] {
				candidates[0] = false
			} else {
				selection.Resolution = Ambiguous
				return selection
			}
		}
		if candidates[0] {
			return selection
		}
		if candidates[1] {
			selection.Family = sqltype.Bit
			return selection
		}
		selection.Resolution = Missing
	}
	return selection
}
func BinaryArgumentsViable(name string, family sqltype.ID, args []Argument, types *BinaryTypes) bool {
	valid := true
	for i, arg := range args {
		if arg.Usage.Type != nil {
			_, err := sqltype.PlanCoercion(arg.Usage, types.Usage(BinaryArgumentID(name, i, family)), sqltype.Implicit)
			valid = valid && err == nil
		}
	}
	return valid
}
func CoerceBinaryArguments(b ArgumentCoercer, name string, family sqltype.ID, arguments []Argument, types *BinaryTypes) ([]expr.Expr, error) {
	args := make([]expr.Expr, len(arguments))
	for i, argument := range arguments {
		target := types.Usage(BinaryArgumentID(name, i, family))
		intent := LikeArgument
		if target.Type.ID() == sqltype.Bit || target.Type.ID() == sqltype.VarBit {
			// Bit-string formals are unrestricted; preserve a stored source's
			// declared width only on the child.
			intent = SizeFormalArgument
		}
		arg, err := b.CoerceArgument(argument, CoercionRequest{Intent: intent, Target: target})
		if err != nil {
			return nil, err
		}
		args[i] = arg
	}
	return args, nil
}

func (e Binary) IntrinsicVolatility() expr.Volatility { return stableIf(!e.Plan.Immutable()) }
