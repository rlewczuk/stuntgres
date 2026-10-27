package extension

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// ProcedureSymbol returns the executable installation symbol, never a mutable
// public label. Membership order is the versioned manifest, preserved by remap.
// This low-level authority deliberately does not call provider validators.
func ProcedureSymbol(v catalog.Read, p *catalog.Procedure) (string, error) {
	return procedureSymbol(v, p, false)
}

// allowDetached is reserved for incumbent provider metadata validation.
// It never grants installed execution or portable replay authority.
func procedureSymbol(v catalog.Read, p *catalog.Procedure, allowDetached bool) (string, error) {
	invalid := func() (string, error) { return "", fmt.Errorf("stuntgres: invalid installed procedure identity") }
	if p == nil || v.Owner() == nil || v.Get(p.Address()) != p || p.Address().ClassID != ids.ProcedureClassID || p.Address().ObjectID == 0 || p.Address().SubID != 0 || p.Revision() == 0 || p.Owner.ID() == 0 || p.Owner != v.Owner().Principals.Principal(p.Owner.ID()) || p.SQL != nil || p.Builtin != nil || p.Foreign != nil || p.Conversion.Name() != "" || v.NameAddress(catalog.DescriptorNameKey(p)) != p.Address() {
		return invalid()
	}
	if p.Installed.Outputs != nil && p.Implementation != StockPgrowlocks && p.Implementation != StockAmcheck && p.Implementation != StockPageinspect {
		return invalid()
	}
	if _, ok := v.Namespace(p.Namespace()); !ok {
		return invalid()
	}
	// Detached unaccent declarations are an incumbent script-provider capability.
	// Its four exact signatures distinguish every released symbol without labels.
	if p.Extension == (ids.ObjectAddress{}) {
		if !allowDetached {
			return invalid()
		}
		if p.Implementation != StockUnaccent || p.RoutineKind != catalog.FunctionRoutine || p.Aggregate != nil || p.Dictionary != (ids.ObjectAddress{}) {
			return invalid()
		}
		switch p.Signature {
		case catalog.UnaccentTextSignature, catalog.UnaccentDictionarySignature:
			if p.Result == 25 && p.Volatility == expr.Stable && p.Strict {
				return "unaccent", nil
			}
		case catalog.UnaccentInitSignature:
			if p.Result == 2281 && p.Volatility == expr.Volatile && !p.Strict {
				return "unaccent_init", nil
			}
		case catalog.UnaccentLexizeSignature:
			if p.Result == 2281 && p.Volatility == expr.Volatile && !p.Strict {
				return "unaccent_lexize", nil
			}
		}
		return invalid()
	}
	ext, ok := v.Extension(p.Extension)
	if !ok || ext.Address().ClassID != ids.ExtensionClassID || ext.Address().SubID != 0 || ext.Address().ObjectID == 0 || ext.Revision() == 0 || v.NameAddress(catalog.DescriptorNameKey(ext)) != ext.Address() {
		return invalid()
	}
	if ext.Owner.ID() == 0 || ext.Owner != v.Owner().Principals.Principal(ext.Owner.ID()) {
		return invalid()
	}
	if _, ok := v.Namespace(ext.TargetNamespace); !ok {
		return invalid()
	}
	implementation, ok := v.Owner().Extensions[ext.Name()]
	if !ok || implementation != p.Implementation || implementation.Versions().InstallPath(ext.Version) == nil {
		return invalid()
	}
	slot := -1
	for i, a := range ext.Members {
		for _, b := range ext.Members[:i] {
			if a == b {
				return invalid()
			}
		}
		if a == p.Address() {
			slot = i
		}
	}
	if slot < 0 {
		return invalid()
	}
	name, signature, result := "", catalog.ProcedureSignature(""), ids.OID(0)
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
	case StockEarthdistance:
		count = EarthMemberCount
		if ext.Version != "1.2" || slot < 2 || slot >= EarthPointOperatorSlot {
			return invalid()
		}
		base, ok := v.Type(ext.Members[0])
		if !ok || base.SQLType == nil || !base.SQLType.IsDomain() || !base.SQLType.DomainBase().Type.IsCube() || base.Extension != ext.Address() {
			return invalid()
		}
		if err := ValidateProviderHeader(v, base, ids.TypeClassID); err != nil {
			return invalid()
		}
		if slot == EarthPointProcedureSlot {
			name, signature, result = "geo_distance", "600,600", ids.OID(sqltype.Float8)
		} else {
			op := sqltype.EarthOperation(slot - 2)
			name, signature, result, strict = op.Name(), earthRoutineSignature(op, base.SQLType.ID()), earthRoutineResult(op, base.SQLType.ID()), op != sqltype.EarthRadiusOperation
		}
	case StockCube:
		count = CubeMemberCount
		if ext.Version != StockCube.control.Version || slot < 1 || slot >= cubeSpatialOperatorStart {
			return invalid()
		}
		base, ok := v.Type(ext.Members[0])
		if !ok || base.SQLType == nil || !base.SQLType.IsCube() || base.Address() != ext.Members[0] || base.Extension != ext.Address() || base.Namespace() == (ids.ObjectAddress{}) || base.Revision() == 0 {
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
			if !ok || !recipe.accept(base.SQLType) || base.Extension != ext.Address() || base.Revision() == 0 {
				return invalid()
			}
			if err := ValidateProviderHeader(v, base, ids.TypeClassID); err != nil {
				return invalid()
			}
			name, signature, result = recipe.input, "2275", ids.OID(base.SQLType.ID())
			if slot == recipe.slot+2 {
				name, signature, result = recipe.output, IsnSignature(base.SQLType.ID()), 2275
			}
			found = true
		}
		if !found {
			return invalid()
		}
	case StockLtree:
		count = LtreeMemberCount
		if len(ext.Members) != count || slot < 0 || slot >= count || slot%3 == 0 {
			return invalid()
		}
		recipe := ltreeRecipes[slot/3]
		base, ok := v.Type(ext.Members[recipe.slot])
		if !ok || !recipe.accept(base.SQLType) || base.Extension != ext.Address() || base.Namespace() == (ids.ObjectAddress{}) || base.Revision() == 0 {
			return invalid()
		}
		if err := ValidateProviderHeader(v, base, ids.TypeClassID); err != nil {
			return invalid()
		}
		name, signature, result = recipe.input, "2275", ids.OID(base.SQLType.ID())
		if slot%3 == 2 {
			name, signature, result = recipe.output, LquerySignature(base.SQLType.ID()), 2275
		}
		if p.Display != (catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: name}) || p.Installed != (catalog.InstalledRoutineDeclaration{}) || p.Language != (ids.ObjectAddress{}) || p.SQLBody != nil || p.SQLDefaults != nil || len(p.Body) != 0 {
			return invalid()
		}
	case StockHstore, StockIntarray, StockSeg, StockCitext:
		count = 3
		if p.Implementation == StockCitext {
			count = CitextMemberCount
		}
		if slot < 1 || slot >= count || p.Implementation == StockCitext && CitextOperatorSlot(slot) {
			return invalid()
		}
		base, ok := v.Type(ext.Members[0])
		if !ok || base.SQLType == nil {
			return invalid()
		}
		if base.Address() != ext.Members[0] || base.Extension != ext.Address() || base.Namespace() == (ids.ObjectAddress{}) || base.Revision() == 0 || p.Implementation == StockHstore && !base.SQLType.IsHstore() || p.Implementation == StockIntarray && !base.SQLType.IsQueryInt() || p.Implementation == StockSeg && !base.SQLType.IsSeg() || p.Implementation == StockCitext && !base.SQLType.IsCitext() {
			return invalid()
		}
		if err := ValidateProviderHeader(v, base, ids.TypeClassID); err != nil {
			return invalid()
		}
		signature, result = "2275", ids.OID(base.SQLType.ID())
		switch p.Implementation {
		case StockHstore:
			name = "hstore_in"
			if slot == 2 {
				name = "hstore_out"
			}
			if p.Display != (catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: name}) || p.Installed != (catalog.InstalledRoutineDeclaration{}) || p.Language != (ids.ObjectAddress{}) || p.SQLBody != nil || p.SQLDefaults != nil || len(p.Body) != 0 {
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
			if p.Display != (catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: name}) || p.Installed != (catalog.InstalledRoutineDeclaration{}) || p.Language != (ids.ObjectAddress{}) || p.SQLBody != nil || p.SQLDefaults != nil || len(p.Body) != 0 {
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
			if p.Installed != m.Declaration() || p.Language != (ids.ObjectAddress{}) || p.Display != (catalog.RoutineDisplay{Language: catalog.SQLRoutineLanguage}) || p.SQLBody != nil || p.SQLDefaults != nil || len(p.Body) != 0 {
				return invalid()
			}
			if err := ValidateProviderHeader(v, p, ids.ProcedureClassID); err != nil {
				return invalid()
			}
		}
	case StockPageinspect:
		count = len(PageinspectMembers)
		if slot >= count { return invalid() }
		m := PageinspectMembers[slot]
		name, signature, result, volatility = m.Name, m.Signature, m.Result, expr.Volatile
		if p.Installed != m.Declaration || p.Display != (catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: name}) || p.Language != (ids.ObjectAddress{}) || p.SQLBody != nil || p.SQLDefaults != nil || p.LiteralDefaults != nil || p.InputNames != nil || len(p.Body) != 0 { return invalid() }
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
		if p.Installed != declaration || p.Display != (catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: name}) || p.Language != (ids.ObjectAddress{}) || p.SQLBody != nil || p.SQLDefaults != nil || len(p.Body) != 0 {
			return invalid()
		}
	case StockTablefunc:
		count = 1
		name, signature, result, volatility = "normal_rand", "23,701,701", 701, expr.Volatile
		if p.Installed != tablefuncNormalDeclaration() || p.Display != (catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: "normal_rand"}) || p.Language != (ids.ObjectAddress{}) || p.SQLBody != nil || p.SQLDefaults != nil || p.LiteralDefaults != nil || p.InputNames != nil || len(p.Body) != 0 {
			return invalid()
		}
	case StockPgrowlocks:
		count = 1
		name, signature, result, volatility = "pgrowlocks", "25", 2249, expr.Volatile
		if p.Installed != pgrowlocksDeclaration() || p.Display != (catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: "pgrowlocks"}) || p.Language != (ids.ObjectAddress{}) || p.SQLBody != nil || p.SQLDefaults != nil || len(p.Body) != 0 {
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
