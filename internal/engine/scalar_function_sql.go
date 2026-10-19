package engine

import (
	"context"
	"fmt"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqltype"
)

// The common sql entry validates provenance and records dependencies once.
func (r scalarRenderer) scalarFunctionSQL(ctx context.Context, view catalog.Read, node expr.Expr) (string, error) {
	switch n := node.(type) {
	case installedSoundexFunctionExpression:
		if !storedReferenceEpochValid(view, r.retainedConsumer(), n.ref) || n.ref.Address.SubID != 0 || r.descriptor(view, n.ref.Address) != n.procedure {
			return "", fmt.Errorf("stuntgres: invalid stored Soundex reference")
		}
		if err := n.validate(view); err != nil {
			return "", err
		}
		namespace, ok := r.descriptor(view, n.procedure.Namespace()).(*catalog.Namespace)
		if !ok {
			return "", fmt.Errorf("stuntgres: missing stored Soundex namespace")
		}
		r.record(n.ref)
		return r.functionSQL(ctx, view, catalog.QuoteIdentifier(namespace.Name())+"."+catalog.QuoteIdentifier(n.procedure.Name()), n.Args)
	case installedMetaphoneFunctionExpression:
		if !storedReferenceEpochValid(view, r.retainedConsumer(), n.ref) || n.ref.Address.SubID != 0 || r.descriptor(view, n.ref.Address) != n.procedure {
			return "", fmt.Errorf("stuntgres: invalid stored Metaphone reference")
		}
		if err := n.validate(view); err != nil {
			return "", err
		}
		namespace, ok := r.descriptor(view, n.procedure.Namespace()).(*catalog.Namespace)
		if !ok {
			return "", fmt.Errorf("stuntgres: missing stored Metaphone namespace")
		}
		r.record(n.ref)
		return r.functionSQL(ctx, view, catalog.QuoteIdentifier(namespace.Name())+"."+catalog.QuoteIdentifier(n.procedure.Name()), n.Args)
	case installedLevenshteinFunctionExpression:
		if !storedReferenceEpochValid(view, r.retainedConsumer(), n.ref) || n.ref.Address.SubID != 0 || r.descriptor(view, n.ref.Address) != n.procedure {
			return "", fmt.Errorf("stuntgres: invalid stored Levenshtein reference")
		}
		if err := n.validate(view); err != nil {
			return "", err
		}
		namespace, ok := r.descriptor(view, n.procedure.Namespace()).(*catalog.Namespace)
		if !ok {
			return "", fmt.Errorf("stuntgres: missing stored Levenshtein namespace")
		}
		r.record(n.ref)
		return r.functionSQL(ctx, view, catalog.QuoteIdentifier(namespace.Name())+"."+catalog.QuoteIdentifier(n.procedure.Name()), n.Args)
	case functions.SQLJSON:
		return r.sqlJSON(ctx, view, n, sqltype.Usage{})
	case installedUUIDOsspFunctionExpression:
		if !storedReferenceEpochValid(view, r.retainedConsumer(), n.ref) || n.ref.Address.SubID != 0 || r.descriptor(view, n.ref.Address) != n.procedure {
			return "", fmt.Errorf("stuntgres: invalid stored uuid-ossp reference")
		}
		if err := n.validate(view); err != nil {
			return "", err
		}
		namespace, ok := r.descriptor(view, n.procedure.Namespace()).(*catalog.Namespace)
		if !ok {
			return "", fmt.Errorf("stuntgres: missing stored uuid-ossp namespace")
		}
		r.record(n.ref)
		return r.functionSQL(ctx, view, catalog.QuoteIdentifier(namespace.Name())+"."+catalog.QuoteIdentifier(n.procedure.Name()), nil)
	case installedCryptoFunctionExpression:
		if !storedReferenceEpochValid(view, r.retainedConsumer(), n.ref) || n.ref.Address.SubID != 0 || r.descriptor(view, n.ref.Address) != n.procedure {
			return "", fmt.Errorf("stuntgres: invalid stored crypto reference")
		}
		if err := n.validate(view); err != nil {
			return "", err
		}
		namespace, ok := r.descriptor(view, n.procedure.Namespace()).(*catalog.Namespace)
		if !ok {
			return "", fmt.Errorf("stuntgres: missing stored crypto namespace")
		}
		r.record(n.ref)
		return r.functionSQL(ctx, view, catalog.QuoteIdentifier(namespace.Name())+"."+catalog.QuoteIdentifier(n.procedure.Name()), n.Args)
	case installedFunctionExpression:
		proc, ok := r.descriptor(view, n.ref.Address).(*catalog.Procedure)
		if !ok || !storedReferenceEpochValid(view, r.retainedConsumer(), n.ref) || n.ref.Address.SubID != 0 || !n.validProvider() || proc.Name() != n.procedure.Name() || proc.Signature != n.procedure.Signature || proc.RoutineKind != n.procedure.RoutineKind || proc.Result != n.procedure.Result || proc.Strict != n.procedure.Strict || proc.Volatility != n.procedure.Volatility || unaccentProviderOf(proc) != unaccentProviderOf(n.procedure) || intarrayProviderOf(proc) != intarrayProviderOf(n.procedure) || pgcryptoProviderOf(proc) != pgcryptoProviderOf(n.procedure) || fuzzystrmatchProviderOf(proc) != fuzzystrmatchProviderOf(n.procedure) || proc.Dictionary != n.procedure.Dictionary || proc.Extension != n.procedure.Extension || proc.Namespace() != n.procedure.Namespace() {
			return "", fmt.Errorf("stuntgres: invalid stored installed function")
		}
		if n.dictionary != nil {
			dictionary, ok := r.descriptor(view, proc.Dictionary).(*catalog.TSDictionary)
			if !ok || dictionary.Address() != n.dictionary.Address() || dictionary.Template != n.dictionary.Template || unaccentDictionaryProviderOf(dictionary) != unaccentDictionaryProviderOf(n.dictionary) {
				return "", fmt.Errorf("stuntgres: invalid stored installed dictionary")
			}
			template, ok := r.descriptor(view, dictionary.Template).(*catalog.TSTemplate)
			if !ok || template.Address() != n.template.Address() || unaccentTemplateProviderOf(template) != unaccentTemplateProviderOf(n.template) {
				return "", fmt.Errorf("stuntgres: invalid stored installed template")
			}
		}
		if fuzzystrmatchProviderOf(proc) == nil && n.dictionary == nil {
			return "", fmt.Errorf("stuntgres: invalid stored installed function")
		}
		namespace, ok := r.descriptor(view, proc.Namespace()).(*catalog.Namespace)
		if !ok {
			return "", fmt.Errorf("stuntgres: missing stored function namespace")
		}
		r.record(n.ref)
		return r.functionSQL(ctx, view, catalog.QuoteIdentifier(namespace.Name())+"."+catalog.QuoteIdentifier(proc.Name()), []expr.Expr{n.Arg})
	case catalogUserNameExpression:
		if r.commentSnapshot != nil {
			*r.commentSnapshot = true
		}
		if err := n.validate(); err != nil {
			return "", err
		}
		args := [1]expr.Expr{n.arg}
		return r.functionSQL(ctx, view, "pg_catalog.pg_get_userbyid", args[:])
	case catalogForeignKeysExpression:
		if r.commentSnapshot != nil {
			*r.commentSnapshot = true
		}
		if err := n.validate(); err != nil {
			return "", err
		}
		return r.functionSQL(ctx, view, "pg_catalog.pg_get_catalog_foreign_keys", nil)
	case transactionIdentityExpression:
		if err := n.validate(); err != nil {
			return "", err
		}
		if n.arg == nil {
			return r.functionSQL(ctx, view, "pg_catalog."+n.kind.name(), nil)
		}
		args := [1]expr.Expr{n.arg}
		return r.functionSQL(ctx, view, "pg_catalog."+n.kind.name(), args[:])
	case commitTimestampExpression:
		if err := n.validate(); err != nil {
			return "", err
		}
		if n.arg == nil {
			return r.functionSQL(ctx, view, "pg_catalog."+n.kind.name(), nil)
		}
		args := [1]expr.Expr{n.arg}
		return r.functionSQL(ctx, view, "pg_catalog."+n.kind.name(), args[:])
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
func literalSQL(ctx context.Context, n literalExpression) (string, error) {
	u := n.ResultUsage()
	if n.value.Type() != u.Type {
		return "", fmt.Errorf("stuntgres: invalid stored literal")
	}
	if n.UnknownNull {
		if !n.value.IsNull() || u.Type.ID() != sqltype.Text {
			return "", fmt.Errorf("stuntgres: invalid unknown NULL provenance")
		}
		return "NULL::unknown", nil
	}
	literal, err := u.Type.SQLLiteral(ctx, n.value)
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
