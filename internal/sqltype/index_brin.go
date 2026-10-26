package sqltype

import (
	"context"
	"fmt"
	"strings"
)

// BrinComparison captures datum ordering, not a B-tree support function. BRIN's
// four selected summary operations remain separate in the captured class.
type BrinComparison struct {
	comparison Comparison
	input      ID
}

func BrinMinmaxIdentity(c IndexClass) bool {
	if !c.IsBrin() {
		return false
	}
	for _, f := range brinIndexClasses {
		if f.OID == c.OID() && f.Family == c.FamilyOID() && f.Input == c.InputOID() && strings.HasSuffix(f.Name, "_minmax_ops") {
			return true
		}
	}
	return false
}

func BindBrinComparison(u Usage, c IndexClass) (BrinComparison, error) {
	if !BrinMinmaxIdentity(c) {
		return BrinComparison{}, failure("0A000", "index operator class is unsupported")
	}
	if !c.Accepts(u.Type) || !u.validCollation() {
		return BrinComparison{}, fmt.Errorf("invalid BRIN minmax input")
	}
	_, orderable := ComparisonCapabilities(u.Type)
	if !orderable {
		return BrinComparison{}, failure("0A000", "index operator class is unsupported")
	}
	for slot := 1; slot <= 4; slot++ {
		s := c.Support(slot)
		if s.Member == 0 || s.Family != c.FamilyOID() || s.Left != c.InputOID() || s.Right != c.InputOID() || s.Routine != uint32(3382+slot) || s.Revision == 0 || s.FamilyRevision == 0 || s.RoutineRevision == 0 {
			return BrinComparison{}, fmt.Errorf("invalid captured BRIN summary support")
		}
	}
	p, err := BindComparison(u.Type, u.Type, u.Collation, false)
	return BrinComparison{comparison: p, input: c.input}, err
}

func (p BrinComparison) Compare(ctx context.Context, a, b Value, input InputEnvironment) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if a.typ != p.comparison.left || b.typ != p.comparison.right || a.null || b.null {
		return 0, fmt.Errorf("invalid BRIN comparison values")
	}
	if p.input == Oid && a.typ.BaseType().id == Int4 {
		av, bv := uint32(a.integer), uint32(b.integer)
		if av < bv {
			return -1, nil
		}
		if av > bv {
			return 1, nil
		}
		return 0, nil
	}
	if p.input == BPChar {
		return p.comparison.inputCollation.CompareText(ctx, strings.TrimRight(a.text, " "), strings.TrimRight(b.text, " "))
	}
	return p.comparison.Compare(ctx, a, b, input)
}
