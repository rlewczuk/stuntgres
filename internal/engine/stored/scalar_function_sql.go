package stored

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
)

// The common sql entry validates provenance and records dependencies once.
func (r ScalarRenderer) ScalarFunctionSQL(ctx context.Context, view catalog.Read, node expr.Expr) (string, error) {
	switch n := node.(type) {
	case plan.SQLRoutineExpr:
		if err := n.ValidateStoredProvider(); err != nil {
			return "", err
		}
		if !ReferenceEpochValid(view, r.retainedConsumer(), n.Ref) {
			return "", fmt.Errorf("stuntgres: invalid stored SQL routine reference")
		}
		procedure, ok := r.descriptor(view, n.Ref.Address).(*catalog.Procedure)
		if !ok || !exec.SameRoutineDeclaration(procedure, n.Procedure) {
			return "", fmt.Errorf("stuntgres: invalid stored SQL routine declaration")
		}
		namespace, ok := r.descriptor(view, procedure.Namespace()).(*catalog.Namespace)
		if !ok {
			return "", fmt.Errorf("stuntgres: missing stored SQL routine namespace")
		}
		r.record(n.Ref)
		return routineCallSQL(n, catalog.QuoteIdentifier(namespace.Name())+"."+catalog.QuoteIdentifier(procedure.Name()), catalog.QuoteIdentifier, func(arg expr.Expr) (string, error) { return r.SQL(ctx, view, arg) })
	case plan.InstalledSoundexExpr:
		if !ReferenceEpochValid(view, r.retainedConsumer(), n.Ref) || n.Ref.Address.SubID != 0 || r.descriptor(view, n.Ref.Address) != n.Procedure {
			return "", fmt.Errorf("stuntgres: invalid stored Soundex reference")
		}
		if err := ValidateInstalledSoundex(n, view); err != nil {
			return "", err
		}
		namespace, ok := r.descriptor(view, n.Procedure.Namespace()).(*catalog.Namespace)
		if !ok {
			return "", fmt.Errorf("stuntgres: missing stored Soundex namespace")
		}
		r.record(n.Ref)
		return r.functionSQL(ctx, view, catalog.QuoteIdentifier(namespace.Name())+"."+catalog.QuoteIdentifier(n.Procedure.Name()), n.Args)
	case plan.InstalledMetaphoneExpr:
		if !ReferenceEpochValid(view, r.retainedConsumer(), n.Ref) || n.Ref.Address.SubID != 0 || r.descriptor(view, n.Ref.Address) != n.Procedure {
			return "", fmt.Errorf("stuntgres: invalid stored Metaphone reference")
		}
		if err := ValidateInstalledMetaphone(n, view); err != nil {
			return "", err
		}
		namespace, ok := r.descriptor(view, n.Procedure.Namespace()).(*catalog.Namespace)
		if !ok {
			return "", fmt.Errorf("stuntgres: missing stored Metaphone namespace")
		}
		r.record(n.Ref)
		return r.functionSQL(ctx, view, catalog.QuoteIdentifier(namespace.Name())+"."+catalog.QuoteIdentifier(n.Procedure.Name()), n.Args)
	case plan.InstalledLevenshteinExpr:
		if !ReferenceEpochValid(view, r.retainedConsumer(), n.Ref) || n.Ref.Address.SubID != 0 || r.descriptor(view, n.Ref.Address) != n.Procedure {
			return "", fmt.Errorf("stuntgres: invalid stored Levenshtein reference")
		}
		if err := ValidateInstalledLevenshtein(n, view); err != nil {
			return "", err
		}
		namespace, ok := r.descriptor(view, n.Procedure.Namespace()).(*catalog.Namespace)
		if !ok {
			return "", fmt.Errorf("stuntgres: missing stored Levenshtein namespace")
		}
		r.record(n.Ref)
		return r.functionSQL(ctx, view, catalog.QuoteIdentifier(namespace.Name())+"."+catalog.QuoteIdentifier(n.Procedure.Name()), n.Args)
	case plan.InstalledUUIDOsspExpr:
		if !ReferenceEpochValid(view, r.retainedConsumer(), n.Ref) || n.Ref.Address.SubID != 0 || r.descriptor(view, n.Ref.Address) != n.Procedure {
			return "", fmt.Errorf("stuntgres: invalid stored uuid-ossp reference")
		}
		if err := ValidateInstalledUUIDOssp(n, view); err != nil {
			return "", err
		}
		namespace, ok := r.descriptor(view, n.Procedure.Namespace()).(*catalog.Namespace)
		if !ok {
			return "", fmt.Errorf("stuntgres: missing stored uuid-ossp namespace")
		}
		r.record(n.Ref)
		return r.functionSQL(ctx, view, catalog.QuoteIdentifier(namespace.Name())+"."+catalog.QuoteIdentifier(n.Procedure.Name()), n.Args)
	case plan.InstalledCryptoExpr:
		if !ReferenceEpochValid(view, r.retainedConsumer(), n.Ref) || n.Ref.Address.SubID != 0 || r.descriptor(view, n.Ref.Address) != n.Procedure {
			return "", fmt.Errorf("stuntgres: invalid stored crypto reference")
		}
		if err := ValidateInstalledCrypto(n, view); err != nil {
			return "", err
		}
		namespace, ok := r.descriptor(view, n.Procedure.Namespace()).(*catalog.Namespace)
		if !ok {
			return "", fmt.Errorf("stuntgres: missing stored crypto namespace")
		}
		r.record(n.Ref)
		return r.functionSQL(ctx, view, catalog.QuoteIdentifier(namespace.Name())+"."+catalog.QuoteIdentifier(n.Procedure.Name()), n.Args)
	case plan.InstalledTextExpr:
		proc, ok := r.descriptor(view, n.Ref.Address).(*catalog.Procedure)
		if !ok || !ReferenceEpochValid(view, r.retainedConsumer(), n.Ref) || n.Ref.Address.SubID != 0 || !analyze.ValidInstalledText(n) || proc.Name() != n.Procedure.Name() || proc.Signature != n.Procedure.Signature || proc.RoutineKind != n.Procedure.RoutineKind || proc.Result != n.Procedure.Result || proc.Strict != n.Procedure.Strict || proc.Volatility != n.Procedure.Volatility || extension.UnaccentProviderOf(proc) != extension.UnaccentProviderOf(n.Procedure) || extension.IntarrayProviderOf(proc) != extension.IntarrayProviderOf(n.Procedure) || extension.PgcryptoProviderOf(proc) != extension.PgcryptoProviderOf(n.Procedure) || extension.FuzzystrmatchProviderOf(proc) != extension.FuzzystrmatchProviderOf(n.Procedure) || proc.Dictionary != n.Procedure.Dictionary || proc.Extension != n.Procedure.Extension || proc.Namespace() != n.Procedure.Namespace() {
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
	case plan.InstalledPgrowlocksExpr:
		if err := ValidateInstalledPgrowlocks(n, view); err != nil {
			return "", err
		}
		namespace, ok := r.descriptor(view, n.Procedure.Namespace()).(*catalog.Namespace)
		if !ok {
			return "", fmt.Errorf("stuntgres: missing stored pgrowlocks namespace")
		}
		r.record(n.Ref)
		return r.functionSQL(ctx, view, catalog.QuoteIdentifier(namespace.Name())+"."+catalog.QuoteIdentifier(n.Procedure.Name()), []expr.Expr{n.Arg})
	case plan.ExtensionPathsExpr:
		if r.CommentSnapshot != nil {
			*r.CommentSnapshot = true
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
