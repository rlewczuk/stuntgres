package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// InstalledRoutine resolves the namespace path first, so its lookup errors
// precede every signature diagnostic; the procedure lookup itself is pure.
// A procedure installed by another provider is not the extension's routine.
func (b *expressionBinder) InstalledRoutine(name parser.ObjectName, extension functions.Extension, signature string) (functions.InstalledRoutine, error) {
	auth, err := b.bindingAuthorization()
	if err != nil {
		return nil, err
	}
	path, err := b.engine.lookupNamespacesSnapshot(auth, name, installedFunctionLookup)
	if err != nil {
		return nil, err
	}
	text := name.Parts[len(name.Parts)-1].Text
	var proc *catalog.Procedure
	if extension == functions.PgcryptoExtension {
		for i := range path.count() {
			namespace := path.at(i)
			// The implicit catalog precedes ordinary paths; explicit ordering
			// can instead select the installed wrapper and its dependency.
			if text == "gen_random_uuid" && namespace.ObjectID == ids.PGCatalogNamespaceOID {
				return functions.CatalogBuiltin{}, nil
			}
			proc = procedureInNamespaces(auth.catalog, namespaceSelection{qualified: namespace}, text, catalog.ProcedureSignature(signature))
			if proc != nil {
				break
			}
		}
	} else {
		proc = procedureInNamespaces(auth.catalog, path, text, catalog.ProcedureSignature(signature))
	}
	if proc == nil {
		return nil, nil
	}
	var installed bool
	switch extension {
	case functions.UUIDOsspExtension:
		installed = uuidOsspProviderOf(proc) == stockUUIDOssp
	case functions.PgcryptoExtension:
		installed = pgcryptoProviderOf(proc) == stockPgcrypto
	case functions.FuzzystrmatchExtension:
		installed = fuzzystrmatchProviderOf(proc) == stockFuzzystrmatch
	case functions.UnaccentExtension:
		installed = unaccentProviderOf(proc) == stockUnaccent
	case functions.IntaggExtension:
		installed = intaggProviderOf(proc) == stockIntagg
	default:
		return nil, fmt.Errorf("stuntgres: unknown installed extension %d", extension)
	}
	if !installed {
		return nil, nil
	}
	return installedExtensionRoutine{binder: b, catalog: auth.catalog, procedure: proc}, nil
}

// installedExtensionRoutine is the procedure identity handed to bind code
// through functions.Binder.InstalledRoutine.
type installedExtensionRoutine struct {
	binder    *expressionBinder
	catalog   catalog.Read
	procedure *catalog.Procedure
}

func (r installedExtensionRoutine) Validate() error {
	p := r.procedure
	switch {
	case uuidOsspProviderOf(p) != nil:
		return stockUUIDOssp.validateProcedure(r.catalog, p)
	case pgcryptoProviderOf(p) != nil:
		return stockPgcrypto.validateProcedure(r.catalog, p)
	case fuzzystrmatchProviderOf(p) != nil:
		return stockFuzzystrmatch.validateProcedure(r.catalog, p)
	case intaggProviderOf(p) != nil:
		return stockIntagg.validateProcedure(r.catalog, p)
	case unaccentProviderOf(p) == stockUnaccent:
		ext, ok := r.catalog.Extension(p.Extension)
		if !ok {
			return fmt.Errorf("stuntgres: missing unaccent installation")
		}
		return stockUnaccent.ValidateExtension(r.catalog, ext)
	}
	return fmt.Errorf("stuntgres: invalid installed routine")
}

// Attach wraps the bound call with the procedure identity. Each family
// records its dependency as its engine binder did: deduplicated, except the
// Levenshtein, Metaphone and Soundex calls, which always appended.
func (r installedExtensionRoutine) Attach(call expr.Expr) (expr.Expr, error) {
	p, b := r.procedure, r.binder
	ref := b.engine.catalog.Domain.Ref(r.catalog.Epoch(), p.Address())
	appended := catalog.ExpressionDependency{Ref: ref, Revision: p.Revision()}
	switch c := call.(type) {
	case functions.UUIDGeneration:
		if uuidOsspProviderOf(p) != stockUUIDOssp {
			break
		}
		c.RoutineReference = p
		b.dependency(ref, p.Revision())
		return installedUUIDOsspFunctionExpression{UUIDGeneration: c, procedure: p, ref: ref}, nil
	case functions.Pgcrypto:
		if pgcryptoProviderOf(p) != stockPgcrypto {
			break
		}
		c.RoutineReference = p
		b.dependency(ref, p.Revision())
		return installedCryptoFunctionExpression{Pgcrypto: c, procedure: p, ref: ref}, nil
	case functions.Levenshtein:
		if fuzzystrmatchProviderOf(p) != stockFuzzystrmatch {
			break
		}
		c.RoutineReference = p
		b.dependencies = append(b.dependencies, appended)
		return installedLevenshteinFunctionExpression{Levenshtein: c, procedure: p, ref: ref}, nil
	case functions.Metaphone:
		if fuzzystrmatchProviderOf(p) != stockFuzzystrmatch {
			break
		}
		c.RoutineReference = p
		b.dependencies = append(b.dependencies, appended)
		return installedMetaphoneFunctionExpression{Metaphone: c, procedure: p, ref: ref}, nil
	case functions.Soundex:
		if fuzzystrmatchProviderOf(p) != stockFuzzystrmatch {
			break
		}
		c.RoutineReference = p
		b.dependencies = append(b.dependencies, appended)
		return installedSoundexFunctionExpression{Soundex: c, procedure: p, ref: ref}, nil
	case functions.InstalledText:
		return r.attachText(c, ref)
	case functions.IntaggEnumeration:
		if intaggProviderOf(p) != stockIntagg {
			break
		}
		return r.attachIntaggEnumeration(c)
	}
	return nil, fmt.Errorf("stuntgres: invalid installed %T call", call)
}

// attachText captures unaccent's installed dictionary and template, whose
// provider supplies the rules evaluation applies.
func (r installedExtensionRoutine) attachText(call functions.InstalledText, ref ids.ObjectRef) (expr.Expr, error) {
	p := r.procedure
	var dict *catalog.TSDictionary
	var tmpl *catalog.TSTemplate
	if unaccentProviderOf(p) != nil {
		var ok bool
		dict, ok = r.catalog.TSDictionary(p.Dictionary)
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing installed unaccent dictionary")
		}
		tmpl, ok = r.catalog.TSTemplate(dict.Template)
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing installed unaccent template")
		}
		call.Matcher = unaccentDictionaryProviderOf(dict).matcher
	}
	r.binder.dependency(ref, p.Revision())
	call.RoutineReference = p
	node := installedFunctionExpression{InstalledText: call, procedure: p, dictionary: dict, template: tmpl, ref: ref}
	if !node.validProvider() {
		return nil, fmt.Errorf("stuntgres: invalid installed function binding")
	}
	return node, nil
}
