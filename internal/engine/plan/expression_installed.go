package plan

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Installed extension routine nodes wrap a functions kernel with the captured
// installed procedure and its object reference. SQL authority and stored
// recipes always use that captured identity, never a builtin alias or
// literal. The engine validates the identity against the extension providers.

// InstalledTextExpr is an installed text routine: unaccent with its captured
// dictionary and template, or fuzzystrmatch daitch_mokotoff.
type InstalledTextExpr struct {
	functions.InstalledText
	Procedure  *catalog.Procedure
	Dictionary *catalog.TSDictionary
	Template   *catalog.TSTemplate
	Ref        ids.ObjectRef
}

// Eval runs in the engine, which checks the captured provider first.
func (n InstalledTextExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	runtime, err := nodeRuntimeOf(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	return runtime.EvalInstalledText(n)
}

// InstalledCryptoExpr is an installed pgcrypto routine.
type InstalledCryptoExpr struct {
	functions.Pgcrypto
	Procedure *catalog.Procedure
	Ref       ids.ObjectRef
}

// Rebuild keeps the installed-routine identity; the promoted Rebuild would
// return the bare functions node.
func (e InstalledCryptoExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	node, err := e.Pgcrypto.Rebuild(info, children)
	if err != nil {
		return nil, err
	}
	e.Pgcrypto = node.(functions.Pgcrypto)
	return e, nil
}

// InstalledUUIDOsspExpr is an installed uuid-ossp generator. The embedded
// selector shares the UUID kernels.
type InstalledUUIDOsspExpr struct {
	functions.UUIDGeneration
	Procedure *catalog.Procedure
	Ref       ids.ObjectRef
}

// Rebuild keeps the installed-routine identity; the promoted Rebuild would
// return the bare functions node.
func (e InstalledUUIDOsspExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	node, err := e.UUIDGeneration.Rebuild(info, children)
	if err != nil {
		return nil, err
	}
	e.UUIDGeneration = node.(functions.UUIDGeneration)
	return e, nil
}

// InstalledMetaphoneExpr is an installed fuzzystrmatch metaphone-family
// routine.
type InstalledMetaphoneExpr struct {
	functions.Metaphone
	Procedure *catalog.Procedure
	Ref       ids.ObjectRef
}

// Rebuild keeps the installed-routine identity; the promoted Rebuild would
// return the bare functions node.
func (e InstalledMetaphoneExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	node, err := e.Metaphone.Rebuild(info, children)
	if err != nil {
		return nil, err
	}
	e.Metaphone = node.(functions.Metaphone)
	return e, nil
}

// InstalledLevenshteinExpr is an installed fuzzystrmatch levenshtein routine.
type InstalledLevenshteinExpr struct {
	functions.Levenshtein
	Procedure *catalog.Procedure
	Ref       ids.ObjectRef
}

// Rebuild keeps the installed-routine identity; the promoted Rebuild would
// return the bare functions node.
func (e InstalledLevenshteinExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	node, err := e.Levenshtein.Rebuild(info, children)
	if err != nil {
		return nil, err
	}
	e.Levenshtein = node.(functions.Levenshtein)
	return e, nil
}

// InstalledSoundexExpr is an installed fuzzystrmatch soundex-family routine.
type InstalledSoundexExpr struct {
	functions.Soundex
	Procedure *catalog.Procedure
	Ref       ids.ObjectRef
}

// Rebuild keeps the installed-routine identity; the promoted Rebuild would
// return the bare functions node.
func (e InstalledSoundexExpr) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	node, err := e.Soundex.Rebuild(info, children)
	if err != nil {
		return nil, err
	}
	e.Soundex = node.(functions.Soundex)
	return e, nil
}
