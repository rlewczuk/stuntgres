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
	pgstats := family == functions.PgstattupleExtension && signature == ""
	spatial := family == functions.CubeExtension && signature == ""
	crosstab := family == functions.TablefuncExtension && signature == ""
	buffercache := family == functions.PgBuffercacheExtension && signature == ""
	if spatial {
		if _, selected := sqltype.CubeSpatialKind(text); !selected {
			return nil, nil
		}
	}
	var proc *catalog.Procedure
	if selected := b.Clause.selectedProcedure; selected != nil && family == functions.TablefuncExtension && !crosstab && string(selected.Signature) != signature {
		return nil, nil
	}
	if selected := b.Clause.selectedProcedure; selected != nil && selected.Implementation != nil {
		if !(string(selected.Signature) == signature || buffercache && extension.PgBuffercacheProviderOf(selected) == extension.StockPgBuffercache || crosstab && extension.TablefuncProviderOf(selected) == extension.StockTablefunc || pgstats && extension.PgstattupleProviderOf(selected) == extension.StockPgstattuple || spatial && extension.CubeProviderOf(selected) == extension.StockCube || earth && extension.EarthdistanceProviderOf(selected) == extension.StockEarthdistance) {
			return nil, nil
		}
		binding, failure := extension.ProcedureBindingName(auth.ReadCatalog(), selected)
		if failure != nil {
			return nil, failure
		}
		definition, ok := functions.Lookup(binding)
		if !ok || definition.Installed != family || binding != text {
			return nil, nil
		}
		proc = selected
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
	if pgstats && proc == nil {
		proc = ProcedureInNamespaces(auth.ReadCatalog(), path, text, "25")
		if proc == nil {
			proc = ProcedureInNamespaces(auth.ReadCatalog(), path, text, "2205")
		}
	}
	if buffercache && proc == nil {
		for _, candidate := range []catalog.ProcedureSignature{"", "23", "2205"} {
			proc = ProcedureInNamespaces(auth.ReadCatalog(), path, text, candidate)
			if proc != nil {
				break
			}
		}
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
	case functions.DblinkExtension:
		installed = extension.DblinkProviderOf(proc) == extension.StockDblink
	case functions.TcnExtension:
		installed = extension.TcnProviderOf(proc) == extension.StockTcn
	case functions.AutoincExtension:
		installed = extension.AutoincProviderOf(proc) == extension.StockAutoinc
	case functions.PgstattupleExtension:
		installed = extension.PgstattupleProviderOf(proc) == extension.StockPgstattuple
	case functions.PgPrewarmExtension:
		installed = extension.PgPrewarmProviderOf(proc) == extension.StockPgPrewarm
	case functions.PgFreespaceExtension:
		installed = extension.PgFreespaceProviderOf(proc) == extension.StockPgFreespace
	case functions.PgBuffercacheExtension:
		installed = extension.PgBuffercacheProviderOf(proc) == extension.StockPgBuffercache
	case functions.PgvisibilityExtension:
		installed = extension.PgvisibilityProviderOf(proc) == extension.StockPgvisibility
	case functions.PgsurgeryExtension:
		installed = extension.PgsurgeryProviderOf(proc) == extension.StockPgsurgery
	case functions.PageinspectExtension:
		installed = extension.PageinspectProviderOf(proc) == extension.StockPageinspect
	case functions.XML2Extension:
		installed = extension.XML2ProviderOf(proc) == extension.StockXML2
	case functions.SSLInfoExtension:
		installed = extension.SSLInfoProviderOf(proc) == extension.StockSSLInfo
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
	case functions.PgStatStatementsExtension:
		installed = extension.PgStatStatementsProviderOf(proc) == extension.StockPgStatStatements
	case functions.TablefuncExtension:
		installed = extension.TablefuncProviderOf(proc) == extension.StockTablefunc
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

func (r installedExtensionRoutine) TablefuncCrosstabSignature() string {
	return string(r.procedure.Signature)
}

func (r installedExtensionRoutine) Validate() error {
	p := r.procedure
	switch {
	case extension.DblinkProviderOf(p) != nil:
		return extension.StockDblink.ValidateProcedure(r.catalog, p)
	case extension.TcnProviderOf(p) != nil:
		return extension.StockTcn.ValidateProcedure(r.catalog, p)
	case extension.AutoincProviderOf(p) != nil:
		return extension.StockAutoinc.ValidateProcedure(r.catalog, p)
	case extension.PgstattupleProviderOf(p) != nil:
		return extension.StockPgstattuple.ValidateProcedure(r.catalog, p)
	case extension.PgPrewarmProviderOf(p) != nil:
		return extension.StockPgPrewarm.ValidateProcedure(r.catalog, p)
	case extension.PgFreespaceProviderOf(p) != nil:
		return extension.StockPgFreespace.ValidateProcedure(r.catalog, p)
	case extension.PgBuffercacheProviderOf(p) != nil:
		return extension.StockPgBuffercache.ValidateProcedure(r.catalog, p)
	case extension.PgvisibilityProviderOf(p) != nil:
		return extension.StockPgvisibility.ValidateProcedure(r.catalog, p)
	case extension.PgsurgeryProviderOf(p) != nil:
		return extension.StockPgsurgery.ValidateProcedure(r.catalog, p)
	case extension.PageinspectProviderOf(p) != nil:
		return extension.StockPageinspect.ValidateProcedure(r.catalog, p)
	case extension.XML2ProviderOf(p) != nil:
		return extension.StockXML2.ValidateProcedure(r.catalog, p)
	case extension.SSLInfoProviderOf(p) != nil:
		return extension.StockSSLInfo.ValidateProcedure(r.catalog, p)
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
	case extension.PgStatStatementsProviderOf(p) != nil:
		return extension.StockPgStatStatements.ValidateProcedure(r.catalog, p)
	case extension.TablefuncProviderOf(p) != nil:
		return extension.StockTablefunc.ValidateProcedure(r.catalog, p)
	case extension.CubeProviderOf(p) != nil:
		return extension.StockCube.ValidateProcedure(r.catalog, p)
	case extension.EarthdistanceProviderOf(p) != nil:
		return extension.StockEarthdistance.ValidateProcedure(r.catalog, p)
	case extension.UnaccentProviderOf(p) == extension.StockUnaccent:
		return (extension.CatalogProvider{Class: ids.ProcedureClassID}).Validate(r.catalog, p)
	}
	return fmt.Errorf("stuntgres: invalid installed routine")
}

func (r installedExtensionRoutine) PgstattupleStatsInput() sqltype.ID {
	if r.procedure.Signature == "2205" {
		return sqltype.Regclass
	}
	return sqltype.Text
}

func (r installedExtensionRoutine) PgstattupleOperation() functions.PgstattupleOperation {
	symbol, err := extension.ProcedureSymbol(r.catalog, r.procedure)
	if err == nil {
		switch symbol {
		case "pgstatindex":
			return functions.BtreeStats
		case "pgstathashindex":
			return functions.HashStats
		case "pgstatginindex":
			return functions.GinStats
		}
	}
	return functions.PgstattupleOperation(255)
}

func (r installedExtensionRoutine) PgstattupleDeclaration() (functions.HeapStatisticsKind, sqltype.ID, string, []sqltype.CompositeMember) {
	kind := functions.HeapStatisticsExact
	switch r.procedure.Display.Source {
	case "pgstattuple_approx_v1_5":
		kind = functions.HeapStatisticsApprox
	case "pg_relpages_v1_5", "pg_relpagesbyid_v1_5":
		kind = functions.HeapStatisticsPages
	}
	input := r.PgstattupleStatsInput()
	formal := r.procedure.Installed.ArgumentNames[0]
	var members []sqltype.CompositeMember
	if outputs := r.procedure.OutputDeclaration(); outputs != nil {
		formal = outputs.Name(0)
		members = make([]sqltype.CompositeMember, outputs.Len())
		for i := range outputs.Len() {
			members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: outputs.Name(outputs.Inputs() + i), Usage: r.binder.Type(sqltype.ID(outputs.Type(i))).Usage()}
		}
	}
	return kind, input, formal, members
}

func (r installedExtensionRoutine) PgFreespaceDeclaration() (functions.PgFreespaceKind, []sqltype.CompositeMember) {
	member := extension.PgFreespaceMembers[r.procedure.Origin.Declaration]
	var members []sqltype.CompositeMember
	if outputs := r.procedure.OutputDeclaration(); outputs != nil {
		members = make([]sqltype.CompositeMember, outputs.Len())
		for i := range outputs.Len() {
			members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: outputs.Name(outputs.Inputs() + i), Usage: r.binder.Type(sqltype.ID(outputs.Type(i))).Usage()}
		}
	}
	return member.Kind, members
}
func (r installedExtensionRoutine) PgvisibilityDeclaration() (functions.PgvisibilityKind, []sqltype.CompositeMember) {
	member := extension.PgvisibilityMembers[r.procedure.Origin.Declaration]
	if member.Result != 2249 {
		return member.Kind, nil
	}
	var members []sqltype.CompositeMember
	if outputs := r.procedure.OutputDeclaration(); outputs != nil {
		members = make([]sqltype.CompositeMember, outputs.Len())
		for i := range outputs.Len() {
			members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: outputs.Name(outputs.Inputs() + i), Usage: r.binder.Type(sqltype.ID(outputs.Type(i))).Usage()}
		}
	}
	return member.Kind, members
}
func (r installedExtensionRoutine) PgBuffercacheDeclaration() (functions.PgBuffercacheKind, bool) {
	kind, err := extension.PgBuffercacheKindOf(r.catalog, r.procedure)
	return kind, err == nil
}
func (r installedExtensionRoutine) PgBuffercacheEvictionDeclaration() (functions.PgBuffercacheEvictionKind, []sqltype.CompositeMember) {
	slot := r.procedure.Origin.Declaration
	if slot == 0 || int(slot) > len(extension.PgBuffercacheEvictionMembers) {
		return functions.PgBuffercacheEvictionKind(255), nil
	}
	member := extension.PgBuffercacheEvictionMembers[slot-1]
	outputs := r.procedure.OutputDeclaration()
	members := make([]sqltype.CompositeMember, outputs.Len())
	for i := range members {
		members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: outputs.Name(outputs.Inputs() + i), Usage: r.binder.Type(sqltype.ID(outputs.Type(i))).Usage()}
	}
	return member.Kind, members
}

