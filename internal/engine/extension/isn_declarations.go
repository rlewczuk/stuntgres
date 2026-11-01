package extension

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const (
	IsnComparisonStart = 24
	IsnOperatorStart   = 192
	IsnConversionStart = 360
	IsnSupportStart    = 367
	IsnFamilySlot      = 375
	IsnClassStart      = 376
	IsnCastStart       = 384
	IsnValidityStart   = 404
	IsnRepairStart     = 412
	IsnWeakGetterSlot  = 420
	IsnWeakSetterSlot  = 421
	IsnMemberCount     = 422
)

var isnPairs = [...][2]int{
	{0, 0}, {1, 1}, {2, 2}, {3, 3}, {4, 4}, {5, 5}, {6, 6}, {7, 7},
	{0, 1}, {1, 0}, {0, 2}, {2, 0}, {0, 3}, {3, 0}, {0, 4}, {4, 0},
	{0, 5}, {5, 0}, {0, 6}, {6, 0}, {0, 7}, {7, 0},
	{1, 2}, {2, 1}, {3, 4}, {4, 3}, {5, 6}, {6, 5},
}
var isnComparisonNames = [...]string{"isneq", "isnne", "isnlt", "isnle", "isngt", "isnge"}
var isnComparisonBodies = [...]string{"int8eq", "int8ne", "int8lt", "int8le", "int8gt", "int8ge"}
var isnCastPairs = [...][2]int{
	{0, 1}, {0, 2}, {0, 3}, {0, 4}, {0, 5}, {0, 6}, {0, 7},
	{1, 0}, {2, 0}, {3, 0}, {4, 0}, {5, 0}, {6, 0}, {7, 0},
	{1, 2}, {2, 1}, {3, 4}, {4, 3}, {5, 6}, {6, 5},
}

func isnMemberClass(slot int) ids.OID {
	switch {
	case slot < 0 || slot >= IsnMemberCount:
		return 0
	case slot < IsnComparisonStart:
		if slot%3 == 0 {
			return ids.TypeClassID
		}
		return ids.ProcedureClassID
	case slot < IsnOperatorStart:
		return ids.ProcedureClassID
	case slot < IsnConversionStart:
		return ids.OperatorClassID
	case slot < IsnFamilySlot:
		return ids.ProcedureClassID
	case slot == IsnFamilySlot:
		return ids.OperatorFamilyClassID
	case slot < IsnCastStart:
		return ids.OperatorClassCatalogID
	case slot < IsnValidityStart:
		return ids.CastClassID
	default:
		return ids.ProcedureClassID
	}
}

func isnPairIndex(left, right int) int {
	for i, pair := range isnPairs {
		if pair == [2]int{left, right} {
			return i
		}
	}
	return -1
}

// All authority uses released scalar roles, never mutable public spellings.
func isnTypeRole(v catalog.Read, t *catalog.Type, installation *catalog.Installation) (int, error) {
	if err := ValidateTypeOrigin(v, t); err != nil {
		return -1, err
	}
	if installation == nil || installation.Implementation != StockIsn || !catalog.SameInstallation(t, installation) {
		return -1, InvalidProviderDescriptor()
	}
	for i, r := range isnRecipes {
		if t.Origin.Declaration == r.slot && r.accept(t.SQLType) {
			return i, nil
		}
	}
	return -1, InvalidProviderDescriptor()
}

func isnRoutineDeclaration(v catalog.Read, members []ids.ObjectAddress, slot int) (name, symbol string, signature catalog.ProcedureSignature, result ids.OID, display catalog.RoutineDisplay, err error) {
	err = InvalidProviderDescriptor()
	if len(members) != IsnMemberCount || isnMemberClass(slot) != ids.ProcedureClassID {
		return
	}
	typeID := func(role int) (sqltype.ID, bool) {
		t, ok := v.Type(members[isnRecipes[role].slot])
		return sqltype.ID(members[isnRecipes[role].slot].ObjectID), ok && isnRecipes[role].accept(t.SQLType)
	}
	if slot < IsnComparisonStart {
		r := isnRecipes[slot/3]
		id, ok := typeID(slot / 3)
		if !ok {
			return
		}
		name, symbol, signature, result = r.input, r.inputSource, "2275", ids.OID(id)
		if slot%3 == 2 {
			name, symbol, signature, result = r.output, r.output, IsnSignature(id), 2275
		}
	} else if slot < IsnOperatorStart {
		index := slot - IsnComparisonStart
		pair := isnPairs[index/6]
		a, ok := typeID(pair[0])
		if !ok {
			return
		}
		b, ok := typeID(pair[1])
		if !ok {
			return
		}
		name, symbol, signature, result = isnComparisonNames[index%6], isnComparisonBodies[index%6], catalog.ProcedureSignature(fmt.Sprintf("%d,%d", a, b)), 16
	} else if slot >= IsnConversionStart && slot < IsnSupportStart {
		role := slot - IsnConversionStart + 1
		a, ok := typeID(0)
		if !ok {
			return
		}
		b, ok := typeID(role)
		if !ok {
			return
		}
		family := role
		if role == 2 || role == 4 || role == 6 {
			family--
		}
		name, symbol, signature, result = isnRecipes[role].name, isnRecipes[family].name+"_cast_from_ean13", IsnSignature(a), ids.OID(b)
	} else if slot >= IsnSupportStart && slot < IsnFamilySlot {
		role := slot - IsnSupportStart
		a, ok := typeID(role)
		if !ok {
			return
		}
		name, symbol, signature, result = "bt"+isnRecipes[role].name+"cmp", "btint8cmp", catalog.ProcedureSignature(fmt.Sprintf("%d,%d", a, a)), 23
	} else if slot >= IsnValidityStart && slot < IsnWeakGetterSlot {
		role := (slot - IsnValidityStart) % len(isnRecipes)
		a, ok := typeID(role)
		if !ok {
			return
		}
		name, symbol, signature, result = "is_valid", "is_valid", IsnSignature(a), 16
		if slot >= IsnRepairStart {
			name, symbol, result = "make_valid", "make_valid", ids.OID(a)
		}
	} else if slot == IsnWeakGetterSlot {
		name, symbol, signature, result = "isn_weak", "weak_input_status", "", 16
	} else if slot == IsnWeakSetterSlot {
		name, symbol, signature, result = "isn_weak", "accept_weak_input", "16", 16
	} else {
		return
	}
	display = catalog.RoutineDisplay{Language: catalog.InternalRoutineLanguage, Source: symbol}
	if slot < IsnComparisonStart || slot >= IsnConversionStart && slot < IsnSupportStart || slot >= IsnValidityStart {
		display = catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: symbol, Binary: "$libdir/isn", HasBinary: true}
	}
	err = nil
	return
}

