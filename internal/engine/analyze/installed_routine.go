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
	earth := family == functions.EarthdistanceExtension
	spatial := family == functions.CubeExtension && signature == ""
	if spatial {
		if _, selected := sqltype.CubeSpatialKind(text); !selected {
			return nil, nil
		}
	}
	var proc *catalog.Procedure
	if selected := b.Clause.selectedProcedure; selected != nil && selected.Implementation != nil && (string(selected.Signature) == signature || spatial && extension.CubeProviderOf(selected) == extension.StockCube || earth && extension.EarthdistanceProviderOf(selected) == extension.StockEarthdistance) {
		symbol, failure := extension.ProcedureSymbol(auth.ReadCatalog(), selected)
		if failure != nil {
			return nil, failure
		}
		if symbol == text {
			proc = selected
		}
	}
	if earth && proc == nil {
		for i := range path.Count() {
			for candidate := range catalog.DefinitionProceduresNamedAt(auth.ReadCatalog(), b.Host.DefinitionOf(auth), path.At(i), text) {
				if extension.EarthdistanceProviderOf(candidate) != extension.StockEarthdistance {
					continue
				}
				if ProcedureInNamespaces(auth.ReadCatalog(), path, text, candidate.Signature) != candidate {
					return nil, nil
				}
				return installedExtensionRoutine{binder: b, catalog: auth.ReadCatalog(), procedure: candidate}, nil
			}
		}
		return nil, nil
	}
	if spatial && proc == nil {
		// Discover only an authentic binary cube signature, then apply the
		// ordinary path/signature winner so a preceding shadow is never skipped.
		for i := range path.Count() {
			for candidate := range catalog.DefinitionProceduresNamedAt(auth.ReadCatalog(), b.Host.DefinitionOf(auth), path.At(i), text) {
				if extension.CubeProviderOf(candidate) != extension.StockCube {
					continue
				}
				if selected := ProcedureInNamespaces(auth.ReadCatalog(), path, text, candidate.Signature); selected != candidate {
					return nil, nil
				}
				routine := installedExtensionRoutine{binder: b, catalog: auth.ReadCatalog(), procedure: candidate}
				return routine, nil
			}
		}
		return nil, nil
	}
	if proc != nil {
		// The shared callable selector already resolved the live public name.
	} else if family == functions.PgcryptoExtension {
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
	case functions.AmcheckExtension:
		installed = extension.AmcheckProviderOf(proc) == extension.StockAmcheck
	case functions.PgrowlocksExtension:
		installed = extension.PgrowlocksProviderOf(proc) == extension.StockPgrowlocks
	case functions.CubeExtension:
		installed = extension.CubeProviderOf(proc) == extension.StockCube
	case functions.EarthdistanceExtension:
		installed = extension.EarthdistanceProviderOf(proc) == extension.StockEarthdistance
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
	case extension.AmcheckProviderOf(p) != nil:
		return extension.StockAmcheck.ValidateProcedure(r.catalog, p)
	case extension.PgrowlocksProviderOf(p) != nil:
		return extension.StockPgrowlocks.ValidateProcedure(r.catalog, p)
	case extension.CubeProviderOf(p) != nil:
		return extension.StockCube.ValidateProcedure(r.catalog, p)
	case extension.EarthdistanceProviderOf(p) != nil:
		return extension.StockEarthdistance.ValidateProcedure(r.catalog, p)
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

func (r installedExtensionRoutine) EarthDescriptor() *sqltype.Descriptor {
	ext, ok := r.catalog.Extension(r.procedure.Extension)
	if !ok || extension.EarthdistanceProviderOf(r.procedure) != extension.StockEarthdistance || len(ext.Members) != extension.EarthMemberCount {
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
	appended := catalog.ExpressionDependency{Ref: ref, Revision: p.Revision(), Purpose: b.dependencyPurpose}
	switch c := call.(type) {
	case functions.Cube:
		if extension.CubeProviderOf(p) != extension.StockCube {
			break
		}
		c.RoutineReference = p
		b.dependency(ref, p.Revision())
		return plan.InstalledCubeExpr{Cube: c, Procedure: p, Ref: ref}, nil
	case functions.EarthCoordinate:
		if extension.EarthdistanceProviderOf(p) != extension.StockEarthdistance {
			break
		}
		// A freshly constructed domain result demands the same captured
		// visibility-based diagnostic naming as an ordinary domain conversion.
		if c.ResultUsage().Type.HasDomainConstraints() {
			if err := b.CaptureTypeDiagnosticInput(c.ResultUsage().Type); err != nil {
				return nil, err
			}
		}
		c.RoutineReference = p
		b.dependency(ref, p.Revision())
		return plan.InstalledEarthExpr{EarthCoordinate: c, Procedure: p, Ref: ref}, nil
	case functions.EarthPoint:
		if extension.EarthdistanceProviderOf(p) != extension.StockEarthdistance {
			break
		}
		c.RoutineReference = p
		b.dependency(ref, p.Revision())
		return plan.InstalledEarthPointExpr{EarthPoint: c, Procedure: p, Ref: ref}, nil
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
	case functions.CubeSpatial:
		if extension.CubeProviderOf(p) != extension.StockCube {
			break
		}
		c.RoutineReference = p
		b.dependency(ref, p.Revision())
		return plan.InstalledCubeSpatialExpr{CubeSpatial: c, Procedure: p, Ref: ref}, nil
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
	case functions.Amcheck:
		if err := extension.ValidateAmcheckIdentity(r.catalog, p, ref); err != nil {
			return nil, err
		}
		c.RoutineReference = p
		if err := extension.ValidateAmcheckCall(r.catalog, p, c); err != nil {
			return nil, err
		}
		b.dependency(ref, p.Revision())
		return plan.InstalledAmcheckExpr{Amcheck: c, Procedure: p, Ref: ref}, nil
	case functions.Pgrowlocks:
		return r.attachPgrowlocks(c, ref)
	case functions.VerifyHeapam:
		return r.attachVerifyHeapam(c, ref)
	}
	return nil, fmt.Errorf("stuntgres: invalid installed %T call", call)
}

// attachText captures the installed function. unaccent's default dictionary is
// resolved in the current function namespace when a non-NULL call executes.
func (r installedExtensionRoutine) attachText(call functions.InstalledText, ref ids.ObjectRef) (expr.Expr, error) {
	p := r.procedure
	r.binder.dependency(ref, p.Revision())
	call.RoutineReference = p
	node := plan.InstalledTextExpr{InstalledText: call, Procedure: p, Ref: ref}
	if !ValidInstalledText(node) {
		return nil, fmt.Errorf("stuntgres: invalid installed function binding")
	}
	return node, nil
}

func (r installedExtensionRoutine) CubeType() *sqltype.Descriptor {
	ext := r.catalog.MustExtension(r.procedure.Extension)
	return r.catalog.MustType(ext.Members[0]).SQLType
}