func (r installedExtensionRoutine) PgBuffercacheAggregateDeclaration() (functions.PgBuffercacheAggregateKind, []sqltype.CompositeMember) {
	slot := r.procedure.Origin.Declaration
	if slot < extension.PgBuffercacheAggregateStart || int(slot) >= extension.PgBuffercacheAggregateStart+len(extension.PgBuffercacheAggregateMembers) {
		return functions.PgBuffercacheAggregateKind(255), nil
	}
	member := extension.PgBuffercacheAggregateMembers[slot-extension.PgBuffercacheAggregateStart]
	outputs := r.procedure.OutputDeclaration()
	members := make([]sqltype.CompositeMember, outputs.Len())
	for i := range members {
		members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: outputs.Name(i), Usage: r.binder.Type(sqltype.ID(outputs.Type(i))).Usage()}
	}
	return member.Kind, members
}

func (r installedExtensionRoutine) CubeDescriptor() *sqltype.Descriptor {
	ext, ok := catalog.OriginInstallation(r.procedure)
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
	ext, ok := catalog.OriginInstallation(r.procedure)
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
	if err := b.captureNativeDeclarationBody(p); err != nil {
		return nil, err
	}
	ref := b.Host.CatalogOwner().Domain.Ref(r.catalog.Epoch(), p.Address())
	appended := catalog.ExpressionDependency{Ref: ref, Revision: p.Revision(), Purpose: b.dependencyPurpose}
	switch c := call.(type) {
	case functions.DblinkConnection:
		if err := extension.ValidateDblinkIdentity(r.catalog, p, ref); err != nil {
			return nil, err
		}
		c.RoutineReference = p
		if err := extension.ValidateDblinkConnectionCall(r.catalog, p, c); err != nil {
			return nil, err
		}
		b.dependency(ref, p.Revision())
		return plan.InstalledDblinkConnectionExpr{DblinkConnection: c, Procedure: p, Ref: ref}, nil
	case functions.DblinkBuildSQLDelete:
		if err := extension.ValidateDblinkIdentity(r.catalog, p, ref); err != nil {
			return nil, err
		}
		c.RoutineReference = p
		if err := extension.ValidateDblinkBuildSQLDeleteCall(r.catalog, p, c); err != nil {
			return nil, err
		}
		b.dependency(ref, p.Revision())
		return plan.InstalledDblinkBuildSQLDeleteExpr{DblinkBuildSQLDelete: c, Procedure: p, Ref: ref}, nil
	case functions.DblinkBuildSQLInsert:
		if err := extension.ValidateDblinkIdentity(r.catalog, p, ref); err != nil {
			return nil, err
		}
		c.RoutineReference = p
		if err := extension.ValidateDblinkBuildSQLInsertCall(r.catalog, p, c); err != nil {
			return nil, err
		}
		b.dependency(ref, p.Revision())
		return plan.InstalledDblinkBuildSQLInsertExpr{DblinkBuildSQLInsert: c, Procedure: p, Ref: ref}, nil
	case functions.DblinkBuildSQLUpdate:
		if err := extension.ValidateDblinkIdentity(r.catalog, p, ref); err != nil {
			return nil, err
		}
		c.RoutineReference = p
		if err := extension.ValidateDblinkBuildSQLUpdateCall(r.catalog, p, c); err != nil {
			return nil, err
		}
		b.dependency(ref, p.Revision())
		return plan.InstalledDblinkBuildSQLUpdateExpr{DblinkBuildSQLUpdate: c, Procedure: p, Ref: ref}, nil
	case functions.Tcn:
		if extension.TcnProviderOf(p) != extension.StockTcn {
			break
		}
		c.RoutineReference = p
		b.dependency(ref, p.Revision())
		return c, nil
	case functions.TriggerBuiltin:
		if extension.AutoincProviderOf(p) != extension.StockAutoinc || c.Name != "autoinc" {
			break
		}
		if err := extension.ValidateAutoincIdentity(r.catalog, p, ref); err != nil {
			return nil, err
		}
		c.RoutineReference = p
		b.dependency(ref, p.Revision())
		return plan.InstalledAutoincExpr{TriggerBuiltin: c, Procedure: p, Ref: ref}, nil
	case functions.PgstattupleStats:
		if err := extension.ValidatePgstattupleIdentity(r.catalog, p, ref); err != nil {
			return nil, err
		}
		c.RoutineReference = p
		if err := extension.ValidatePgstattupleStatsCall(r.catalog, p, c); err != nil {
			return nil, err
		}
		b.dependency(ref, p.Revision())
		return plan.InstalledPgstattupleStatsExpr{PgstattupleStats: c, Procedure: p, Ref: ref}, nil
	case functions.Pgstattuple:
		if err := extension.ValidatePgstattupleIdentity(r.catalog, p, ref); err != nil {
			return nil, err
		}
		c.RoutineReference = p
		if err := extension.ValidatePgstattupleCall(r.catalog, p, c); err != nil {
			return nil, err
		}
		b.dependency(ref, p.Revision())
		return plan.InstalledPgstattupleExpr{Pgstattuple: c, Procedure: p, Ref: ref}, nil
	case functions.PgPrewarm:
		if err := extension.ValidatePgPrewarmIdentity(r.catalog, p, ref); err != nil { return nil, err }
		c.RoutineReference = p
		if err := extension.ValidatePgPrewarmCall(r.catalog, p, c); err != nil { return nil, err }
		b.dependency(ref, p.Revision())
		return plan.InstalledPgPrewarmExpr{PgPrewarm: c, Procedure: p, Ref: ref}, nil
	case functions.PgFreespace:
		if err := extension.ValidatePgFreespaceIdentity(r.catalog, p, ref); err != nil {
			return nil, err
		}
		if c.Kind.Set() {
			if b.Params.ExecuteParameters {
				return nil, expr.ErrorAt("0A000", "set-returning functions are not allowed in EXECUTE parameters", c.Pos)
			}
			if b.Consumer.PolicyConsumer && b.Query.Frame == 0 {
				return nil, sqlError("0A000", "set-returning functions are not allowed in policy expressions")
			}
			if err := b.AdmitSetReturning(c.Pos); err != nil {
				return nil, err
			}
		}
		c.RoutineReference = p
		if err := extension.ValidatePgFreespaceCall(r.catalog, p, c); err != nil {
			return nil, err
		}
		b.dependency(ref, p.Revision())
		return plan.InstalledPgFreespaceExpr{PgFreespace: c, Procedure: p, Ref: ref}, nil
	case functions.PgBuffercacheEviction:
		if err := extension.ValidatePgBuffercacheIdentity(r.catalog, p, ref); err != nil {
			return nil, err
		}
		c.RoutineReference = p
		if err := extension.ValidatePgBuffercacheEvictionCall(r.catalog, p, c); err != nil {
			return nil, err
		}
		b.dependency(ref, p.Revision())
		return plan.InstalledPgBuffercacheEvictionExpr{PgBuffercacheEviction: c, Procedure: p, Ref: ref}, nil
	case functions.PgBuffercacheAggregate:
		if err := extension.ValidatePgBuffercacheIdentity(r.catalog, p, ref); err != nil {
			return nil, err
		}
		if c.Kind.Set() {
			if b.Params.ExecuteParameters {
				return nil, expr.ErrorAt("0A000", "set-returning functions are not allowed in EXECUTE parameters", c.Pos)
			}
			if b.Consumer.PolicyConsumer && b.Query.Frame == 0 {
				return nil, sqlError("0A000", "set-returning functions are not allowed in policy expressions")
			}
			if err := b.AdmitSetReturning(c.Pos); err != nil {
				return nil, err
			}
		}
		c.RoutineReference = p
		if err := extension.ValidatePgBuffercacheAggregateCall(r.catalog, p, c); err != nil {
			return nil, err
		}
		b.dependency(ref, p.Revision())
		return plan.InstalledPgBuffercacheAggregateExpr{PgBuffercacheAggregate: c, Procedure: p, Ref: ref}, nil
	case functions.Pgvisibility:
		if err := extension.ValidatePgvisibilityIdentity(r.catalog, p, ref); err != nil {
			return nil, err
		}
		if c.Kind.Set() {
			if b.Params.ExecuteParameters {
				return nil, expr.ErrorAt("0A000", "set-returning functions are not allowed in EXECUTE parameters", c.Pos)
			}
			if b.Consumer.PolicyConsumer && b.Query.Frame == 0 {
				return nil, sqlError("0A000", "set-returning functions are not allowed in policy expressions")
			}
			if err := b.AdmitSetReturning(c.Pos); err != nil {
				return nil, err
			}
		}
		c.RoutineReference = p
		if err := extension.ValidatePgvisibilityCall(r.catalog, p, c); err != nil {
			return nil, err
		}
		b.dependency(ref, p.Revision())
		return plan.InstalledPgvisibilityExpr{Pgvisibility: c, Procedure: p, Ref: ref}, nil
	case functions.Pgsurgery:
		if err := extension.ValidatePgsurgeryIdentity(r.catalog, p, ref); err != nil {
			return nil, err
		}
		symbol, err := extension.ProcedureSymbol(r.catalog, p)
		if err != nil {
			return nil, err
		}
		switch symbol {
		case "heap_force_freeze":
			c.Operation = functions.PgsurgeryFreeze
		case "heap_force_kill":
			c.Operation = functions.PgsurgeryKill
		default:
			return nil, fmt.Errorf("stuntgres: invalid pg_surgery operation")
		}
		c.RoutineReference = p
		if err := extension.ValidatePgsurgeryCall(r.catalog, p, c); err != nil {
			return nil, err
		}
		b.dependency(ref, p.Revision())
		return plan.InstalledPgsurgeryExpr{Pgsurgery: c, Procedure: p, Ref: ref}, nil
	case functions.Pageinspect:
		if err := extension.ValidatePageinspectIdentity(r.catalog, p, ref); err != nil {
			return nil, err
		}
		symbol, err := extension.ProcedureSymbol(r.catalog, p)
		if err != nil {
			return nil, err
		}
		if symbol != c.Member {
			return nil, fmt.Errorf("stuntgres: invalid pageinspect call member")
		}
		if p.Installed.ReturnsSet {
			if b.Params.ExecuteParameters {
				return nil, expr.ErrorAt("0A000", "set-returning functions are not allowed in EXECUTE parameters", c.Pos)
			}
			if b.Consumer.PolicyConsumer && b.Query.Frame == 0 {
				return nil, sqlError("0A000", "set-returning functions are not allowed in policy expressions")
			}
			if err := b.AdmitSetReturning(c.Pos); err != nil {
				return nil, err
			}
		}
		c.RoutineReference = p
		if err := extension.ValidatePageinspectCall(r.catalog, p, c); err != nil {
			return nil, err
		}
		b.dependency(ref, p.Revision())
		return plan.InstalledPageinspectExpr{Pageinspect: c, Procedure: p, Ref: ref}, nil
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
	case functions.XML2:
		if extension.XML2ProviderOf(p) != extension.StockXML2 {
			break
		}
		c.RoutineReference = p
		node := plan.InstalledXML2Expr{XML2: c, Procedure: p, Ref: ref}
		if p.DeclarationBody != nil {
			inner, err := extension.XML2Kernel(r.catalog, p)
			if err != nil {
				return nil, err
			}
			innerRef := r.catalog.Domain().Ref(r.catalog.Epoch(), inner.Address())
			b.dependency(innerRef, inner.Revision())
			node.Kernel = inner
		}
		if !ValidInstalledXML2Shape(node) {
			return nil, fmt.Errorf("stuntgres: invalid installed xml2 binding")
		}
		b.dependency(ref, p.Revision())
		return node, nil
	case functions.SSLInfo:
		if extension.SSLInfoProviderOf(p) != extension.StockSSLInfo {
			break
		}
		if _, set := c.SourceAdmission(); set {
			if b.Params.ExecuteParameters {
				return nil, expr.ErrorAt("0A000", "set-returning functions are not allowed in EXECUTE parameters", c.Pos)
			}
			if b.Consumer.PolicyConsumer && b.Query.Frame == 0 {
				return nil, sqlError("0A000", "set-returning functions are not allowed in policy expressions")
			}
			if err := b.AdmitSetReturning(c.Pos); err != nil {
				return nil, err
			}
		}
		c.RoutineReference = p
		b.dependency(ref, p.Revision())
		return plan.InstalledSSLInfoExpr{SSLInfo: c, Procedure: p, Ref: ref}, nil
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
	case functions.DblinkGetPkey:
		return r.attachDblinkGetPkey(c, ref)
	case functions.PgStatStatements:
		if err := extension.StockPgStatStatements.ValidateProcedure(r.catalog, p); err != nil {
			return nil, err
		}
		if !c.InfoOnly {
			if b.Params.ExecuteParameters {
				return nil, expr.ErrorAt("0A000", "set-returning functions are not allowed in EXECUTE parameters", c.Pos)
			}
			if b.Consumer.PolicyConsumer && b.Query.Frame == 0 {
				return nil, sqlError("0A000", "set-returning functions are not allowed in policy expressions")
			}
			if err := b.AdmitSetReturning(c.Pos); err != nil {
				return nil, err
			}
		}
		c.RoutineReference = p
		b.dependency(ref, p.Revision())
		return plan.InstalledPgStatStatementsExpr{PgStatStatements: c, Procedure: p, Ref: ref}, nil
	case functions.PgStatStatementsReset:
		if err := extension.StockPgStatStatements.ValidateProcedure(r.catalog, p); err != nil {
			return nil, err
		}
		if p.Origin.Declaration != 2 {
			return nil, fmt.Errorf("stuntgres: mismatched statement statistics reset declaration")
		}
		c.RoutineReference = p
		b.dependency(ref, p.Revision())
		return plan.InstalledPgStatStatementsResetExpr{PgStatStatementsReset: c, Procedure: p, Ref: ref}, nil
	case functions.PgpArmorHeaders:
		return r.attachPgpArmorHeaders(c, ref)
	case functions.TablefuncNormal:
		return r.attachTablefuncNormal(c, ref)
	case functions.Connectby:
		if err := extension.ValidateTablefuncIdentity(r.catalog, p, ref); err != nil {
			return nil, err
		}
		if b.Params.ExecuteParameters {
			return nil, expr.ErrorAt("0A000", "set-returning functions are not allowed in EXECUTE parameters", c.Pos)
		}
		if b.Consumer.PolicyConsumer && b.Query.Frame == 0 {
			return nil, sqlError("0A000", "set-returning functions are not allowed in policy expressions")
		}
		if err := b.AdmitSetReturning(c.Pos); err != nil {
			return nil, err
		}
		c.RoutineReference = p
		b.dependency(ref, p.Revision())
		return plan.InstalledConnectbyExpr{Connectby: c, Procedure: p, Ref: ref}, nil
	case functions.TablefuncCrosstab:
		return r.attachTablefuncCrosstab(c, ref)
	case functions.XML2XPathTable:
		return r.attachXML2XPathTable(c, ref)
	case functions.PgBuffercache:
		return r.attachPgBuffercache(c, ref)
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
	node := (plan.InstalledTextExpr{InstalledText: call, Procedure: p, Ref: ref}).CaptureTypes(r.catalog)
	if !ValidInstalledText(node) {
		return nil, fmt.Errorf("stuntgres: invalid installed function binding")
	}
	return node, nil
}

func (r installedExtensionRoutine) CubeType() *sqltype.Descriptor {
	return r.CubeDescriptor()
}
