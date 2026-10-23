package analyze

import (
	"fmt"

	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// InstalledRoutine resolves the namespace path first, so its lookup errors
// precede every signature diagnostic; the procedure lookup itself is pure.
// A procedure installed by another provider is not the extension's routine.
func (b *Binder) InstalledRoutine(name parser.ObjectName, family functions.Extension, signature string) (functions.InstalledRoutine, error) {
	auth, err := b.BindingAuthorization()
	if err != nil {
		return nil, err
	}
	path, err := b.Host.LookupNamespacesSnapshot(auth, name, InstalledFunctionLookup)
	if err != nil {
		return nil, err
	}
	text := name.Parts[len(name.Parts)-1].Text
	var proc *catalog.Procedure
	if family == functions.PgcryptoExtension {
		for i := range path.Count() {
			namespace := path.At(i)
			// The implicit catalog precedes ordinary paths; explicit ordering
			// can instead select the installed wrapper and its dependency.
			if text == "gen_random_uuid" && namespace.ObjectID == ids.PGCatalogNamespaceOID {
				return functions.CatalogBuiltin{}, nil
			}
			proc = ProcedureInNamespaces(auth.ReadCatalog(), NamespaceSelection{Qualified: namespace}, text, catalog.ProcedureSignature(signature))
			if proc != nil {
				break
			}
		}
	} else {
		proc = ProcedureInNamespaces(auth.ReadCatalog(), path, text, catalog.ProcedureSignature(signature))
	}
	if proc == nil {
		return nil, nil
	}
	var installed bool
	switch family {
	case functions.UUIDOsspExtension:
		installed = extension.UUIDOsspProviderOf(proc) == extension.StockUUIDOssp
	case functions.PgcryptoExtension:
		installed = extension.PgcryptoProviderOf(proc) == extension.StockPgcrypto
	case functions.FuzzystrmatchExtension:
		installed = extension.FuzzystrmatchProviderOf(proc) == extension.StockFuzzystrmatch
	case functions.UnaccentExtension:
		installed = extension.UnaccentProviderOf(proc) == extension.StockUnaccent
	case functions.IntaggExtension:
		installed = extension.IntaggProviderOf(proc) == extension.StockIntagg
	case functions.PgrowlocksExtension:
		installed = extension.PgrowlocksProviderOf(proc) == extension.StockPgrowlocks
	case functions.CubeExtension:
		installed = extension.CubeProviderOf(proc) == extension.StockCube
	default:
		return nil, fmt.Errorf("stuntgres: unknown installed extension %d", family)
	}
	if !installed {
		return nil, nil
	}
	return installedExtensionRoutine{binder: b, catalog: auth.ReadCatalog(), procedure: proc}, nil
}

// installedExtensionRoutine is the procedure identity handed to bind code
// through functions.Binder.InstalledRoutine.
type installedExtensionRoutine struct {
	binder    *Binder
	catalog   catalog.Read
	procedure *catalog.Procedure
}

func (r installedExtensionRoutine) Validate() error {
	p := r.procedure
	switch {
	case extension.UUIDOsspProviderOf(p) != nil:
		return extension.StockUUIDOssp.ValidateProcedure(r.catalog, p)
	case extension.PgcryptoProviderOf(p) != nil:
		return extension.StockPgcrypto.ValidateProcedure(r.catalog, p)
	case extension.FuzzystrmatchProviderOf(p) != nil:
		return extension.StockFuzzystrmatch.ValidateProcedure(r.catalog, p)
	case extension.IntaggProviderOf(p) != nil:
		return extension.StockIntagg.ValidateProcedure(r.catalog, p)
	case extension.PgrowlocksProviderOf(p) != nil:
		return extension.StockPgrowlocks.ValidateProcedure(r.catalog, p)
	case extension.CubeProviderOf(p) != nil:
		return extension.StockCube.ValidateProcedure(r.catalog, p)
	case extension.UnaccentProviderOf(p) == extension.StockUnaccent:
		ext, ok := r.catalog.Extension(p.Extension)
		if !ok {
			return fmt.Errorf("stuntgres: missing unaccent installation")
		}
		return extension.StockUnaccent.ValidateExtension(r.catalog, ext)
	}
	return fmt.Errorf("stuntgres: invalid installed routine")
}

func (r installedExtensionRoutine) CubeDescriptor() *sqltype.Descriptor {
	ext, ok := r.catalog.Extension(r.procedure.Extension)
	if !ok || extension.CubeProviderOf(r.procedure) != extension.StockCube || len(ext.Members) == 0 {
		return nil
	}
	base, ok := r.catalog.Type(ext.Members[0])
	if !ok {
		return nil
	}
	return base.SQLType
}

// Attach wraps the bound call with the procedure identity. Each family
// records its dependency as its engine binder did: deduplicated, except the
// Levenshtein, Metaphone and Soundex calls, which always appended.
func (r installedExtensionRoutine) Attach(call expr.Expr) (expr.Expr, error) {
	p, b := r.procedure, r.binder
	ref := b.Host.CatalogOwner().Domain.Ref(r.catalog.Epoch(), p.Address())
	appended := catalog.ExpressionDependency{Ref: ref, Revision: p.Revision()}
	switch c := call.(type) {
	case functions.Cube:
		if extension.CubeProviderOf(p) != extension.StockCube {
			break
		}
		c.RoutineReference = p
		b.dependency(ref, p.Revision())
		return plan.InstalledCubeExpr{Cube: c, Procedure: p, Ref: ref}, nil
	case functions.UUIDGeneration:
		if extension.UUIDOsspProviderOf(p) != extension.StockUUIDOssp {
			break
		}
		c.RoutineReference = p
		b.dependency(ref, p.Revision())
		return plan.InstalledUUIDOsspExpr{UUIDGeneration: c, Procedure: p, Ref: ref}, nil
	case functions.Pgcrypto:
		if extension.PgcryptoProviderOf(p) != extension.StockPgcrypto {
			break
		}
		c.RoutineReference = p
		b.dependency(ref, p.Revision())
		return plan.InstalledCryptoExpr{Pgcrypto: c, Procedure: p, Ref: ref}, nil
	case functions.Levenshtein:
		if extension.FuzzystrmatchProviderOf(p) != extension.StockFuzzystrmatch {
			break
		}
		c.RoutineReference = p
		b.Dependencies = append(b.Dependencies, appended)
		return plan.InstalledLevenshteinExpr{Levenshtein: c, Procedure: p, Ref: ref}, nil
	case functions.Metaphone:
		if extension.FuzzystrmatchProviderOf(p) != extension.StockFuzzystrmatch {
			break
		}
		c.RoutineReference = p
		b.Dependencies = append(b.Dependencies, appended)
		return plan.InstalledMetaphoneExpr{Metaphone: c, Procedure: p, Ref: ref}, nil
	case functions.Soundex:
		if extension.FuzzystrmatchProviderOf(p) != extension.StockFuzzystrmatch {
			break
		}
		c.RoutineReference = p
		b.Dependencies = append(b.Dependencies, appended)
		return plan.InstalledSoundexExpr{Soundex: c, Procedure: p, Ref: ref}, nil
	case functions.InstalledText:
		return r.attachText(c, ref)
	case functions.IntaggEnumeration:
		if extension.IntaggProviderOf(p) != extension.StockIntagg {
			break
		}
		return r.attachIntaggEnumeration(c)
	case functions.Pgrowlocks:
		return r.attachPgrowlocks(c, ref)
	}
	return nil, fmt.Errorf("stuntgres: invalid installed %T call", call)
}

// attachText captures unaccent's installed dictionary and template, whose
// provider supplies the rules evaluation applies.
func (r installedExtensionRoutine) attachText(call functions.InstalledText, ref ids.ObjectRef) (expr.Expr, error) {
	p := r.procedure
	var dict *catalog.TSDictionary
	var tmpl *catalog.TSTemplate
	if extension.UnaccentProviderOf(p) != nil {
		var ok bool
		dict, ok = r.catalog.TSDictionary(p.Dictionary)
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing installed unaccent dictionary")
		}
		tmpl, ok = r.catalog.TSTemplate(dict.Template)
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing installed unaccent template")
		}
		call.Matcher = extension.UnaccentDictionaryProviderOf(dict).Matcher
	}
	r.binder.dependency(ref, p.Revision())
	call.RoutineReference = p
	node := plan.InstalledTextExpr{InstalledText: call, Procedure: p, Dictionary: dict, Template: tmpl, Ref: ref}
	if !ValidInstalledText(node) {
		return nil, fmt.Errorf("stuntgres: invalid installed function binding")
	}
	return node, nil
}