// IsnComparisonOperation discriminates actual comparison declarations from codecs.
func IsnComparisonOperation(v catalog.Read, p *catalog.Procedure) (sqltype.ComparisonOperator, bool) {
	if p == nil || p.Implementation != StockIsn || ValidateProcedureOrigin(v, p, StockIsn) != nil {
		return 0, false
	}
	slot := p.Origin.Declaration
	if slot < IsnComparisonStart || slot >= IsnOperatorStart {
		return 0, false
	}
	return sqltype.ComparisonOperator((slot - IsnComparisonStart) % 6), true
}
func IsnComparisonName(symbol string) bool {
	for _, s := range isnComparisonBodies {
		if s == symbol {
			return true
		}
	}
	return false
}

func validateIsnRoutineTypes(v catalog.Read, p *catalog.Procedure) error {
	installation := p.Origin.Installation
	slot := p.Origin.Declaration
	var roles [2]int
	count := 1
	switch {
	case slot < IsnComparisonStart:
		roles[0] = slot / 3
	case slot < IsnOperatorStart:
		roles = isnPairs[(slot-IsnComparisonStart)/6]
		count = 2
	case slot >= IsnConversionStart && slot < IsnSupportStart:
		roles = [2]int{0, slot - IsnConversionStart + 1}
		count = 2
	case slot >= IsnSupportStart && slot < IsnFamilySlot:
		roles[0] = slot - IsnSupportStart
	case slot >= IsnValidityStart && slot < IsnWeakGetterSlot:
		roles[0] = (slot - IsnValidityStart) % len(isnRecipes)
	case slot == IsnWeakGetterSlot || slot == IsnWeakSetterSlot:
		t, ok := v.LookupType(sqltype.Bool)
		if !ok || t == nil || t.ID() != sqltype.Bool {
			return InvalidProviderDescriptor()
		}
		count = 0
	default:
		return InvalidProviderDescriptor()
	}
	for _, role := range roles[:count] {
		t, ok := v.Type(installation.Members[isnRecipes[role].slot])
		if !ok {
			return InvalidProviderDescriptor()
		}
		if _, err := isnTypeRole(v, t, installation); err != nil {
			return err
		}
	}
	return nil
}

// IsnRoutineProperties is the final released declaration metadata, including
// the 1.3 weak getter/setter overrides. Callers first authenticate the slot.
func IsnRoutineProperties(slot int) (expr.Volatility, byte) {
	if slot == IsnWeakGetterSlot {
		return expr.Stable, 's'
	}
	if slot == IsnWeakSetterSlot {
		return expr.Volatile, 'u'
	}
	return expr.Immutable, 's'
}

func IsnValidationOperation(v catalog.Read, p *catalog.Procedure) (sqltype.ISNOperation, bool) {
	if p == nil || p.Implementation != StockIsn || ValidateProcedureOrigin(v, p, StockIsn) != nil {
		return 0, false
	}
	switch slot := p.Origin.Declaration; {
	case slot >= IsnValidityStart && slot < IsnRepairStart:
		return sqltype.ISNIsValid, true
	case slot >= IsnRepairStart && slot < IsnWeakGetterSlot:
		return sqltype.ISNMakeValid, true
	case slot == IsnWeakGetterSlot:
		return sqltype.ISNWeakGet, true
	case slot == IsnWeakSetterSlot:
		return sqltype.ISNWeakSet, true
	default:
		return 0, false
	}
}
