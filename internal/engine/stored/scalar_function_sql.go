package stored

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
)

// The common sql entry validates provenance and records dependencies once.
func (r ScalarRenderer) ScalarFunctionSQL(ctx context.Context, view catalog.Read, node expr.Expr) (string, error) {
	switch n := node.(type) {
	case plan.InstalledAmcheckExpr:
		if !ReferenceEpochValid(view, r.retainedConsumer(), n.Ref) || !r.sameRoutineDescriptor(view, n.Ref.Address, n.Procedure) {
			return "", fmt.Errorf("stuntgres: invalid stored amcheck reference")
		}
		if err := ValidateInstalledAmcheck(n, view); err != nil {
			return "", err
		}
		procedure, _ := view.Procedure(n.Ref.Address)
		namespace, ok := view.Namespace(procedure.Namespace())
		if !ok {
			return "", fmt.Errorf("stuntgres: missing amcheck namespace")
		}
		r.record(n.Ref)
		return amcheckCallSQL(n, procedure, catalog.QuoteIdentifier(namespace.Name())+"."+catalog.QuoteIdentifier(procedure.Name()), catalog.QuoteIdentifier, func(arg expr.Expr) (string, error) { return r.SQL(ctx, view, arg) })
	case plan.RangeConstructorExpr:
		if err := n.ValidateStoredProvider(); err != nil {
			return "", err
		}
		if !ReferenceEpochValid(view, r.retainedConsumer(), n.Ref) {
			return "", fmt.Errorf("invalid stored constructor reference")
		}
		p, ok := r.descriptor(view, n.Ref.Address).(*catalog.Procedure)
		if !ok || !exec.SameRoutineDeclaration(p, n.Procedure) {
			return "", fmt.Errorf("invalid stored constructor declaration")
		}
		ns, ok := r.descriptor(view, p.Namespace()).(*catalog.Namespace)
		if !ok {
			return "", fmt.Errorf("missing constructor namespace")
		}
		r.record(n.Ref)
		return rangeConstructorCallSQL(n, catalog.QuoteIdentifier(ns.Name())+"."+catalog.QuoteIdentifier(p.Name()), func(arg expr.Expr) (string, error) { return r.SQL(ctx, view, arg) })
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
	case plan.InstalledEarthExpr:
		if !ReferenceEpochValid(view, r.retainedConsumer(), n.Ref) || n.Ref.Address.SubID != 0 || !r.sameRoutineDescriptor(view, n.Ref.Address, n.Procedure) {
			return "", fmt.Errorf("invalid stored earth reference")
		}
		if err := ValidateInstalledEarth(n, view); err != nil {
			return "", err
		}
		procedure, _ := view.Procedure(n.Procedure.Address())
		namespace, ok := view.Namespace(procedure.Namespace())
		if !ok {
			return "", fmt.Errorf("missing earth function namespace")
		}
		r.record(n.Ref)
		return r.functionSQL(ctx, view, catalog.QuoteIdentifier(namespace.Name())+"."+catalog.QuoteIdentifier(procedure.Name()), n.Args)
	case plan.InstalledCubeExpr:
		if !ReferenceEpochValid(view, r.retainedConsumer(), n.Ref) || n.Ref.Address.SubID != 0 || !r.sameRoutineDescriptor(view, n.Ref.Address, n.Procedure) {
			return "", fmt.Errorf("stuntgres: invalid stored cube reference")
		}
		if err := ValidateInstalledCube(n, view); err != nil {
			return "", err
		}
		procedure, _ := view.Procedure(n.Procedure.Address())
		namespace, ok := view.Namespace(procedure.Namespace())
		if !ok {
			return "", fmt.Errorf("stuntgres: missing stored cube namespace")
		}
		r.record(n.Ref)
		return r.functionSQL(ctx, view, catalog.QuoteIdentifier(namespace.Name())+"."+catalog.QuoteIdentifier(procedure.Name()), n.Args)
	case plan.InstalledSoundexExpr:
		if !ReferenceEpochValid(view, r.retainedConsumer(), n.Ref) || n.Ref.Address.SubID != 0 || !r.sameRoutineDescriptor(view, n.Ref.Address, n.Procedure) {
			return "", fmt.Errorf("stuntgres: invalid stored Soundex reference")
		}
		if err := ValidateInstalledSoundex(n, view); err != nil {
			return "", err
		}
		procedure, _ := view.Procedure(n.Procedure.Address())
		namespace, ok := view.Namespace(procedure.Namespace())
		if !ok {
			return "", fmt.Errorf("stuntgres: missing stored Soundex namespace")
		}
		r.record(n.Ref)
		return r.functionSQL(ctx, view, catalog.QuoteIdentifier(namespace.Name())+"."+catalog.QuoteIdentifier(procedure.Name()), n.Args)
	case plan.InstalledMetaphoneExpr:
		if !ReferenceEpochValid(view, r.retainedConsumer(), n.Ref) || n.Ref.Address.SubID != 0 || !r.sameRoutineDescriptor(view, n.Ref.Address, n.Procedure) {
			return "", fmt.Errorf("stuntgres: invalid stored Metaphone reference")
		}
		if err := ValidateInstalledMetaphone(n, view); err != nil {
			return "", err
		}
		procedure, _ := view.Procedure(n.Procedure.Address())
		namespace, ok := view.Namespace(procedure.Namespace())
		if !ok {
			return "", fmt.Errorf("stuntgres: missing stored Metaphone namespace")
		}
		r.record(n.Ref)
		return r.functionSQL(ctx, view, catalog.QuoteIdentifier(namespace.Name())+"."+catalog.QuoteIdentifier(procedure.Name()), n.Args)
	case plan.InstalledLevenshteinExpr:
		if !ReferenceEpochValid(view, r.retainedConsumer(), n.Ref) || n.Ref.Address.SubID != 0 || !r.sameRoutineDescriptor(view, n.Ref.Address, n.Procedure) {
			return "", fmt.Errorf("stuntgres: invalid stored Levenshtein reference")
		}
		if err := ValidateInstalledLevenshtein(n, view); err != nil {
			return "", err
		}
		procedure, _ := view.Procedure(n.Procedure.Address())
		namespace, ok := view.Namespace(procedure.Namespace())
		if !ok {
			return "", fmt.Errorf("stuntgres: missing stored Levenshtein namespace")
		}
		r.record(n.Ref)
		return r.functionSQL(ctx, view, catalog.QuoteIdentifier(namespace.Name())+"."+catalog.QuoteIdentifier(procedure.Name()), n.Args)
	case plan.InstalledUUIDOsspExpr:
		if !ReferenceEpochValid(view, r.retainedConsumer(), n.Ref) || n.Ref.Address.SubID != 0 || !r.sameRoutineDescriptor(view, n.Ref.Address, n.Procedure) {
			return "", fmt.Errorf("stuntgres: invalid stored uuid-ossp reference")
		}
		if err := ValidateInstalledUUIDOssp(n, view); err != nil {
			return "", err
		}
		procedure, _ := view.Procedure(n.Procedure.Address())
		namespace, ok := view.Namespace(procedure.Namespace())
		if !ok {
			return "", fmt.Errorf("stuntgres: missing stored uuid-ossp namespace")
		}
		r.record(n.Ref)
		return r.functionSQL(ctx, view, catalog.QuoteIdentifier(namespace.Name())+"."+catalog.QuoteIdentifier(procedure.Name()), n.Args)
	case plan.InstalledCryptoExpr:
		if !ReferenceEpochValid(view, r.retainedConsumer(), n.Ref) || n.Ref.Address.SubID != 0 || !r.sameRoutineDescriptor(view, n.Ref.Address, n.Procedure) {
			return "", fmt.Errorf("stuntgres: invalid stored crypto reference")
		}
		if err := ValidateInstalledCrypto(n, view); err != nil {
			return "", err
		}
		procedure, _ := view.Procedure(n.Procedure.Address())
		namespace, ok := view.Namespace(procedure.Namespace())
		if !ok {
			return "", fmt.Errorf("stuntgres: missing stored crypto namespace")
		}
		r.record(n.Ref)
		return r.functionSQL(ctx, view, catalog.QuoteIdentifier(namespace.Name())+"."+catalog.QuoteIdentifier(procedure.Name()), n.Args)
	case plan.CitextComparisonExpr:
		original, ok := n.RoutineReference.(*catalog.Procedure)
		if !ok || !ReferenceEpochValid(view, r.retainedConsumer(), n.Ref) || !r.sameRoutineDescriptor(view, n.Ref.Address, original) {
			return "", fmt.Errorf("stuntgres: invalid stored citext comparison reference")
		}
		procedure, err := currentCitextComparison(n, view)
		if err != nil {
			return "", err
		}
		namespace, ok := view.Namespace(procedure.Namespace())
		if !ok {
			return "", fmt.Errorf("stuntgres: missing stored citext comparison namespace")
		}
		spec, err := citextComparisonRendering(n, catalog.QuoteIdentifier(namespace.Name())+"."+catalog.QuoteIdentifier(procedure.Name()))
		if err != nil {
			return "", err
		}
		r.record(n.Ref)
		return r.rendering(ctx, view, spec)
	case plan.CitextPatternExpr:
		original, ok := n.RoutineReference.(*catalog.Procedure)
		if !ok || !ReferenceEpochValid(view, r.retainedConsumer(), n.Ref) || !r.sameRoutineDescriptor(view, n.Ref.Address, original) {
			return "", fmt.Errorf("stuntgres: invalid stored citext pattern reference")
		}
		procedure, err := currentCitextPattern(n, view)
		if err != nil {
			return "", err
		}
		namespace, ok := view.Namespace(procedure.Namespace())
		if !ok {
			return "", fmt.Errorf("stuntgres: missing stored citext pattern namespace")
		}
		spec, err := citextPatternRendering(n, catalog.QuoteIdentifier(namespace.Name())+"."+catalog.QuoteIdentifier(procedure.Name()))
		if err != nil {
			return "", err
		}
		r.record(n.Ref)
		return r.rendering(ctx, view, spec)
	case plan.CubeComparisonExpr:
		original, ok := n.RoutineReference.(*catalog.Procedure)
		if !ok || !ReferenceEpochValid(view, r.retainedConsumer(), n.Ref) || n.Ref.Address.SubID != 0 || !r.sameRoutineDescriptor(view, n.Ref.Address, original) {
			return "", fmt.Errorf("stuntgres: invalid stored cube comparison reference")
		}
		procedure, err := currentCubeComparison(n, view)
		if err != nil {
			return "", err
		}
		namespace, ok := view.Namespace(procedure.Namespace())
		if !ok {
			return "", fmt.Errorf("stuntgres: missing stored cube comparison namespace")
		}
		spec, err := cubeComparisonRendering(n, catalog.QuoteIdentifier(namespace.Name())+"."+catalog.QuoteIdentifier(procedure.Name()))
		if err != nil {
			return "", err
		}
		r.record(n.Ref)
		return r.rendering(ctx, view, spec)
	case plan.InstalledEarthPointExpr:
		if !ReferenceEpochValid(view, r.retainedConsumer(), n.Ref) || n.Ref.Address.SubID != 0 || !r.sameRoutineDescriptor(view, n.Ref.Address, n.Procedure) {
			return "", fmt.Errorf("stuntgres: invalid stored earth point reference")
		}
		procedure, err := currentEarthPointProcedure(n, view)
		if err != nil {
			return "", err
		}
		namespace, ok := view.Namespace(procedure.Namespace())
		if !ok {
			return "", fmt.Errorf("stuntgres: missing stored earth point namespace")
		}
		r.record(n.Ref)
		return r.functionSQL(ctx, view, catalog.QuoteIdentifier(namespace.Name())+"."+catalog.QuoteIdentifier(procedure.Name()), n.Args[:])
	case plan.InstalledCubeSpatialExpr:
		if !ReferenceEpochValid(view, r.retainedConsumer(), n.Ref) || n.Ref.Address.SubID != 0 || !r.sameRoutineDescriptor(view, n.Ref.Address, n.Procedure) {
			return "", fmt.Errorf("stuntgres: invalid stored cube spatial reference")
		}
		procedure, err := currentCubeSpatialProcedure(n, view)
		if err != nil {
			return "", err
		}
		namespace, ok := view.Namespace(procedure.Namespace())
		if !ok {
			return "", fmt.Errorf("stuntgres: missing stored cube spatial namespace")
		}
		r.record(n.Ref)
		return r.functionSQL(ctx, view, catalog.QuoteIdentifier(namespace.Name())+"."+catalog.QuoteIdentifier(procedure.Name()), n.Args[:])
	case plan.InstalledTextExpr:
		if err := ValidateInstalledText(n, view); err != nil {
			return "", err
		}
		proc, ok := r.descriptor(view, n.Ref.Address).(*catalog.Procedure)
		if !ok || !ReferenceEpochValid(view, r.retainedConsumer(), n.Ref) || n.Ref.Address.SubID != 0 || !analyze.ValidInstalledText(n) || !exec.SameRoutineDeclaration(proc, n.Procedure) {
			return "", fmt.Errorf("stuntgres: invalid stored installed function")
		}
		proc, _ = view.Procedure(n.Procedure.Address())
		namespace, ok := view.Namespace(proc.Namespace())
		if !ok {
			return "", fmt.Errorf("stuntgres: missing stored function namespace")
		}
		r.record(n.Ref)
		return r.functionSQL(ctx, view, catalog.QuoteIdentifier(namespace.Name())+"."+catalog.QuoteIdentifier(proc.Name()), []expr.Expr{n.Arg})
	case plan.InstalledPgrowlocksExpr:
		if err := ValidateInstalledPgrowlocks(n, view); err != nil {
			return "", err
		}
		procedure, _ := view.Procedure(n.Procedure.Address())
		namespace, ok := view.Namespace(procedure.Namespace())
		if !ok {
			return "", fmt.Errorf("stuntgres: missing stored pgrowlocks namespace")
		}
		r.record(n.Ref)
		return r.functionSQL(ctx, view, catalog.QuoteIdentifier(namespace.Name())+"."+catalog.QuoteIdentifier(procedure.Name()), []expr.Expr{n.Arg})
	case plan.InstalledVerifyHeapamExpr:
		if err := ValidateInstalledVerifyHeapam(n, view); err != nil {
			return "", err
		}
		procedure, _ := view.Procedure(n.Procedure.Address())
		namespace, ok := view.Namespace(procedure.Namespace())
		if !ok {
			return "", fmt.Errorf("stuntgres: missing stored verify_heapam namespace")
		}
		r.record(n.Ref)
		return r.functionSQL(ctx, view, catalog.QuoteIdentifier(namespace.Name())+"."+catalog.QuoteIdentifier(procedure.Name()), n.Args[:])
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
		spec, err = currentRoutineRendering(view, node, spec)
		if err != nil {
			return "", err
		}
		return r.rendering(ctx, view, spec)
	default:
		return "", fmt.Errorf("stuntgres: unsupported stored default node")
	}
}
