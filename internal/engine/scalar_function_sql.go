package engine

import (
	"context"
	"fmt"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/sqltype"
)

// The common sql entry validates provenance and records dependencies once.
func (r scalarRenderer) scalarFunctionSQL(ctx context.Context, view catalog.Read, node expr.Expr) (string, error) {
	switch n := node.(type) {
	case plan.SQLRoutineExpr:
		if err := n.ValidateStoredProvider(); err != nil {
			return "", err
		}
		if !storedReferenceEpochValid(view, r.retainedConsumer(), n.Ref) {
			return "", fmt.Errorf("stuntgres: invalid stored SQL routine reference")
		}
		procedure, ok := r.descriptor(view, n.Ref.Address).(*catalog.Procedure)
		if !ok || !routine.SameRoutineDeclaration(procedure, n.Procedure) {
			return "", fmt.Errorf("stuntgres: invalid stored SQL routine declaration")
		}
		namespace, ok := r.descriptor(view, procedure.Namespace()).(*catalog.Namespace)
		if !ok {
			return "", fmt.Errorf("stuntgres: missing stored SQL routine namespace")
		}
		r.record(n.Ref)
		return r.functionSQL(ctx, view, catalog.QuoteIdentifier(namespace.Name())+"."+catalog.QuoteIdentifier(procedure.Name()), n.Args)
	case installedSoundexFunctionExpression:
		if !storedReferenceEpochValid(view, r.retainedConsumer(), n.Ref) || n.Ref.Address.SubID != 0 || r.descriptor(view, n.Ref.Address) != n.Procedure {
			return "", fmt.Errorf("stuntgres: invalid stored Soundex reference")
		}
		if err := validateInstalledSoundex(n, view); err != nil {
			return "", err
		}
		namespace, ok := r.descriptor(view, n.Procedure.Namespace()).(*catalog.Namespace)
		if !ok {
			return "", fmt.Errorf("stuntgres: missing stored Soundex namespace")
		}
		r.record(n.Ref)
		return r.functionSQL(ctx, view, catalog.QuoteIdentifier(namespace.Name())+"."+catalog.QuoteIdentifier(n.Procedure.Name()), n.Args)
	case installedMetaphoneFunctionExpression:
		if !storedReferenceEpochValid(view, r.retainedConsumer(), n.Ref) || n.Ref.Address.SubID != 0 || r.descriptor(view, n.Ref.Address) != n.Procedure {
			return "", fmt.Errorf("stuntgres: invalid stored Metaphone reference")
		}
		if err := validateInstalledMetaphone(n, view); err != nil {
			return "", err
		}
		namespace, ok := r.descriptor(view, n.Procedure.Namespace()).(*catalog.Namespace)
		if !ok {
			return "", fmt.Errorf("stuntgres: missing stored Metaphone namespace")
		}
		r.record(n.Ref)
		return r.functionSQL(ctx, view, catalog.QuoteIdentifier(namespace.Name())+"."+catalog.QuoteIdentifier(n.Procedure.Name()), n.Args)
	case installedLevenshteinFunctionExpression:
		if !storedReferenceEpochValid(view, r.retainedConsumer(), n.Ref) || n.Ref.Address.SubID != 0 || r.descriptor(view, n.Ref.Address) != n.Procedure {
			return "", fmt.Errorf("stuntgres: invalid stored Levenshtein reference")
		}
		if err := validateInstalledLevenshtein(n, view); err != nil {
			return "", err
		}
		namespace, ok := r.descriptor(view, n.Procedure.Namespace()).(*catalog.Namespace)
		if !ok {
			return "", fmt.Errorf("stuntgres: missing stored Levenshtein namespace")
		}
		r.record(n.Ref)
		return r.functionSQL(ctx, view, catalog.QuoteIdentifier(namespace.Name())+"."+catalog.QuoteIdentifier(n.Procedure.Name()), n.Args)
	case installedUUIDOsspFunctionExpression:
		if !storedReferenceEpochValid(view, r.retainedConsumer(), n.Ref) || n.Ref.Address.SubID != 0 || r.descriptor(view, n.Ref.Address) != n.Procedure {
			return "", fmt.Errorf("stuntgres: invalid stored uuid-ossp reference")
		}
		if err := validateInstalledUUIDOssp(n, view); err != nil {
			return "", err
		}
		namespace, ok := r.descriptor(view, n.Procedure.Namespace()).(*catalog.Namespace)
		if !ok {
			return "", fmt.Errorf("stuntgres: missing stored uuid-ossp namespace")
		}
		r.record(n.Ref)
		return r.functionSQL(ctx, view, catalog.QuoteIdentifier(namespace.Name())+"."+catalog.QuoteIdentifier(n.Procedure.Name()), nil)
	case installedCryptoFunctionExpression:
		if !storedReferenceEpochValid(view, r.retainedConsumer(), n.Ref) || n.Ref.Address.SubID != 0 || r.descriptor(view, n.Ref.Address) != n.Procedure {
			return "", fmt.Errorf("stuntgres: invalid stored crypto reference")
		}
		if err := validateInstalledCrypto(n, view); err != nil {
			return "", err
		}
		namespace, ok := r.descriptor(view, n.Procedure.Namespace()).(*catalog.Namespace)
		if !ok {
			return "", fmt.Errorf("stuntgres: missing stored crypto namespace")
		}
		r.record(n.Ref)
		return r.functionSQL(ctx, view, catalog.QuoteIdentifier(namespace.Name())+"."+catalog.QuoteIdentifier(n.Procedure.Name()), n.Args)
	case installedFunctionExpression:
		proc, ok := r.descriptor(view, n.Ref.Address).(*catalog.Procedure)
		if !ok || !storedReferenceEpochValid(view, r.retainedConsumer(), n.Ref) || n.Ref.Address.SubID != 0 || !validInstalledText(n) || proc.Name() != n.Procedure.Name() || proc.Signature != n.Procedure.Signature || proc.RoutineKind != n.Procedure.RoutineKind || proc.Result != n.Procedure.Result || proc.Strict != n.Procedure.Strict || proc.Volatility != n.Procedure.Volatility || extension.UnaccentProviderOf(proc) != extension.UnaccentProviderOf(n.Procedure) || extension.IntarrayProviderOf(proc) != extension.IntarrayProviderOf(n.Procedure) || extension.PgcryptoProviderOf(proc) != extension.PgcryptoProviderOf(n.Procedure) || extension.FuzzystrmatchProviderOf(proc) != extension.FuzzystrmatchProviderOf(n.Procedure) || proc.Dictionary != n.Procedure.Dictionary || proc.Extension != n.Procedure.Extension || proc.Namespace() != n.Procedure.Namespace() {
			return "", fmt.Errorf("stuntgres: invalid stored installed function")
		}
		if n.Dictionary != nil {
			dictionary, ok := r.descriptor(view, proc.Dictionary).(*catalog.TSDictionary)
			if !ok || dictionary.Address() != n.Dictionary.Address() || dictionary.Template != n.Dictionary.Template || extension.UnaccentDictionaryProviderOf(dictionary) != extension.UnaccentDictionaryProviderOf(n.Dictionary) {
				return "", fmt.Errorf("stuntgres: invalid stored installed dictionary")
			}
			template, ok := r.descriptor(view, dictionary.Template).(*catalog.TSTemplate)
			if !ok || template.Address() != n.Template.Address() || extension.UnaccentTemplateProviderOf(template) != extension.UnaccentTemplateProviderOf(n.Template) {
				return "", fmt.Errorf("stuntgres: invalid stored installed template")
			}
		}
		if extension.FuzzystrmatchProviderOf(proc) == nil && n.Dictionary == nil {
			return "", fmt.Errorf("stuntgres: invalid stored installed function")
		}
		namespace, ok := r.descriptor(view, proc.Namespace()).(*catalog.Namespace)
		if !ok {
			return "", fmt.Errorf("stuntgres: missing stored function namespace")
		}
		r.record(n.Ref)
		return r.functionSQL(ctx, view, catalog.QuoteIdentifier(namespace.Name())+"."+catalog.QuoteIdentifier(proc.Name()), []expr.Expr{n.Arg})
	case extensionPathsExpression:
		if r.commentSnapshot != nil {
			*r.commentSnapshot = true
		}
		if err := n.Validate(); err != nil {
			return "", err
		}
		args := [1]expr.Expr{n.Arg}
		return r.functionSQL(ctx, view, "pg_catalog.pg_extension_update_paths", args[:])
	case expr.Renderable:
		// Function nodes describe their stored form; engine wrappers that embed
		// one keep their explicit cases above.
		spec, err := n.Render()
		if err != nil {
			return "", err
		}
		return r.rendering(ctx, view, spec)
	default:
		return "", fmt.Errorf("stuntgres: unsupported stored default node")
	}
}

// Serialize captured scalars through their owning type, without evaluating a
// stored expression or converting a temporal introducer into an unsupported cast.
func literalSQL(ctx context.Context, n plan.LiteralExpr) (string, error) {
	u := n.ResultUsage()
	if n.Value.Type() != u.Type {
		return "", fmt.Errorf("stuntgres: invalid stored literal")
	}
	if n.UnknownNull {
		if !n.Value.IsNull() || u.Type.ID() != sqltype.Text {
			return "", fmt.Errorf("stuntgres: invalid unknown NULL provenance")
		}
		return "NULL::unknown", nil
	}
	literal, err := u.Type.SQLLiteral(ctx, n.Value)
	if err == nil && sqltype.IsFTS(u.Type.ID()) {
		return "CAST(" + literal + " AS " + u.Type.SQLName() + ")", nil
	}
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
	if err != nil || n.Value.IsNull() {
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
		text, err := u.Type.Output(ctx, n.Value)
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
