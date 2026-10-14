package engine

import (
	"context"
	"fmt"
	"strings"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// The common sql entry validates provenance and records dependencies once.
func (r scalarRenderer) scalarFunctionSQL(ctx context.Context, view CatalogRead, node BoundExpr, u sqltype.Usage) (string, error) {
	switch n := node.(type) {
	case installedFunctionExpression:
		proc, ok := view.state.descriptors[n.ref.Address].(*procedureDescriptor)
		if !ok || !storedReferenceEpochValid(view, r.retainedConsumer(), n.ref) || n.ref.Address.SubID != 0 || proc != n.procedure || proc.signature != unaccentTextSignature || n.usage.Type.ID() != sqltype.Text || n.arg == nil || n.arg.ResultUsage().Type.ID() != sqltype.Text || view.state.descriptors[proc.dictionary] != n.dictionary || n.dictionary == nil || view.state.descriptors[n.dictionary.template] != n.template || n.template == nil {
			return "", fmt.Errorf("stuntgres: invalid stored installed function")
		}
		namespace, ok := view.state.descriptors[proc.namespace].(*namespaceDescriptor)
		if !ok {
			return "", fmt.Errorf("stuntgres: missing stored function namespace")
		}
		r.record(n.ref)
		return r.functionSQL(ctx, view, quoteIdentifier(namespace.name)+"."+quoteIdentifier(proc.name), []BoundExpr{n.arg})
	case functions.UUIDGeneration[BoundExpr, *EvalContext, ExprProperties]:
		if u.Type.ID() != sqltype.UUID || (n.Name != "uuidv7" && n.Name != "uuidv4" && n.Name != "gen_random_uuid") || len(n.Args) > 1 || len(n.Args) != 0 && n.Name != "uuidv7" {
			return "", fmt.Errorf("stuntgres: invalid stored UUID generation signature")
		}
		if len(n.Args) != 0 && n.Args[0].ResultUsage().Type.ID() != sqltype.Interval {
			return "", fmt.Errorf("stuntgres: invalid stored UUID shift")
		}
		r.uuidShiftIntervalNulls = len(n.Args) != 0
		return r.functionSQL(ctx, view, "pg_catalog."+n.Name, n.Args)
	case functions.Random[BoundExpr, *EvalContext, ExprProperties]:
		if r.owner == nil {
			return "", fmt.Errorf("stuntgres: volatile stored default")
		}
		argumentType := sqltype.Float8
		valid := false
		switch n.Name {
		case "random":
			if len(n.Args) == 0 {
				valid = u.Type.ID() == sqltype.Float8
			}
			if len(n.Args) == 2 {
				argumentType = u.Type.ID()
				valid = argumentType == sqltype.Int4 || argumentType == sqltype.Int8 || argumentType == sqltype.Numeric
			}
		case "random_normal":
			valid = len(n.Args) <= 2 && u.Type.ID() == sqltype.Float8
		case "setseed":
			valid = len(n.Args) == 1 && u.Type.ID() == sqltype.Void
		}
		if !valid {
			return "", fmt.Errorf("stuntgres: invalid stored random signature")
		}
		for _, arg := range n.Args {
			if arg == nil || arg.ResultUsage().Type == nil || arg.ResultUsage().Type.ID() != argumentType {
				return "", fmt.Errorf("stuntgres: invalid stored random argument")
			}
		}
		return r.functionSQL(ctx, view, "pg_catalog."+n.Name, n.Args)
	case functions.Binary[BoundExpr, *EvalContext, ExprProperties]:
		if len(n.Args) < 1 || len(n.Args) > 4 {
			return "", fmt.Errorf("stuntgres: invalid stored binary arity")
		}
		if err := n.ValidateSyntax(); err != nil {
			return "", err
		}
		for _, arg := range n.Args {
			if arg == nil {
				return "", fmt.Errorf("stuntgres: missing binary operand")
			}
		}
		if err := n.Validate(); err != nil {
			return "", err
		}
		if n.Plan.Name() != "||" && !jsonbExistenceName(n.Plan.Name()) {
			return renderBinaryCall(n.Plan.Name(), n.Syntax, n.Args, "pg_catalog."+n.Plan.Name(), func(arg BoundExpr) (string, error) { return r.sql(ctx, view, arg) })
		}
		left, err := r.sql(ctx, view, n.Args[0])
		if err != nil {
			return "", err
		}
		right, err := r.sql(ctx, view, n.Args[1])
		if err != nil {
			return "", err
		}
		return "(" + left + " OPERATOR(pg_catalog." + n.Plan.Name() + ") " + right + ")", nil
	case functions.DatetimeJustify[BoundExpr, *EvalContext, ExprProperties]:
		return r.functionSQL(ctx, view, "pg_catalog."+n.Plan.Name(), n.Arguments())
	case functions.DatetimeExtract[BoundExpr, *EvalContext, ExprProperties]:
		return datetimeExtractLogicalSQL(n, "pg_catalog."+n.Plan.Name(), func(arg BoundExpr) (string, error) { return r.sql(ctx, view, arg) })
	case functions.DatetimeOverlaps[BoundExpr, *EvalContext, ExprProperties]:
		return datetimeOverlapsLogicalSQL(n, func(arg BoundExpr) (string, error) { return r.sql(ctx, view, arg) })
	case functions.DatetimeBin[BoundExpr, *EvalContext, ExprProperties]:
		return r.functionSQL(ctx, view, "pg_catalog.date_bin", n.Arguments())
	case functions.DatetimeTrunc[BoundExpr, *EvalContext, ExprProperties]:
		return r.functionSQL(ctx, view, "pg_catalog.date_trunc", n.Arguments())
	case functions.DatetimeAgeFinite[BoundExpr, *EvalContext, ExprProperties]:
		return r.functionSQL(ctx, view, "pg_catalog."+n.Plan.Name(), n.Arguments())
	case functions.DatetimeMake[BoundExpr, *EvalContext, ExprProperties]:
		return datetimeMakeSQL(n, "pg_catalog."+n.Plan.Name(), func(arg BoundExpr) (string, error) { return r.sql(ctx, view, arg) }, quoteIdentifier)
	case functions.DatetimeFormatting[BoundExpr, *EvalContext, ExprProperties]:
		return r.functionSQL(ctx, view, "pg_catalog."+n.Plan.Name(), n.Args[:])
	case functions.NumericFormatting[BoundExpr, *EvalContext, ExprProperties]:
		return r.functionSQL(ctx, view, "pg_catalog."+n.Plan.Name(), n.Args[:])
	case functions.String[BoundExpr, *EvalContext, ExprProperties]:
		if len(n.Args) < 1 || len(n.Args) > 4 {
			return "", fmt.Errorf("stuntgres: invalid stored string arity")
		}
		for _, arg := range n.Args {
			if arg == nil {
				return "", fmt.Errorf("stuntgres: missing string operand")
			}
		}
		if err := n.Validate(); err != nil {
			return "", err
		}
		if r.commentSnapshot != nil {
			for i, arg := range n.Args {
				if !n.Textual[i] && arg.ResultUsage().Type.ContainsCatalogIdentifier() {
					*r.commentSnapshot = true
				}
			}
		}
		if n.Plan.Name() == "||" {
			left, err := r.stringArgumentSQL(ctx, view, n.Args[0])
			if err != nil {
				return "", err
			}
			right, err := r.stringArgumentSQL(ctx, view, n.Args[1])
			if err != nil {
				return "", err
			}
			return "(" + left + " OPERATOR(pg_catalog.||) " + right + ")", nil
		}
		parts := make([]string, len(n.Args))
		for i, arg := range n.Args {
			text, err := r.stringArgumentSQL(ctx, view, arg)
			if err != nil {
				return "", err
			}
			if sqltype.StringBaseConversion(n.Plan.Name()) {
				// Replay must select the stored integer overload, including
				// small BIGINT values and typed NULLs.
				declaration, err := catalogTypeDeclaration(view, n.Plan.ArgumentUsage(i))
				if err != nil {
					return "", err
				}
				text = "CAST(" + text + " AS " + declaration + ")"
			}
			parts[i] = text
		}
		if n.Syntax != parser.OrdinaryStringSyntax {
			return stringSpecialSQL(n, parts)
		}
		return "pg_catalog." + n.Plan.Name() + "(" + strings.Join(parts, ", ") + ")", nil
	case functions.ArrayInspect[BoundExpr, *EvalContext, ExprProperties]:
		return arrayInspectSQL(n, func(child BoundExpr) (string, error) { return r.sql(ctx, view, child) })
	case functions.ArraySearch[BoundExpr, *EvalContext, ExprProperties]:
		if err := n.Validate(); err != nil {
			return "", err
		}
		return arraySearchSQL(n, func(child BoundExpr) (string, error) { return r.sql(ctx, view, child) }, func(namespace, name string) string {
			if namespace != "" {
				return "OPERATOR(" + quoteIdentifier(namespace) + "." + name + ")"
			}
			return name
		})
	case functions.Enum[BoundExpr, *EvalContext, ExprProperties]:
		if len(n.Args) < 1 || len(n.Args) > 2 {
			return "", fmt.Errorf("stuntgres: invalid stored enum arity")
		}
		for _, arg := range n.Args {
			if arg == nil {
				return "", fmt.Errorf("stuntgres: missing enum operand")
			}
		}
		if err := n.Validate(); err != nil {
			return "", err
		}
		return r.functionSQL(ctx, view, "pg_catalog."+n.Plan.Name(), n.Args)
	case functions.Math[BoundExpr, *EvalContext, ExprProperties]:
		if len(n.Args) > 2 {
			return "", fmt.Errorf("stuntgres: invalid stored math arity")
		}
		for _, arg := range n.Args {
			if arg == nil {
				return "", fmt.Errorf("stuntgres: missing math operand")
			}
		}
		if err := n.Validate(); err != nil {
			return "", err
		}
		if n.Plan.Name() == "pg_size_pretty" {
			text, err := r.sql(ctx, view, n.Args[0])
			if err != nil {
				return "", err
			}
			declaration, err := catalogTypeDeclaration(view, n.Args[0].ResultUsage())
			if err != nil {
				return "", err
			}
			return "pg_catalog.pg_size_pretty(CAST(" + text + " AS " + declaration + "))", nil
		}
		return r.functionSQL(ctx, view, "pg_catalog."+n.Plan.Name(), n.Args)
	case functions.NullCount[BoundExpr, *EvalContext, ExprProperties]:
		if !n.ValidSignature() {
			return "", fmt.Errorf("stuntgres: invalid stored null count signature")
		}
		name := n.Name()
		if name == "" {
			return "", fmt.Errorf("stuntgres: invalid stored null count")
		}
		if n.DotBuiltin {
			if len(n.Args) != 1 {
				return "", fmt.Errorf("stuntgres: invalid stored dot builtin signature")
			}
			base, err := r.sql(ctx, view, n.Args[0])
			if err != nil {
				return "", err
			}
			return "(" + base + ")." + pgIdentifier(name), nil
		}
		return r.functionSQL(ctx, view, "pg_catalog."+name, n.Args)
	case functions.JSONCreate[BoundExpr, *EvalContext, ExprProperties]:
		parts := make([]string, len(n.Args))
		for i, arg := range n.Args {
			if r.commentSnapshot != nil && arg.ResultUsage().Type.ContainsCatalogIdentifier() {
				*r.commentSnapshot = true
			}
			text, err := r.sql(ctx, view, arg)
			if err != nil {
				return "", err
			}
			if literal, ok := arg.(literalExpression); ok && !literal.usage.Type.IsDomain() && r.checkLiterals == nil && !n.UnknownArgument(i) {
				declaration, err := catalogTypeDeclaration(view, literal.usage)
				if err != nil {
					return "", err
				}
				text = "CAST(" + text + " AS " + declaration + ")"
			}
			if n.Variadic && i == len(n.Args)-1 {
				text = "VARIADIC " + text
			}
			parts[i] = text
		}
		return "pg_catalog." + n.Kind.Name() + "(" + strings.Join(parts, ", ") + ")", nil
	case functions.StringVariadic[BoundExpr, *EvalContext, ExprProperties, quoteOrigin]:
		parts := make([]string, len(n.Args))
		for i, arg := range n.Args {
			if r.commentSnapshot != nil && arg.ResultUsage().Type.ContainsCatalogIdentifier() {
				*r.commentSnapshot = true
			}
			text, err := r.sql(ctx, view, arg)
			if err != nil {
				return "", err
			}
			if literal, ok := arg.(literalExpression); ok && !literal.unknownNull && !literal.usage.Type.IsDomain() && r.checkLiterals == nil {
				id := literal.usage.Type.ID()
				if id != sqltype.Text && id != sqltype.Int4 && id != sqltype.Bool && id != sqltype.Regclass && id != sqltype.Regtype && !sqltype.IsOperatorAlias(id) && !sqltype.IsRegTS(id) && id != sqltype.Bit {
					declaration, err := catalogTypeDeclaration(view, literal.usage)
					if err != nil {
						return "", err
					}
					text = "CAST(" + text + " AS " + declaration + ")"
				}
			}
			if n.Variadic && i == len(n.Args)-1 {
				text = "VARIADIC " + text
			}
			parts[i] = text
		}
		return "pg_catalog." + n.Name + "(" + strings.Join(parts, ", ") + ")", nil
	case functions.Format[BoundExpr, *EvalContext, ExprProperties]:
		if err := n.Validate(); err != nil {
			return "", err
		}
		parts := make([]string, len(n.Args))
		for i, arg := range n.Args {
			if r.commentSnapshot != nil && arg.ResultUsage().Type.ContainsCatalogIdentifier() {
				*r.commentSnapshot = true
			}
			text, err := r.sql(ctx, view, arg)
			if err != nil {
				return "", err
			}
			// ANY operands need explicit identity on replay unless their owning
			// literal syntax already preserves it (including canonical B literals).
			if literal, ok := arg.(literalExpression); ok && !literal.unknownNull && !literal.usage.Type.IsDomain() && r.checkLiterals == nil {
				id := literal.usage.Type.ID()
				if id != sqltype.Text && id != sqltype.Int4 && id != sqltype.Bool && id != sqltype.Regclass && id != sqltype.Regtype && !sqltype.IsOperatorAlias(id) && !sqltype.IsRegTS(id) && id != sqltype.Bit || id == sqltype.Text && i > 0 && n.RawUnknown[i/64]&(uint64(1)<<uint(i%64)) == 0 {
					declaration, err := catalogTypeDeclaration(view, literal.usage)
					if err != nil {
						return "", err
					}
					text = "CAST(" + text + " AS " + declaration + ")"
				}
			}
			if n.Variadic && i == len(n.Args)-1 {
				text = "VARIADIC " + text
			}
			parts[i] = text
		}
		return "pg_catalog.format(" + strings.Join(parts, ", ") + ")", nil
	default:
		return "", fmt.Errorf("stuntgres: unsupported stored default node")
	}
}

// Serialize captured scalars through their owning type, without evaluating a
// stored expression or converting a temporal introducer into an unsupported cast.
func literalSQL(ctx context.Context, n literalExpression) (string, error) {
	u := n.ResultUsage()
	if n.value.Type() != u.Type {
		return "", fmt.Errorf("stuntgres: invalid stored literal")
	}
	if n.unknownNull {
		if !n.value.IsNull() || u.Type.ID() != sqltype.Text {
			return "", fmt.Errorf("stuntgres: invalid unknown NULL provenance")
		}
		return "NULL::unknown", nil
	}
	literal, err := u.Type.SQLLiteral(ctx, n.value)
	if err == nil && (sqltype.IsOperatorAlias(u.Type.ID()) || sqltype.IsRegTS(u.Type.ID())) {
		return "CAST(" + literal + " AS " + u.Type.SQLName() + ")", nil
	}
	if err == nil && sqltype.IsTransactionIdentifier(u.Type.ID()) {
		return "CAST(" + literal + " AS " + u.Type.SQLName() + ")", nil
	}
	if err == nil && u.Type.ID() == sqltype.Oid {
		return "CAST(" + literal + " AS OID)", nil
	}
	if err == nil && u.Type.ID() == sqltype.JSON {
		return "CAST(" + literal + " AS JSON)", nil
	}
	if err == nil && u.Type.ID() == sqltype.JSONB {
		return "CAST(" + literal + " AS JSONB)", nil
	}
	if err == nil && u.Type.ID() == sqltype.JSONPath {
		return "CAST(" + literal + " AS JSONPATH)", nil
	}
	if err != nil || n.value.IsNull() {
		return literal, err
	}
	switch u.Type.ID() {
	case sqltype.Interval:
		literalUsage := u
		if u.Typmod >= 0 && (u.Typmod>>16)&sqltype.IntervalSecond == 0 {
			// Non-SECOND precision needs a generic column declaration, not
			// invalid keyword literal syntax. The destination restores that
			// exact typmod; this already-adjusted scalar keeps its field mask.
			literalUsage.Typmod |= sqltype.IntervalFullPrecision
		}
		declaration, err := literalUsage.Declaration()
		if err != nil {
			return "", err
		}
		if strings.HasPrefix(declaration, "INTERVAL(") {
			return declaration + strings.TrimPrefix(literal, "INTERVAL"), nil
		}
		return literal + strings.TrimPrefix(declaration, "INTERVAL"), nil
	case sqltype.Date, sqltype.Time, sqltype.Timestamp, sqltype.Timestamptz, sqltype.Timetz:
		// The endpoint encoding can already carry its required precision.
		if strings.HasPrefix(literal, "TIMESTAMP") {
			return literal, nil
		}
		declaration, err := u.Declaration()
		if err != nil {
			return "", err
		}
		if u.Type.ID() == sqltype.Timetz && strings.HasPrefix(literal, "pg_catalog.timezone(") {
			return "CAST(" + literal + " AS " + declaration + ")", nil
		}
		return declaration + " " + literal, nil
	case sqltype.Numeric:
		text, err := u.Type.Output(ctx, n.value)
		if err != nil {
			return "", err
		}
		if text == "NaN" || text == "Infinity" || text == "-Infinity" {
			return literal, nil
		}
		if !strings.ContainsAny(text, ".eE") {
			// Keep NUMERIC identity without adding a display-scale digit.
			text += "e0"
		}
		return text, nil
	}
	return literal, nil
}
