package analyze

import (
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
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
	if n.Validate() != nil {
		return ""
	}
	return catalog.ProcedureSignature(n.Plan.Signature().SQL)
}

// Crypto calls retain their own declaration and argument-closure rules. Nested
// sslinfo inquiries delegate their authenticated scalar tree, never an arbitrary
// rejected subtree or unrelated volatile producer.
func ClosedPgcryptoDefault(node expr.Expr) bool {
	if node == nil {
		return true
	}
	p := node.Properties()
	if p.RowDependent || p.ParameterDependent || p.StageDependent {
		return false
	}
	switch n := node.(type) {
	case plan.InstalledXML2Expr:
		if !ValidInstalledXML2Shape(n) {
			return false
		}
		for _, child := range n.Args {
			if !ClosedPgcryptoDefault(child) {
				return false
			}
		}
		return true
	case plan.InstalledSSLInfoExpr:
		return ClosedSSLInfoDefault(n)
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

// Fixed nextval leaves remain volatile inside constructor/field/cast wrappers.
// A validated strict literal-NULL array call can retain those leaves as original
// dependencies: execution planning removes the call after visiting its children.
func ClosedSequenceDefault(node expr.Expr) bool {
	if node == nil || node.Properties().RowDependent || node.Properties().ParameterDependent || node.Properties().StageDependent {
		return false
	}
	switch n := node.(type) {
	case functions.ArrayRandom:
		if n.Validate() != nil {
			return false
		}
		if _, folded, err := exec.FoldStrictParameterNull(&exec.Context{}, n); err != nil || !folded {
			return false
		}
		for _, child := range n.Args {
			if child.Properties().Volatility > expr.Stable && !ClosedSequenceDefault(child) {
				return false
			}
		}
		return true
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
		if n.Procedure == nil || n.Procedure.RoutineDefinition() == nil || n.Ref == (ids.ObjectRef{}) || n.Ref.Address != n.Procedure.Address() || n.Info.RoutineReference != n.Procedure || n.ValidateStoredProvider() != nil {
			return false, false
		}
		containsSQL = true
	case plan.CoercionExpr, plan.CollationExpr, plan.RowExpr, plan.FieldExpr, plan.ArrayExpr, plan.SubscriptExpr,
		plan.ComparisonExpr, plan.ArithmeticExpr, plan.BitwiseExpr, plan.LikeExpr,
		plan.NullTestExpr, plan.TruthTestExpr, plan.LogicalExpr, plan.DistinctExpr,
		plan.ConditionalExpr, plan.CaseExpr, plan.ScalarArrayComparisonExpr:
	case expr.ClosureTransparent:
	default:
		return ClosedSequenceDefault(node) || ClosedUUIDDefault(node) || ClosedTransactionTimeDefault(node) || ClosedPgcryptoDefault(node) || ClosedSSLInfoDefault(node), false
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

// Installed heap statistics execute against the DEFAULT statement owner, just
// like stored SQL calls. Their intrinsic stage dependence is intentional: never
// lower volatility or admit row/parameter-dependent arguments to gain closure.
// Every retained provider leaf still needs its authentic installed declaration.
func ClosedPgstattupleDefault(node expr.Expr, view catalog.Read) bool {
	return closedPgstattupleExpression(node, view, false)
}

// ClosedPgstattupleCheck admits the same installed producers in an owner-bound
// CHECK. Row references retain the renderer's ordinary attribute validation;
// neither CHECK nor DEFAULT admits an unrelated stage-dependent producer.
func ClosedPgstattupleCheck(node expr.Expr, view catalog.Read) bool {
	return closedPgstattupleExpression(node, view, true)
}

func closedPgstattupleExpression(node expr.Expr, view catalog.Read, rowOwner bool) bool {
	if node == nil {
		return true
	}
	p := node.Properties()
	if p.RowDependent && !rowOwner || p.ParameterDependent {
		return false
	}
	switch n := node.(type) {
	case plan.InstalledXML2Expr:
		if !ValidInstalledXML2Shape(n) || p.StageDependent {
			return false
		}
		for _, child := range n.Args {
			if !closedPgstattupleExpression(child, view, rowOwner) {
				return false
			}
		}
		return true
	case plan.InstalledSSLInfoExpr:
		return !p.StageDependent && ValidInstalledSSLInfoShape(n) && !n.Procedure.Installed.ReturnsSet && closedPgstattupleExpression(n.Arg, view, rowOwner)
	case plan.InstalledPgFreespaceExpr:
		if n.Kind.Set() || n.Procedure == nil || n.CallRoutineReference() != n.Procedure {
			return false
		}
		current, err := exec.ValidateStoredRoutineDescriptor(view, n.Procedure)
		if err != nil || extension.ValidatePgFreespaceIdentity(view, current, n.Ref) != nil {
			return false
		}
		call := n.PgFreespace
		call.RoutineReference = current
		if extension.ValidatePgFreespaceCall(view, current, call) != nil {
			return false
		}
	case plan.InstalledPgBuffercacheEvictionExpr:
		if n.Procedure == nil || n.CallRoutineReference() != n.Procedure {
			return false
		}
		current, err := exec.ValidateStoredRoutineDescriptor(view, n.Procedure)
		if err != nil || extension.ValidatePgBuffercacheIdentity(view, current, n.Ref) != nil {
			return false
		}
		call := n.PgBuffercacheEviction
		call.RoutineReference = current
		if extension.ValidatePgBuffercacheEvictionCall(view, current, call) != nil {
			return false
		}
	case plan.InstalledPgBuffercacheAggregateExpr:
		if n.Kind.Set() || n.Procedure == nil || n.CallRoutineReference() != n.Procedure {
			return false
		}
		current, err := exec.ValidateStoredRoutineDescriptor(view, n.Procedure)
		if err != nil || extension.ValidatePgBuffercacheIdentity(view, current, n.Ref) != nil {
			return false
		}
		call := n.PgBuffercacheAggregate
		call.RoutineReference = current
		if extension.ValidatePgBuffercacheAggregateCall(view, current, call) != nil {
			return false
		}
	case plan.InstalledPgvisibilityExpr:
		if n.Kind.Set() || n.Procedure == nil || n.CallRoutineReference() != n.Procedure {
			return false
		}
		current, err := exec.ValidateStoredRoutineDescriptor(view, n.Procedure)
		if err != nil || extension.ValidatePgvisibilityIdentity(view, current, n.Ref) != nil {
			return false
		}
		call := n.Pgvisibility
		call.RoutineReference = current
		if extension.ValidatePgvisibilityCall(view, current, call) != nil {
			return false
		}
	case plan.InstalledPgsurgeryExpr:
		if n.Procedure == nil || n.CallRoutineReference() != n.Procedure {
			return false
		}
		current, err := exec.ValidateStoredRoutineDescriptor(view, n.Procedure)
		if err != nil || extension.ValidatePgsurgeryIdentity(view, current, n.Ref) != nil {
			return false
		}
		call := n.Pgsurgery
		call.RoutineReference = current
		if extension.ValidatePgsurgeryCall(view, current, call) != nil {
			return false
		}
	case plan.InstalledDblinkConnectionExpr:
		if n.Procedure == nil || n.CallRoutineReference() != n.Procedure {
			return false
		}
		current, err := exec.ValidateStoredRoutineDescriptor(view, n.Procedure)
		if err != nil || extension.ValidateDblinkIdentity(view, current, n.Ref) != nil {
			return false
		}
		call := n.DblinkConnection
		call.RoutineReference = current
		if extension.ValidateDblinkConnectionCall(view, current, call) != nil {
			return false
		}
	case plan.InstalledDblinkBuildSQLDeleteExpr:
		if n.Procedure == nil || n.CallRoutineReference() != n.Procedure {
			return false
		}
		current, err := exec.ValidateStoredRoutineDescriptor(view, n.Procedure)
		if err != nil || extension.ValidateDblinkIdentity(view, current, n.Ref) != nil {
			return false
		}
		call := n.DblinkBuildSQLDelete
		call.RoutineReference = current
		if extension.ValidateDblinkBuildSQLDeleteCall(view, current, call) != nil {
			return false
		}
	case plan.InstalledDblinkBuildSQLInsertExpr:
		if n.Procedure == nil || n.CallRoutineReference() != n.Procedure {
			return false
		}
		current, err := exec.ValidateStoredRoutineDescriptor(view, n.Procedure)
		if err != nil || extension.ValidateDblinkIdentity(view, current, n.Ref) != nil {
			return false
		}
		call := n.DblinkBuildSQLInsert
		call.RoutineReference = current
		if extension.ValidateDblinkBuildSQLInsertCall(view, current, call) != nil {
			return false
		}
	case plan.InstalledDblinkBuildSQLUpdateExpr:
		if n.Procedure == nil || n.CallRoutineReference() != n.Procedure {
			return false
		}
		current, err := exec.ValidateStoredRoutineDescriptor(view, n.Procedure)
		if err != nil || extension.ValidateDblinkIdentity(view, current, n.Ref) != nil {
			return false
		}
		call := n.DblinkBuildSQLUpdate
		call.RoutineReference = current
		if extension.ValidateDblinkBuildSQLUpdateCall(view, current, call) != nil {
			return false
		}
	case plan.InstalledPgstattupleExpr:
		if n.Procedure == nil || n.CallRoutineReference() != n.Procedure {
			return false
		}
		// Stored callers retain the selected immutable declaration across name,
		// namespace and ACL changes. Authenticate that declaration before using
		// the current sealed provider; pointer equality would reject relocation.
		current, err := exec.ValidateStoredRoutineDescriptor(view, n.Procedure)
		if err != nil || extension.ValidatePgstattupleIdentity(view, current, n.Ref) != nil {
			return false
		}
		call := n.Pgstattuple
		call.RoutineReference = current
		if extension.ValidatePgstattupleCall(view, current, call) != nil {
			return false
		}
	case plan.CoercionExpr, plan.CollationExpr, plan.RowExpr, plan.FieldExpr,
		plan.ComparisonExpr, plan.ArithmeticExpr, plan.BitwiseExpr, plan.LikeExpr,
		plan.NullTestExpr, plan.TruthTestExpr, plan.LogicalExpr, plan.DistinctExpr,
		plan.ConditionalExpr, plan.CaseExpr, plan.ScalarArrayComparisonExpr:
	default:
		return !p.StageDependent && (rowOwner || p.Volatility <= expr.Stable)
	}
	valid := true
	plan.ExpressionChildren(node, func(child expr.Expr) {
		valid = valid && closedPgstattupleExpression(child, view, rowOwner)
	})
	return valid
}

// Validate provider-specific captured authority without assuming a dictionary.
func ValidInstalledText(n plan.InstalledTextExpr) bool {
	p := n.Procedure
	if p == nil || n.RoutineReference != p || !n.ValidTypes() || n.Ref.Domain() == nil || n.Ref.Epoch == 0 || n.Ref.Address != p.Address() || n.Ref.Address.ClassID != ids.ProcedureClassID || n.Ref.Address.SubID != 0 || !p.Origin.Valid(p.Address()) || ids.OID(n.Usage.Type.ID()) != p.Result || p.Conversion.Name() != "" {
		return false
	}
	if p.RoutineKind != catalog.FunctionRoutine || p.SQL != nil || p.Builtin != nil || p.Foreign != nil || p.Host != nil || p.HostHandler != nil || p.HostConversion != nil || p.SecurityDefiner || p.Configuration != nil {
		return false
	}
	canonical, ok := p.Origin.Installation.Declarations[p.Origin.Declaration].(*catalog.Procedure)
	if !ok || canonical.Implementation != p.Implementation || canonical.Signature != p.Signature || canonical.Result != p.Result || canonical.Display != p.Display || canonical.Installed != p.Installed {
		return false
	}
	if extension.FuzzystrmatchProviderOf(p) != nil {
		return extension.FuzzystrmatchProviderOf(p) == extension.StockFuzzystrmatch && n.Function == "daitch_mokotoff" && n.Dictionary == nil && p.Signature == catalog.UnaccentTextSignature && p.Result == 1009 && p.Volatility == expr.Immutable && p.Strict && p.Dictionary == (ids.ObjectAddress{}) && n.Usage.Type.IsArray() && n.Usage.Type.Element() == n.Arg.ResultUsage().Type
	}
	signature := catalog.UnaccentTextSignature
	if n.Dictionary != nil {
		signature = catalog.UnaccentDictionarySignature
	}
	return extension.UnaccentProviderOf(p) == extension.StockUnaccent && n.Function == "unaccent" && p.Signature == signature && p.Result == 25 && p.Volatility == expr.Stable && p.Strict && n.Usage.Type == n.Arg.ResultUsage().Type
}
