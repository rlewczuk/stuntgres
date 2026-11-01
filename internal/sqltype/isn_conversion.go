package sqltype

import "fmt"

// IsISN reports the scalar datum family, not installed catalog authority.
func (d *Descriptor) IsISN() bool { return isnRole(d) >= 0 }

func isnRole(d *Descriptor) int {
	switch {
	case d.IsEan13():
		return 0
	case d.IsIsbn():
		return 1
	case d.IsIsbn13():
		return 2
	case d.IsIsmn():
		return 3
	case d.IsIsmn13():
		return 4
	case d.IsIssn():
		return 5
	case d.IsIssn13():
		return 6
	case d.IsUpc():
		return 7
	}
	return -1
}

func isnPair(a, b int) bool {
	return a >= 0 && b >= 0 && (a == b || a == 0 || b == 0 || a > 0 && a < 7 && (a-1)/2 == (b-1)/2)
}

func validIsnPayload(d *Descriptor, packed int64) bool {
	switch isnRole(d) {
	case 0:
		return packed >= 0 && packed <= 19999999999999
	case 1, 2:
		return validIsbnNumber(packed)
	case 3, 4:
		return validIsmnNumber(packed)
	case 5, 6:
		return validIssnNumber(packed)
	case 7:
		return validUpcNumber(packed)
	}
	return false
}

// applyISN copies the complete corrected identifier and independent check flag.
// Only an incompatible narrowing formats digits, solely for its diagnostic.
func (p CoercionPlan) applyISN(v Value) (Value, error) {
	if !v.ValidResult(p.source) || !validIsnPayload(p.source.Type, v.integer) {
		return Value{}, fmt.Errorf("invalid ISN conversion source")
	}
	if !validIsnPayload(p.target.Type, v.integer) {
		var digits [13]byte
		n := v.integer >> 1
		for i := 12; i >= 0; i-- {
			digits[i] = byte(n%10) + '0'
			n /= 10
		}
		family := isnNumberFamily(digits[:])
		if digits[0] == '0' {
			family = "UPC"
		}
		if family != "EAN13" {
			family = "EAN13(" + family + ")"
		}
		target := "UPC"
		switch isnRole(p.target.Type) {
		case 1, 2:
			target = "ISBN"
		case 3, 4:
			target = "ISMN"
		case 5, 6:
			target = "ISSN"
		}
		return Value{}, failure("22P02", fmt.Sprintf("cannot cast %s to %s for number: \"%s\"", family, target, digits[:]))
	}
	return Value{typ: p.target.Type, integer: v.integer}, nil
}
