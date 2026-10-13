package engine

import (
	"context"
	"fmt"
	"strings"
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
	case uuidGenerationExpression:
		if u.Type.ID() != sqltype.UUID || (n.name != "uuidv7" && n.name != "uuidv4" && n.name != "gen_random_uuid") || len(n.args) > 1 || len(n.args) != 0 && n.name != "uuidv7" {
			return "", fmt.Errorf("stuntgres: invalid stored UUID generation signature")
		}
		if len(n.args) != 0 && n.args[0].ResultUsage().Type.ID() != sqltype.Interval {
			return "", fmt.Errorf("stuntgres: invalid stored UUID shift")
		}
		r.uuidShiftIntervalNulls = len(n.args) != 0
		return r.functionSQL(ctx, view, "pg_catalog."+n.name, n.args)
	case randomExpression:
		if r.owner == nil {
			return "", fmt.Errorf("stuntgres: volatile stored default")
		}
		argumentType := sqltype.Float8
		valid := false
		switch n.name {
		case "random":
			if len(n.args) == 0 {
				valid = u.Type.ID() == sqltype.Float8
			}
			if len(n.args) == 2 {
				argumentType = u.Type.ID()
				valid = argumentType == sqltype.Int4 || argumentType == sqltype.Int8 || argumentType == sqltype.Numeric
			}
		case "random_normal":
			valid = len(n.args) <= 2 && u.Type.ID() == sqltype.Float8
		case "setseed":
			valid = len(n.args) == 1 && u.Type.ID() == sqltype.Void
		}
		if !valid {
			return "", fmt.Errorf("stuntgres: invalid stored random signature")
		}
		for _, arg := range n.args {
			if arg == nil || arg.ResultUsage().Type == nil || arg.ResultUsage().Type.ID() != argumentType {
				return "", fmt.Errorf("stuntgres: invalid stored random argument")
			}
		}
		return r.functionSQL(ctx, view, "pg_catalog."+n.name, n.args)
	case binaryExpression:
		if len(n.args) < 1 || len(n.args) > 4 {
			return "", fmt.Errorf("stuntgres: invalid stored binary arity")
		}
		if err := n.validateSyntax(); err != nil {
			return "", err
		}
		var usages [4]sqltype.Usage
		for i, arg := range n.args {
			if arg == nil {
				return "", fmt.Errorf("stuntgres: missing binary operand")
			}
			usages[i] = arg.ResultUsage()
		}
		plan, err := sqltype.BindBinary(n.plan.Name(), u, usages[:len(n.args)]...)
		if err != nil || plan != n.plan {
			return "", fmt.Errorf("stuntgres: invalid stored binary provider")
		}
		if n.plan.Name() != "||" {
			return renderBinaryCall(n.plan.Name(), n.syntax, n.args, "pg_catalog."+n.plan.Name(), func(arg BoundExpr) (string, error) { return r.sql(ctx, view, arg) })
		}
		left, err := r.sql(ctx, view, n.args[0])
		if err != nil {
			return "", err
		}
		right, err := r.sql(ctx, view, n.args[1])
		if err != nil {
			return "", err
		}
		return "(" + left + " OPERATOR(pg_catalog.||) " + right + ")", nil
	case datetimeJustifyExpression:
		return r.functionSQL(ctx, view, "pg_catalog."+n.plan.Name(), n.children())
	case datetimeExtractExpression:
		return n.logicalSQL("pg_catalog."+n.plan.Name(), func(arg BoundExpr) (string, error) { return r.sql(ctx, view, arg) })
	case datetimeOverlapsExpression:
		return n.logicalSQL(func(arg BoundExpr) (string, error) { return r.sql(ctx, view, arg) })
	case datetimeBinExpression:
		return r.functionSQL(ctx, view, "pg_catalog.date_bin", n.children())
	case datetimeTruncExpression:
		return r.functionSQL(ctx, view, "pg_catalog.date_trunc", n.children())
	case datetimeAgeFiniteExpression:
		return r.functionSQL(ctx, view, "pg_catalog."+n.plan.Name(), n.children())
	case datetimeMakeExpression:
		return n.sql("pg_catalog."+n.plan.Name(), func(arg BoundExpr) (string, error) { return r.sql(ctx, view, arg) }, quoteIdentifier)
	case datetimeFormattingExpression:
		return r.functionSQL(ctx, view, "pg_catalog."+n.plan.Name(), n.args[:])
	case numericFormattingExpression:
		return r.functionSQL(ctx, view, "pg_catalog."+n.plan.Name(), n.args[:])
	case stringExpression:
		if len(n.args) < 1 || len(n.args) > 4 {
			return "", fmt.Errorf("stuntgres: invalid stored string arity")
		}
		var usages [4]sqltype.Usage
		for i, arg := range n.args {
			if arg == nil {
				return "", fmt.Errorf("stuntgres: missing string operand")
			}
			usages[i] = arg.ResultUsage()
		}
		plan, err := sqltype.BindString(n.plan.Name(), u, n.plan.TextUsage(), usages[:len(n.args)], n.textual, n.plan.CollationIndeterminate())
		if err != nil || plan != n.plan {
			return "", fmt.Errorf("stuntgres: invalid stored string provider")
		}
		if r.commentSnapshot != nil {
			for i, arg := range n.args {
				if !n.textual[i] && arg.ResultUsage().Type.ContainsCatalogIdentifier() {
					*r.commentSnapshot = true
				}
			}
		}
		if n.plan.Name() == "||" {
			left, err := r.stringArgumentSQL(ctx, view, n.args[0])
			if err != nil {
				return "", err
			}
			right, err := r.stringArgumentSQL(ctx, view, n.args[1])
			if err != nil {
				return "", err
			}
			return "(" + left + " OPERATOR(pg_catalog.||) " + right + ")", nil
		}
		parts := make([]string, len(n.args))
		for i, arg := range n.args {
			text, err := r.stringArgumentSQL(ctx, view, arg)
			if err != nil {
				return "", err
			}
			if sqltype.StringBaseConversion(n.plan.Name()) {
				// Replay must select the stored integer overload, including
				// small BIGINT values and typed NULLs.
				declaration, err := catalogTypeDeclaration(view, n.plan.ArgumentUsage(i))
				if err != nil {
					return "", err
				}
				text = "CAST(" + text + " AS " + declaration + ")"
			}
			parts[i] = text
		}
		if n.syntax != parser.OrdinaryStringSyntax {
			return n.specialSQL(parts)
		}
		return "pg_catalog." + n.plan.Name() + "(" + strings.Join(parts, ", ") + ")", nil
	case arraySearchExpression:
		if len(n.args) < 2 || len(n.args) > 3 {
			return "", fmt.Errorf("stuntgres: invalid stored array search arity")
		}
		ref, conflict := n.plan.InputCollation()
		plan, err := sqltype.BindArraySearch(n.plan.Name(), n.args[0].ResultUsage().Type, u.Type, n.plan.Integer(), ref, conflict)
		if err != nil || plan != n.plan {
			return "", fmt.Errorf("stuntgres: invalid stored array search provider")
		}
		return n.sql(func(child BoundExpr) (string, error) { return r.sql(ctx, view, child) }, func(namespace, name string) string {
			if namespace != "" {
				return "OPERATOR(" + quoteIdentifier(namespace) + "." + name + ")"
			}
			return name
		})
	case enumFunctionExpression:
		if len(n.args) < 1 || len(n.args) > 2 {
			return "", fmt.Errorf("stuntgres: invalid stored enum arity")
		}
		var usages [2]sqltype.Usage
		for i, arg := range n.args {
			if arg == nil {
				return "", fmt.Errorf("stuntgres: missing enum operand")
			}
			usages[i] = arg.ResultUsage()
		}
		plan, err := sqltype.BindEnumSupport(n.plan.Name(), u, usages[:len(n.args)]...)
		if err != nil || plan != n.plan {
			return "", fmt.Errorf("stuntgres: invalid stored enum provider")
		}
		return r.functionSQL(ctx, view, "pg_catalog."+n.plan.Name(), n.args)
	case mathExpression:
		if len(n.args) > 2 {
			return "", fmt.Errorf("stuntgres: invalid stored math arity")
		}
		var usages [2]sqltype.Usage
		for i, arg := range n.args {
			if arg == nil {
				return "", fmt.Errorf("stuntgres: missing math operand")
			}
			usages[i] = arg.ResultUsage()
		}
		plan, err := sqltype.BindMath(n.plan.Name(), u, usages[:len(n.args)]...)
		if err != nil || plan != n.plan {
			return "", fmt.Errorf("stuntgres: invalid stored math provider")
		}
		if n.plan.Name() == "pg_size_pretty" {
			text, err := r.sql(ctx, view, n.args[0])
			if err != nil {
				return "", err
			}
			declaration, err := catalogTypeDeclaration(view, usages[0])
			if err != nil {
				return "", err
			}
			return "pg_catalog.pg_size_pretty(CAST(" + text + " AS " + declaration + "))", nil
		}
		return r.functionSQL(ctx, view, "pg_catalog."+n.plan.Name(), n.args)
	case nullCountExpression:
		if len(n.args) == 0 || len(n.args) > 100 || u.Type.ID() != sqltype.Int4 {
			return "", fmt.Errorf("stuntgres: invalid stored null count signature")
		}
		name := "num_nulls"
		if n.kind == countNonnulls {
			name = "num_nonnulls"
		} else if n.kind != countNulls {
			return "", fmt.Errorf("stuntgres: invalid stored null count")
		}
		if n.dotBuiltin {
			if len(n.args) != 1 {
				return "", fmt.Errorf("stuntgres: invalid stored dot builtin signature")
			}
			base, err := r.sql(ctx, view, n.args[0])
			if err != nil {
				return "", err
			}
			return "(" + base + ")." + pgIdentifier(name), nil
		}
		return r.functionSQL(ctx, view, "pg_catalog."+name, n.args)
	case jsonCreateExpression:
		parts := make([]string, len(n.args))
		for i, arg := range n.args {
			if r.commentSnapshot != nil && arg.ResultUsage().Type.ContainsCatalogIdentifier() {
				*r.commentSnapshot = true
			}
			text, err := r.sql(ctx, view, arg)
			if err != nil {
				return "", err
			}
			if literal, ok := arg.(literalExpression); ok && !literal.usage.Type.IsDomain() && r.checkLiterals == nil && !n.unknownArgument(i) {
				declaration, err := catalogTypeDeclaration(view, literal.usage)
				if err != nil {
					return "", err
				}
				text = "CAST(" + text + " AS " + declaration + ")"
			}
			if n.variadic && i == len(n.args)-1 {
				text = "VARIADIC " + text
			}
			parts[i] = text
		}
		return "pg_catalog." + n.kind.Name() + "(" + strings.Join(parts, ", ") + ")", nil
	case stringVariadicExpression:
		parts := make([]string, len(n.args))
		for i, arg := range n.args {
			if r.commentSnapshot != nil && arg.ResultUsage().Type.ContainsCatalogIdentifier() {
				*r.commentSnapshot = true
			}
			text, err := r.sql(ctx, view, arg)
			if err != nil {
				return "", err
			}
			if literal, ok := arg.(literalExpression); ok && !literal.unknownNull && !literal.usage.Type.IsDomain() && r.checkLiterals == nil {
				id := literal.usage.Type.ID()
				if id != sqltype.Text && id != sqltype.Int4 && id != sqltype.Bool && id != sqltype.Regclass && id != sqltype.Regtype && id != sqltype.Bit {
					declaration, err := catalogTypeDeclaration(view, literal.usage)
					if err != nil {
						return "", err
					}
					text = "CAST(" + text + " AS " + declaration + ")"
				}
			}
			if n.variadic && i == len(n.args)-1 {
				text = "VARIADIC " + text
			}
			parts[i] = text
		}
		return "pg_catalog." + n.name + "(" + strings.Join(parts, ", ") + ")", nil
	case formatExpression:
		if len(n.args) < 1 || len(n.args) > 100 || u.Type.ID() != sqltype.Text || n.args[0].ResultUsage().Type.ID() != sqltype.Text || n.int4 == nil || n.int4.ID() != sqltype.Int4 || n.variadic && (len(n.args) != 2 || !n.args[1].ResultUsage().Type.BaseType().IsArray()) {
			return "", fmt.Errorf("stuntgres: invalid stored format signature")
		}
		parts := make([]string, len(n.args))
		for i, arg := range n.args {
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
				if id != sqltype.Text && id != sqltype.Int4 && id != sqltype.Bool && id != sqltype.Regclass && id != sqltype.Regtype && id != sqltype.Bit || id == sqltype.Text && i > 0 && n.rawUnknown[i/64]&(uint64(1)<<uint(i%64)) == 0 {
					declaration, err := catalogTypeDeclaration(view, literal.usage)
					if err != nil {
						return "", err
					}
					text = "CAST(" + text + " AS " + declaration + ")"
				}
			}
			if n.variadic && i == len(n.args)-1 {
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
