package extension

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// ProcedureSymbol resolves immutable released declaration authority. Public
// names, namespaces and current extension membership never select a symbol.
func ProcedureSymbol(v catalog.Read, p *catalog.Procedure) (string, error) {
	return procedureIdentityName(v, p, false)
}

// ProcedureInstallationName returns the immutable released SQL name rather than
// a C symbol or mutable public label.
func ProcedureInstallationName(v catalog.Read, p *catalog.Procedure) (string, error) {
	return procedureIdentityName(v, p, true)
}

func procedureIdentityName(v catalog.Read, p *catalog.Procedure, installationName bool) (string, error) {
	invalid := func() (string, error) { return "", fmt.Errorf("stuntgres: invalid installed procedure identity") }
	if p == nil || v.Owner() == nil || v.Get(p.Address()) != p || p.Address().ClassID != ids.ProcedureClassID || p.Address().ObjectID == 0 || p.Address().SubID != 0 || p.Revision() == 0 || p.Owner.ID() == 0 || p.Owner != v.Owner().Principals.Principal(p.Owner.ID()) || p.SQL != nil || p.Builtin != nil || p.Foreign != nil || p.Conversion.Name() != "" || v.NameAddress(catalog.DescriptorNameKey(p)) != p.Address() {
		return invalid()
	}
	if (p.SecurityDefiner || p.Configuration != nil) && !StockInsertUsername.SupportsDMLTrigger(p) {
		return invalid()
	}
	// These providers own fixed OUT declarations. Their exact slot-owned
	// capabilities are checked below; output shape alone never grants authority.
	if p.Installed.Outputs != nil && p.Implementation != StockPgrowlocks && p.Implementation != StockAmcheck && p.Implementation != StockPageinspect && p.Implementation != StockPgstattuple && p.Implementation != StockHstore {
		return invalid()
	}
	if _, ok := v.Namespace(p.Namespace()); !ok {
		return invalid()
	}
	if err := catalog.ValidateExtensionMembership(v, p); err != nil {
		return invalid()
	}
	if !p.Origin.Valid(p.Address()) {
		return invalid()
	}
	ext := p.Origin.Installation
	implementation, registered := v.Owner().Extensions[ext.Name()]
	if !registered || implementation != p.Implementation || implementation != ext.Implementation || implementation.Versions().InstallPath(ext.Version) == nil {
		return invalid()
	}
	slot := p.Origin.Declaration
	canonical, ok := ext.Declarations[slot].(*catalog.Procedure)
	if !ok || p.Display != canonical.Display || p.Installed != canonical.Installed || p.DeclarationBody != canonical.DeclarationBody || p.Language != canonical.Language || p.SQLBody != canonical.SQLBody || p.SQLDefaults != canonical.SQLDefaults || p.LiteralDefaults != canonical.LiteralDefaults || p.InputNames != canonical.InputNames || p.PLHandler != canonical.PLHandler || p.RangeConstructor != canonical.RangeConstructor || p.AccessMethod != canonical.AccessMethod || p.BrinSupport != canonical.BrinSupport || len(p.Body) != 0 || len(p.DeclarationDependencies) != 0 {
		return invalid()
	}
	name, signature, result := "", catalog.ProcedureSignature(""), ids.OID(0)
	sqlName := ""
	kind, volatility, strict := catalog.FunctionRoutine, expr.Immutable, true
	count := 0
	switch p.Implementation {
	case StockPgcrypto:
		count = len(functions.PgcryptoMembers)
		if slot >= count {
			return invalid()
		}
		m := functions.PgcryptoMembers[slot]
		name, signature, result, volatility, strict = m.Name, catalog.ProcedureSignature(m.Signature), m.Result, m.Volatility, m.Strict
	case StockUUIDOssp:
		count = len(functions.UUIDOsspMembers)
		if slot >= count {
			return invalid()
		}
		m := functions.UUIDOsspMembers[slot]
		name, signature, result, volatility = m.Name, catalog.ProcedureSignature(m.Signature), 2950, m.Volatility
		declaration := catalog.InstalledRoutineDeclaration{Parallel: 's'}
		copy(declaration.ArgumentNames[:], m.ArgumentNames[:])
		if p.Installed != declaration {
			return invalid()
		}
	case StockFuzzystrmatch:
		count = len(FuzzystrmatchMembers)
		index := slot
		if ext.Version == "1.1" {
			count--
			index++
		}
		if slot >= count {
			return invalid()
		}
		m := FuzzystrmatchMembers[index]
		name, signature, result = m.Name, m.Signature, m.Result
	case StockIntagg:
		count = len(IntaggMembers)
		if slot >= count {
			return invalid()
		}
		m := IntaggMembers[slot]
		name, signature, result, strict, kind = m.Name, m.Signature, m.Result, m.Strict, m.Kind
		if p.Installed != intaggDeclaration(slot) {
			return invalid()
		}
		if err := validateOwnedAggregate(v, p); err != nil {
			return "", err
		}
	case StockEarthdistance:
		count = EarthMemberCount
		if ext.Version != "1.2" || slot < 2 || slot >= EarthPointOperatorSlot {
			return invalid()
		}
		if slot == EarthPointProcedureSlot {
			name, signature, result = "geo_distance", "600,600", ids.OID(sqltype.Float8)
		} else {
			op := sqltype.EarthOperation(slot - 2)
			if p.DeclarationBody == nil {
				return invalid()
			}
			earthID := sqltype.ID(ext.Members[0].ObjectID)
			if op >= sqltype.EarthLL {
				base, ok := v.Type(ext.Members[0])
				if !ok || base.SQLType == nil || !base.SQLType.IsDomain() || !base.SQLType.DomainBase().Type.IsCube() || !catalog.SameInstallation(base, ext) {
					return invalid()
				}
				if err := ValidateProviderHeader(v, base, ids.TypeClassID); err != nil {
					return invalid()
				}
			}
			dependencies := 0
			valid := StockEarthdistance.VisitProcedureDependencies(v, p, func(dependency ids.ObjectAddress) bool {
				helper, ok := v.Procedure(dependency)
				if !ok {
					return false
				}
				symbol, err := ProcedureSymbol(v, helper)
				if err != nil {
					return false
				}
				if dependency == ext.Members[2] {
					if !catalog.SameInstallation(helper, ext) || symbol != "earth" {
						return false
					}
				} else if helper.Implementation != StockCube || symbol != "cube" && symbol != "cube_ll_coord" {
					return false
				}
				dependencies++
				return true
			})
			expected := 0
			switch op {
			case sqltype.EarthSecToGC, sqltype.EarthGCToSec, sqltype.EarthLongitude:
				expected = 1
			case sqltype.EarthLatitude:
				expected = 2
			case sqltype.EarthLL:
				expected = 3
			}
			if !valid || dependencies != expected {
				return invalid()
			}
			name, signature, result, strict = op.Name(), earthRoutineSignature(op, earthID), earthRoutineResult(op, earthID), op != sqltype.EarthRadiusOperation
		}
	case StockCube:
		count = CubeMemberCount
		if ext.Version != StockCube.control.Version || slot < 1 || slot >= cubeSpatialOperatorStart {
			return invalid()
		}
		base, ok := v.Type(ext.Members[0])
		if !ok || base.SQLType == nil || !base.SQLType.IsCube() || base.Address() != ext.Members[0] || !catalog.SameInstallation(base, ext) || base.Namespace() == (ids.ObjectAddress{}) || base.Revision() == 0 {
			return invalid()
		}
		if err := ValidateProviderHeader(v, base, ids.TypeClassID); err != nil {
			return invalid()
		}
		if slot >= cubeSpatialProcedureStart {
			member := cubeSpatialMembers[slot-cubeSpatialProcedureStart]
			name, signature, result = member.operation.Routine(), CubeBinarySignature(base.SQLType.ID()), ids.OID(member.operation.ResultID())
		} else {
			spec, ok := cubeRoutineForMember(slot)
			if !ok {
				return invalid()
			}
			name, signature, result = spec.name, spec.signature(base.SQLType.ID()), spec.resultOID(base.SQLType.ID())
		}
	case StockIsn:
		count = isnMemberCount
		found := false
		for _, recipe := range isnRecipes {
			if slot != recipe.slot+1 && slot != recipe.slot+2 {
				continue
			}
			if len(ext.Members) != count {
				return invalid()
			}
			base, ok := v.Type(ext.Members[recipe.slot])
			if !ok || !recipe.accept(base.SQLType) || !catalog.SameInstallation(base, ext) || base.Revision() == 0 {
				return invalid()
			}
			if err := ValidateProviderHeader(v, base, ids.TypeClassID); err != nil {
				return invalid()
			}
			name, sqlName, signature, result = recipe.inputSource, recipe.input, "2275", ids.OID(base.SQLType.ID())
			if slot == recipe.slot+2 {
				name, sqlName, signature, result = recipe.output, recipe.output, IsnSignature(base.SQLType.ID()), 2275
			}
			found = true
		}
		if !found {
			return invalid()
		}
	case StockLtree:
		count = LtreeMemberCount
		if len(ext.Members) != count || slot < 0 || slot >= count || ltreeMemberClass(slot) != ids.ProcedureClassID {
			return invalid()
		}
		if slot >= LtreePathProcedureStart && slot < LtreePathOperatorStart {
			var err error
			signature, result, err = ltreePathSignature(v, ext, slot-LtreePathProcedureStart)
			if err != nil {
				return invalid()
			}
			name = ltreePathRecipes[slot-LtreePathProcedureStart].symbol
			sqlName = ltreePathRecipes[slot-LtreePathProcedureStart].name
		} else if slot >= LtreeLqueryProcedureStart && slot < LtreeLqueryOperatorStart {
			index := slot - LtreeLqueryProcedureStart
			left, right, err := ltreeLqueryTypes(v, ext, index)
			if err != nil {
				return invalid()
			}
			name = LtreeLqueryProcedureSymbol(sqltype.LtreeLqueryOperation(index))
			signature = catalog.ProcedureSignature(fmt.Sprintf("%d,%d", left.ID(), right.ID()))
			result = 16
		} else if slot >= LtreeLtxtqueryProcedureStart && slot < LtreeLtxtqueryOperatorStart {
			index := slot - LtreeLtxtqueryProcedureStart
			left, right, err := ltreeLtxtqueryTypes(v, ext, index)
			if err != nil {
				return invalid()
			}
			name = LtreeLtxtqueryProcedureSymbol(sqltype.LtreeLtxtqueryOperation(index))
			signature, result = catalog.ProcedureSignature(fmt.Sprintf("%d,%d", left.ID(), right.ID())), 16
		} else if slot >= LtreeHierarchyProcedureStart && slot < LtreeHierarchyOperatorStart {
			base, ok := v.Type(ext.Members[3])
			if !ok || base.SQLType == nil || !base.SQLType.IsLtree() || !catalog.SameInstallation(base, ext) {
				return invalid()
			}
			if err := ValidateProviderHeader(v, base, ids.TypeClassID); err != nil {
				return invalid()
			}
			name = LtreeHierarchyProcedureSymbol(sqltype.LtreeHierarchyOperation(slot - LtreeHierarchyProcedureStart))
			signature = catalog.ProcedureSignature(fmt.Sprintf("%d,%d", base.SQLType.ID(), base.SQLType.ID()))
			result = 16
		} else if slot >= LtreeComparisonProcedureStart && slot < LtreeComparisonOperatorStart {
			base, ok := v.Type(ext.Members[3])
			if !ok || base.SQLType == nil || !base.SQLType.IsLtree() || !catalog.SameInstallation(base, ext) {
				return invalid()
			}
			if err := ValidateProviderHeader(v, base, ids.TypeClassID); err != nil {
				return invalid()
			}
			name, signature, result = LtreeComparisonDeclaration(slot-LtreeComparisonProcedureStart, base.SQLType.ID())
		} else if slot >= LtreeArrayProcedureStart {
			index := slot - LtreeArrayProcedureStart
			var err error
			signature, result, err = ltreeArraySignature(v, ext, index)
			if err != nil {
				return invalid()
			}
			name = ltreeArrayRecipes[index].name
		} else {
			recipe := ltreeRecipes[slot/3]
			base, ok := v.Type(ext.Members[recipe.slot])
			if !ok || !recipe.accept(base.SQLType) || !catalog.SameInstallation(base, ext) || base.Namespace() == (ids.ObjectAddress{}) || base.Revision() == 0 {
				return invalid()
			}
			if err := ValidateProviderHeader(v, base, ids.TypeClassID); err != nil {
				return invalid()
			}
			name, signature, result = recipe.input, "2275", ids.OID(base.SQLType.ID())
			if slot%3 == 2 {
				name, signature, result = recipe.output, LquerySignature(base.SQLType.ID()), 2275
			}
		}
		if p.Display != (catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: name, Binary: "$libdir/ltree", HasBinary: true}) || p.Installed != (catalog.InstalledRoutineDeclaration{}) || p.Language != (ids.ObjectAddress{}) || p.SQLBody != nil || p.SQLDefaults != nil || len(p.Body) != 0 {
			return invalid()
		}
	case StockHstore, StockIntarray, StockSeg, StockCitext:
		count = 3
		if p.Implementation == StockCitext {
			count = CitextMemberCount
		}
		if p.Implementation == StockHstore {
			count = HstoreMemberCount
		}
		if slot < 1 || slot >= count || p.Implementation == StockCitext && CitextOperatorSlot(slot) || p.Implementation == StockHstore && hstoreMemberClass(slot) != ids.ProcedureClassID {
			return invalid()
		}
		base, ok := v.Type(ext.Members[0])
		if !ok || base.SQLType == nil {
			return invalid()
		}
		if base.Address() != ext.Members[0] || !catalog.SameInstallation(base, ext) || base.Namespace() == (ids.ObjectAddress{}) || base.Revision() == 0 || p.Implementation == StockHstore && !base.SQLType.IsHstore() || p.Implementation == StockIntarray && !base.SQLType.IsQueryInt() || p.Implementation == StockSeg && !base.SQLType.IsSeg() || p.Implementation == StockCitext && !base.SQLType.IsCitext() {
			return invalid()
		}
		if err := ValidateProviderHeader(v, base, ids.TypeClassID); err != nil {
			return invalid()
		}
		signature, result = "2275", ids.OID(base.SQLType.ID())
		switch p.Implementation {
		case StockHstore:
			recipe, ok := hstoreRoutineAt(slot)
			if !ok {
				return invalid()
			}
			name = recipe.symbol
			sqlName = recipe.name
			strict = recipe.strict
			if p.Display != (catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: name, Binary: "$libdir/hstore", HasBinary: true}) || p.Installed != hstoreRoutineDeclaration(slot) || p.Language != (ids.ObjectAddress{}) || p.SQLBody != nil || p.SQLDefaults != nil || len(p.Body) != 0 {
				return invalid()
			}
		case StockIntarray:
			name = "bqarr_in"
			if slot == 2 {
				name = "bqarr_out"
			}
		case StockSeg:
			name = "seg_in"
			if slot == 2 {
				name = "seg_out"
			}
			if p.Display != (catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: name, Binary: "$libdir/seg", HasBinary: true}) || p.Installed != (catalog.InstalledRoutineDeclaration{}) || p.Language != (ids.ObjectAddress{}) || p.SQLBody != nil || p.SQLDefaults != nil || len(p.Body) != 0 {
				return invalid()
			}
		case StockCitext:
			name = "citextin"
			if slot == 2 {
				name = "citextout"
			}
		}
		if slot == 2 {
			signature, result = QueryIntSignature(base.SQLType.ID()), 2275
		} else if p.Implementation == StockCitext && slot >= CitextComparisonProcedureStart && slot < CitextComparisonOperatorStart {
			name = CitextComparisonNames[slot-CitextComparisonProcedureStart]
			signature, result = CitextComparisonSignature(base.SQLType.ID()), ids.OID(sqltype.Bool)
		}
		if p.Implementation == StockHstore {
			recipe, ok := hstoreRoutineAt(slot)
			if !ok {
				return invalid()
			}
			signature, result = recipe.declaration(base.SQLType.ID())
		}
		if p.Implementation == StockCitext && slot >= CitextConversionProcedureStart && slot < CitextPatternProcedureStart {
			name, signature, result = "citext", []catalog.ProcedureSignature{"1042", "16", "869"}[slot-CitextConversionProcedureStart], ids.OID(base.SQLType.ID())
		}
		if p.Implementation == StockCitext && slot >= CitextPatternProcedureStart && slot < CitextPatternOperatorStart {
			index := slot - CitextPatternProcedureStart
			name = CitextPatternNames[index/2]
			signature, result = CitextPatternSignature(base.SQLType.ID(), citextPatternRight(base.SQLType.ID(), index%2)), ids.OID(sqltype.Bool)
			if p.Display.Language != catalog.InternalRoutineLanguage || p.Display.Source != name {
				return invalid()
			}
			if err := ValidateProviderHeader(v, p, ids.ProcedureClassID); err != nil {
				return invalid()
			}
		}
		if p.Implementation == StockCitext && slot >= CitextRegexpProcedureStart && slot < CitextRegexpProcedureEnd {
			m := CitextRegexpMembers[slot-CitextRegexpProcedureStart]
			name, signature, result = m.Name, m.Signature(base.SQLType.ID()), m.Result
			if p.DeclarationBody != citextRegexpDeclarationBodies[slot-CitextRegexpProcedureStart] {
				return invalid()
			}
			if p.Installed != m.Declaration || p.Language != (ids.ObjectAddress{}) || p.Display != (catalog.RoutineDisplay{Language: catalog.SQLRoutineLanguage}) {
				return invalid()
			}
			if err := ValidateProviderHeader(v, p, ids.ProcedureClassID); err != nil {
				return invalid()
			}
		}
		if p.Implementation == StockCitext && slot >= CitextStringProcedureStart {
			m := CitextStringMembers[slot-CitextStringProcedureStart]
			name, signature, result = m.Name, m.Signature(base.SQLType.ID()), m.Result
			if p.DeclarationBody != citextStringDeclarationBodies[slot-CitextStringProcedureStart] {
				return invalid()
			}
			if p.Installed != m.Declaration() || p.Language != (ids.ObjectAddress{}) || p.Display != (catalog.RoutineDisplay{Language: catalog.SQLRoutineLanguage}) || p.SQLBody != nil || p.SQLDefaults != nil || len(p.Body) != 0 {
				return invalid()
			}
			if err := ValidateProviderHeader(v, p, ids.ProcedureClassID); err != nil {
				return invalid()
			}
		}
	case StockPgstattuple:
		count = len(PgstattupleMembers)
		if slot >= count {
			return invalid()
		}
		m := PgstattupleMembers[slot]
		name, signature, result, volatility = m.Name, m.Signature, m.Result, expr.Volatile
		if p.Installed != m.Declaration || p.Display != (catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: m.Source, Binary: "$libdir/pgstattuple", HasBinary: true}) || p.Language != (ids.ObjectAddress{}) || p.SQLBody != nil || p.SQLDefaults != nil || p.LiteralDefaults != nil || p.InputNames != nil || len(p.Body) != 0 {
			return invalid()
		}
	case StockPageinspect:
		count = len(PageinspectMembers)
		if slot >= count {
			return invalid()
		}
		m := PageinspectMembers[slot]
		name, signature, result, volatility = m.Name, m.Signature, m.Result, expr.Volatile
		strict = slot < 17
		display := catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: m.Source, Binary: "$libdir/pageinspect", HasBinary: true}
		var body sqltype.StructuralTree
		if slot >= 19 {
			display = catalog.RoutineDisplay{Language: catalog.SQLRoutineLanguage}
			body = pageinspectAttrsBodies[slot-19]
		}
		if p.Installed != m.Declaration || p.Display != display || p.DeclarationBody != body || p.Language != (ids.ObjectAddress{}) || p.SQLBody != nil || p.SQLDefaults != nil || p.LiteralDefaults != nil || p.InputNames != pageinspectSplitNames(slot) || len(p.Body) != 0 {
			return invalid()
		}
	case StockAmcheck:
		count = 1 + len(AmcheckMembers)
		if slot >= count {
			return invalid()
		}
		declaration := verifyHeapamDeclaration()
		name, signature, result, volatility, strict = "verify_heapam", VerifyHeapamSignature, 2249, expr.Volatile, false
		if slot > 0 {
			m := AmcheckMembers[slot-1]
			name, signature, result, strict = m.Name, m.Signature, 2278, true
			declaration = m.Declaration
		}
		expectedDefaults := (*catalog.RoutineLiteralDefaults)(nil)
		if slot == 0 {
			expectedDefaults = verifyHeapamDefaults
		}
		if p.LiteralDefaults != expectedDefaults {
			return invalid()
		}
		if p.Installed != declaration || p.Display != (catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: name, Binary: "$libdir/amcheck", HasBinary: true}) || p.Language != (ids.ObjectAddress{}) || p.SQLBody != nil || p.SQLDefaults != nil || len(p.Body) != 0 {
			return invalid()
		}
	case StockTablefunc:
		count = tablefuncMemberCount
		if len(ext.Members) != count || slot < 0 || slot >= count {
			return invalid()
		}
		declaration, source := tablefuncDeclaration(), "crosstab"
		if slot < tablefuncCrosstabStart {
			member := tablefuncMembers[slot]
			name, signature, result, volatility = member.name, member.signature, member.result, member.volatility
			declaration, source = tablefuncNormalDeclaration(), member.source
		} else {
			recipe := catalog.Extension{Members: ext.Members}
			name, signature, result = tablefuncProcedure(slot, &recipe)
			if name == "" {
				return invalid()
			}
			volatility = expr.Stable
			if slot >= tablefuncCrosstabStart+7 && slot <= tablefuncCrosstabStart+9 {
				typ, ok := v.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: result})
				if !ok || !catalog.SameInstallation(typ, ext) {
					return invalid()
				}
				if err := ValidateTypeOrigin(v, typ); err != nil {
					return "", err
				}
			}
		}
		if p.Installed != declaration || p.Display != (catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: source, Binary: "$libdir/tablefunc", HasBinary: true}) || p.Language != (ids.ObjectAddress{}) || p.SQLBody != nil || p.SQLDefaults != nil || p.LiteralDefaults != nil || p.InputNames != nil || len(p.Body) != 0 {
			return invalid()
		}
	case StockTcn:
		count = 1
		name, result, volatility, strict = "triggered_change_notification", 2279, expr.Volatile, false
		if ext.Version != "1.0" || p.Installed != (catalog.InstalledRoutineDeclaration{Parallel: 'u'}) || p.Display != (catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: name, Binary: "$libdir/tcn", HasBinary: true}) {
			return invalid()
		}
	case StockRefint:
		count = len(refintSymbols)
		if slot >= count {
			return invalid()
		}
		name, result, volatility, strict = refintSymbols[slot], 2279, expr.Volatile, false
		if p.Installed != (catalog.InstalledRoutineDeclaration{Parallel: 'u'}) || p.Display != (catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: name, Binary: "$libdir/refint", HasBinary: true}) {
			return invalid()
		}
	case StockAutoinc:
		count = 1
		name, result, volatility, strict = "autoinc", 2279, expr.Volatile, false
		if ext.Version != "1.0" || slot != 0 || p.Installed != (catalog.InstalledRoutineDeclaration{Parallel: 'u'}) || p.Display != (catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: "autoinc", Binary: "$libdir/autoinc", HasBinary: true}) || p.Language != (ids.ObjectAddress{}) || p.SQLBody != nil || p.SQLDefaults != nil || p.LiteralDefaults != nil || p.InputNames != nil || len(p.Body) != 0 {
			return invalid()
		}
	case StockInsertUsername:
		count = 1
		name, result, volatility, strict = "insert_username", 2279, expr.Volatile, false
		if ext.Version != "1.0" || !p.Configuration.Valid() || p.Installed != (catalog.InstalledRoutineDeclaration{Parallel: 'u'}) || p.Display != (catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: name, Binary: "$libdir/insert_username", HasBinary: true}) || p.Language != (ids.ObjectAddress{}) {
			return invalid()
		}
	case StockModdatetime:
		count = 1
		name, signature, result, volatility, strict = "moddatetime", "", 2279, expr.Volatile, false
		if p.Installed != (catalog.InstalledRoutineDeclaration{Parallel: 'u'}) || p.Display != (catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: "moddatetime", Binary: "$libdir/moddatetime", HasBinary: true}) {
			return invalid()
		}
	case StockLo:
		count = 3
		name, signature, result, volatility, strict = "lo_manage", "", 2279, expr.Volatile, false
		if ext.Version != "1.2" || slot != 2 || p.Installed != (catalog.InstalledRoutineDeclaration{Parallel: 'u'}) || p.Display != (catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: name, Binary: "$libdir/lo", HasBinary: true}) {
			return invalid()
		}
	case StockPgrowlocks:
		count = 1
		name, signature, result, volatility = "pgrowlocks", "25", 2249, expr.Volatile
		if p.Installed != pgrowlocksDeclaration() || p.Display != (catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: "pgrowlocks", Binary: "$libdir/pgrowlocks", HasBinary: true}) || p.Language != (ids.ObjectAddress{}) || p.SQLBody != nil || p.SQLDefaults != nil || len(p.Body) != 0 {
			return invalid()
		}
	case StockUnaccent:
		count = 6
		if len(ext.Members) != count || slot > 3 {
			return invalid()
		}
		name = "unaccent"
		signature = []catalog.ProcedureSignature{catalog.UnaccentDictionarySignature, catalog.UnaccentTextSignature, catalog.UnaccentInitSignature, catalog.UnaccentLexizeSignature}[slot]
		result, volatility = 25, expr.Stable
		if slot >= 2 {
			result, volatility, strict = 2281, expr.Volatile, false
			if slot == 2 {
				name = "unaccent_init"
			} else {
				name = "unaccent_lexize"
			}
		}
		if slot < 2 && p.Dictionary != ext.Members[5] {
			return invalid()
		}
	default:
		return invalid()
	}
	if len(ext.Members) != count || p.Signature != signature || p.Result != result || p.RoutineKind != kind || p.Volatility != volatility || p.Strict != strict || kind != catalog.AggregateRoutine && p.Aggregate != nil || p.Implementation != StockUnaccent && p.Dictionary != (ids.ObjectAddress{}) {
		return invalid()
	}
	if installationName && sqlName != "" {
		return sqlName, nil
	}
	return name, nil
}

// A supplied manifest may be a detached COW copy, but must describe the exact
// current installation and ordered inventory in the view being validated.
func currentManifest(v catalog.Read, ext *catalog.Extension) bool {
	if ext == nil {
		return false
	}
	current, ok := v.Extension(ext.Address())
	if !ok || current.Name() != ext.Name() || current.Version != ext.Version || current.Revision() != ext.Revision() || current.TargetNamespace != ext.TargetNamespace || current.Owner != ext.Owner || len(current.Members) != len(ext.Members) {
		return false
	}
	for i, a := range ext.Members {
		if a != current.Members[i] {
			return false
		}
		for _, previous := range ext.Members[:i] {
			if previous == a {
				return false
			}
		}
	}
	return true
}
