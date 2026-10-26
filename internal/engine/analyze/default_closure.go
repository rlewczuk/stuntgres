package analyze

import (
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// CryptoExpressionSignature returns the catalog signature of a pgcrypto call,
// or "" when its arguments match no installed signature.
func CryptoExpressionSignature(n plan.InstalledCryptoExpr) catalog.ProcedureSignature {
	if n.Plan.Name() == "gen_random_bytes" {
		if len(n.Args) == 1 && n.Args[0].ResultUsage().Type.ID() == sqltype.Int4 {
			return "23"
		}
		return ""
	}
	if n.Plan.Name() == "gen_salt" {
		if len(n.Args) == 1 {
			return "25"
		}
		if len(n.Args) == 2 {
			return "25,23"
		}
		return ""
	}
	if len(n.Args) == 4 && (n.Plan.Name() == "encrypt_iv" || n.Plan.Name() == "decrypt_iv") {
		return "17,17,17,25"
	}
	if len(n.Args) == 2 {
		if n.Args[0].ResultUsage().Type.ID() == sqltype.Bytea {
			return "17,25"
		}
		return "25,25"
	}
	if len(n.Args) == 3 {
		if n.Args[0].ResultUsage().Type.ID() == sqltype.Bytea {
			return "17,17,25"
		}
		return "25,25,25"
	}
	return ""
}

// Only genuine installed crypto calls introduce volatility here. Like the
// existing UUID/time default owners, recurse through supported scalar wrappers
// without admitting unrelated volatile producers or frame-dependent recipes.
func ClosedPgcryptoDefault(node expr.Expr) bool {
	if node == nil {
		return true
	}
	p := node.Properties()
	if p.RowDependent || p.ParameterDependent || p.StageDependent {
		return false
	}
	switch n := node.(type) {
	case plan.InstalledCryptoExpr:
		if n.Procedure == nil || extension.PgcryptoProviderOf(n.Procedure) != extension.StockPgcrypto || n.Ref.Address != n.Procedure.Address() || n.Ref.Epoch == 0 || n.Validate() != nil {
			return false
		}
		member, ok := functions.PgcryptoLookup(n.Plan.Name(), string(CryptoExpressionSignature(n)))
		if !ok || string(n.Procedure.Signature) != member.Signature || n.Procedure.Result != member.Result || n.Procedure.Volatility != member.Volatility || n.Procedure.Strict != member.Strict {
			return false
		}
	case plan.CoercionExpr, plan.CollationExpr, plan.RowExpr, plan.FieldExpr,
		plan.ComparisonExpr, plan.ArithmeticExpr, plan.BitwiseExpr, plan.LikeExpr,
		plan.NullTestExpr, plan.TruthTestExpr, plan.LogicalExpr, plan.DistinctExpr,
		plan.ConditionalExpr, plan.CaseExpr, plan.ScalarArrayComparisonExpr,
		functions.Binary,
		functions.String,
		functions.StringVariadic,
		functions.Format,
		functions.NullCount,
		functions.LikeEscape,
		functions.JSONCreate:
	default:
		return p.Volatility <= expr.Stable
	}
	valid := true
	plan.ExpressionChildren(node, func(child expr.Expr) { valid = valid && ClosedPgcryptoDefault(child) })
	return valid
}

// Only fixed nextval leaves remain volatile inside constructor/field/cast wrappers.
func ClosedSequenceDefault(node expr.Expr) bool {
	if node == nil || node.Properties().RowDependent || node.Properties().ParameterDependent || node.Properties().StageDependent {
		return false
	}
	switch n := node.(type) {
	case plan.CoercionExpr:
		return ClosedSequenceDefault(n.Child)
	case plan.RowExpr:
		for _, field := range n.Fields {
			if field.Properties().Volatility > expr.Stable && !ClosedSequenceDefault(field) {
				return false
			}
		}
		return true
	case plan.FieldExpr:
		return ClosedSequenceDefault(n.Base)
	case functions.SequenceCall:
		if n.Name != "nextval" || n.Identity || len(n.Args) != 1 || n.Ref == (ids.ObjectRef{}) || n.ResultUsage().Type.ID() != sqltype.Int8 || n.Properties() != (expr.Properties{Volatility: expr.Volatile}) {
			return false
		}
		literal, ok := n.Args[0].(plan.LiteralExpr)
		if !ok || literal.Value.IsNull() || literal.ResultUsage().Type.ID() != sqltype.Regclass || literal.Properties() != (expr.Properties{}) {
			return false
		}
		text, err := literal.Value.Text()
		return err == nil && text == strconv.FormatUint(uint64(n.Ref.Address.ObjectID), 10)
	}
	return false
}

// ClosedRegclassDefault reports whether node is a closed regclass default:
// literals under coercion, row, field, array and subscript wrappers only.
func ClosedRegclassDefault(node expr.Expr) bool {
	if node == nil || node.Properties().RowDependent || node.Properties().ParameterDependent || node.Properties().StageDependent {
		return false
	}
	switch n := node.(type) {
	case plan.LiteralExpr:
		return true
	case plan.CoercionExpr:
		return ClosedRegclassDefault(n.Child)
	case plan.RowExpr:
		for _, field := range n.Fields {
			if !ClosedRegclassDefault(field) {
				return false
			}
		}
		return true
	case plan.FieldExpr:
		return ClosedRegclassDefault(n.Base)
	case plan.ArrayExpr:
		for _, element := range n.Elements {
			if !ClosedRegclassDefault(element) {
				return false
			}
		}
		return true
	case plan.SubscriptExpr:
		if !ClosedRegclassDefault(n.Base) {
			return false
		}
		for _, s := range n.Subscripts {
			if s.Lower != nil && !ClosedRegclassDefault(s.Lower) || s.Upper != nil && !ClosedRegclassDefault(s.Upper) {
				return false
			}
		}
		return true
	}
	return false
}

// Only selected time leaves may introduce volatility into this closed default.
// Other volatile producers retain their separately owned admission rules.
func ClosedTransactionTimeDefault(node expr.Expr) bool {
	if node == nil {
		return true
	}
	p := node.Properties()
	if p.RowDependent || p.ParameterDependent || p.StageDependent {
		return false
	}
	switch n := node.(type) {
	case functions.TransactionTime:
		return n.Validate(expr.PropertiesMatch(n.Property, n.Kind.IntrinsicRule())) == nil
	case functions.DatetimeSleep:
		return n.Validate(expr.PropertiesMatch(n.Property, functions.DatetimeSleepIntrinsicRule()), n.Arg != nil) && ClosedTransactionTimeDefault(n.Arg)
	case plan.RowExpr, plan.FieldExpr:
		valid := true
		plan.ExpressionChildren(node, func(child expr.Expr) { valid = valid && ClosedTransactionTimeDefault(child) })
		return valid
	case plan.ExtensionPathsExpr:
		return false
	case plan.ScalarArrayComparisonExpr:
		valid := true
		plan.ExpressionChildren(node, func(child expr.Expr) { valid = valid && ClosedTransactionTimeDefault(child) })
		return valid
	case plan.CoercionExpr, plan.CollationExpr, plan.ComparisonExpr, plan.ArithmeticExpr, plan.BitwiseExpr, plan.LikeExpr, plan.NullTestExpr, plan.TruthTestExpr, plan.LogicalExpr, plan.DistinctExpr, plan.ConditionalExpr, plan.CaseExpr:
		valid := true
		plan.ExpressionChildren(node, func(child expr.Expr) { valid = valid && ClosedTransactionTimeDefault(child) })
		return valid
	case expr.ConditionalClosure:
		return n.ClosedDefault(ClosedTransactionTimeDefault)
	case expr.ClosureTransparent:
		// Function nodes the closure looks through.
		valid := true
		plan.ExpressionChildren(node, func(child expr.Expr) { valid = valid && ClosedTransactionTimeDefault(child) })
		return valid
	default:
		return p.Volatility <= expr.Stable
	}
}

// Only selected UUID leaves may introduce volatility into a closed DEFAULT.
// Shift arguments themselves must be closed Immutable/Stable trees. This does
// not extend the separate fixed-nextval allowance or admit random()/setseed().
func ClosedUUIDDefault(node expr.Expr) bool {
	if node == nil {
		return true
	}
	p := node.Properties()
	if p.RowDependent || p.ParameterDependent || p.StageDependent {
		return false
	}
	switch n := node.(type) {
	case plan.InstalledUUIDOsspExpr:
		if !ValidInstalledUUIDOsspShape(n) {
			return false
		}
		for _, arg := range n.Args {
			if arg.Properties().Volatility > expr.Stable || !ClosedUUIDDefault(arg) {
				return false
			}
		}
		return true
	case functions.UUIDGeneration:
		for _, arg := range n.Args {
			if arg.Properties().Volatility > expr.Stable || !ClosedUUIDDefault(arg) {
				return false
			}
		}
		return true
	case plan.RowExpr, plan.FieldExpr:
		valid := true
		plan.ExpressionChildren(node, func(child expr.Expr) { valid = valid && ClosedUUIDDefault(child) })
		return valid
	case functions.DatetimeTrunc, functions.DatetimeBin, functions.DatetimeZoneCalendar:
		// Looked through here only; the transaction-time check closes them by
		// volatility.
		valid := true
		plan.ExpressionChildren(node, func(child expr.Expr) { valid = valid && ClosedUUIDDefault(child) })
		return valid
	case plan.ScalarArrayComparisonExpr:
		valid := true
		plan.ExpressionChildren(node, func(child expr.Expr) { valid = valid && ClosedUUIDDefault(child) })
		return valid
	case plan.CoercionExpr, plan.CollationExpr, plan.ComparisonExpr, plan.ArithmeticExpr, plan.BitwiseExpr, plan.LikeExpr, plan.NullTestExpr, plan.TruthTestExpr, plan.LogicalExpr, plan.DistinctExpr, plan.ConditionalExpr, plan.CaseExpr:
		valid := true
		plan.ExpressionChildren(node, func(child expr.Expr) { valid = valid && ClosedUUIDDefault(child) })
		return valid
	case expr.ClosureTransparent:
		// Function nodes the closure looks through.
		valid := true
		plan.ExpressionChildren(node, func(child expr.Expr) { valid = valid && ClosedUUIDDefault(child) })
		return valid
	default:
		return p.Volatility <= expr.Stable
	}
}

// ValidInstalledUUIDOsspShape reports whether n is a well-formed call of an
// installed uuid-ossp routine.
func ValidInstalledUUIDOsspShape(n plan.InstalledUUIDOsspExpr) bool {
	properties := n.Properties()
	// Group rewriting adds execution-stage dependence to a valid call.
	// DEFAULT closure separately rejects that dependence before shape checks.
	properties.StageDependent = false
	if n.Procedure == nil {
		return false
	}
	member, selected := functions.UUIDOsspLookupSelector(n.Name)
	if !selected || string(n.Procedure.Signature) != member.Signature || n.RoutineReference != n.Procedure || extension.UUIDOsspProviderOf(n.Procedure) != extension.StockUUIDOssp || n.Ref.Address != n.Procedure.Address() || n.Ref.Epoch == 0 || n.Name != member.Selector || n.Timestamp != nil || n.Addition != (sqltype.Arithmetic{}) || n.ResultUsage().Type == nil || n.ResultUsage().Type.ID() != sqltype.UUID || !n.ResultUsage().Valid() {
		return false
	}
	count := 0
	if member.Signature != "" {
		count = 2
	}
	if len(n.Args) != count {
		return false
	}
	if _, err := functions.NewUUIDGeneration(n.Info, n.Name, n.Args, nil); err != nil {
		return false
	}
	expected := expr.Properties{Volatility: member.Volatility}
	for _, arg := range n.Args {
		expected = expr.MergeProperties(expected, arg.Properties())
	}
	expected.StageDependent = false
	return properties == expected
}

// A selected SQL routine executes inside the existing DEFAULT statement owner.
// Its body is neither evaluated nor traversed at definition time. Closure here
// concerns caller arguments only; the callee performs ordinary live query/ACL
// admission when the default is demanded. Unrelated volatile producers retain
// their existing, independently bounded default admission rules.
func ClosedSQLRoutineDefault(node expr.Expr) bool {
	valid, containsSQL := SQLRoutineDefaultClosure(node)
	return valid && containsSQL
}

// Keep the SQL extension distinct from the legacy whole-expression closures:
// transparent wrappers around nextval alone must not become newly admissible.
func SQLRoutineDefaultClosure(node expr.Expr) (valid, containsSQL bool) {
	if node == nil {
		return true, false
	}
	p := node.Properties()
	if p.RowDependent || p.ParameterDependent || p.StageDependent {
		return false, false
	}
	if p.Volatility <= expr.Stable {
		return true, false
	}
	switch n := node.(type) {
	case plan.RangeConstructorExpr:
		if n.ValidateStoredProvider() != nil {
			return false, false
		}
	case plan.SQLRoutineExpr:
		if n.Procedure == nil || n.Procedure.SQL == nil || n.Ref == (ids.ObjectRef{}) || n.Ref.Address != n.Procedure.Address() || n.Info.RoutineReference != n.Procedure || n.ValidateStoredProvider() != nil {
			return false, false
		}
		containsSQL = true
	case plan.CoercionExpr, plan.CollationExpr, plan.RowExpr, plan.FieldExpr, plan.ArrayExpr, plan.SubscriptExpr,
		plan.ComparisonExpr, plan.ArithmeticExpr, plan.BitwiseExpr, plan.LikeExpr,
		plan.NullTestExpr, plan.TruthTestExpr, plan.LogicalExpr, plan.DistinctExpr,
		plan.ConditionalExpr, plan.CaseExpr, plan.ScalarArrayComparisonExpr:
	case expr.ClosureTransparent:
	default:
		return ClosedSequenceDefault(node) || ClosedUUIDDefault(node) || ClosedTransactionTimeDefault(node) || ClosedPgcryptoDefault(node), false
	}
	valid = true
	plan.ExpressionChildren(node, func(child expr.Expr) {
		if valid {
			childValid, childSQL := SQLRoutineDefaultClosure(child)
			valid = childValid
			containsSQL = containsSQL || childSQL
		}
	})
	return valid, containsSQL
}

// Validate provider-specific captured authority without assuming a dictionary.
func ValidInstalledText(n plan.InstalledTextExpr) bool {
	p := n.Procedure
	if p == nil || n.RoutineReference != p || n.Arg == nil || n.Usage.Type == nil || n.Arg.ResultUsage().Type == nil || p.Signature != catalog.UnaccentTextSignature || n.Arg.ResultUsage().Type.ID() != sqltype.Text || ids.OID(n.Usage.Type.ID()) != p.Result {
		return false
	}
	if extension.FuzzystrmatchProviderOf(p) != nil {
		return extension.FuzzystrmatchProviderOf(p) == extension.StockFuzzystrmatch && n.Function == "daitch_mokotoff" && p.Result == 1009 && p.Volatility == expr.Immutable && p.Strict && p.Conversion.Name() == "" && p.Dictionary == (ids.ObjectAddress{}) && n.Usage.Type.IsArray() && n.Usage.Type.Element() == n.Arg.ResultUsage().Type
	}
	return extension.UnaccentProviderOf(p) == extension.StockUnaccent && n.Function == "unaccent" && p.Conversion.Name() == "" && p.Result == 25 && p.Volatility == expr.Stable && p.Strict && n.Usage.Type == n.Arg.ResultUsage().Type
}
