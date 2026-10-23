package sqltype

import "fmt"

// Ean13 owns a packed numeric identifier and its invalid-input flag. Catalog
// identity grants neither sibling casts nor comparison/array capabilities.
func Ean13(id ID, name, declaration string) (*Descriptor, error) {
	if id == 0 || id == Unknown || name == "" || declaration == "" {
		return nil, fmt.Errorf("invalid ean13 descriptor")
	}
	return &Descriptor{id: id, name: name, sql: declaration, database: "EAN13", ean13: true}, nil
}

func (d *Descriptor) IsEan13() bool { return d != nil && d.ean13 }

// RemapEan13 requires source-pointer authorization from the catalog importer.
func (v Value) RemapEan13(target *Descriptor) (Value, error) {
	if !v.typ.IsEan13() || !target.IsEan13() || !v.null && !v.validEan13() {
		return Value{}, fmt.Errorf("incompatible imported ean13")
	}
	v.typ = target
	return v, nil
}

func (v Value) validEan13() bool {
	return v.typ != nil && v.typ.BaseType().IsEan13() && v.integer >= 0 && v.integer <= 19999999999999
}

// Recognition order follows REL_18_6 string2ean, including short-family errors
// before checksum validation and the raw-character lookahead for ? and !.
func (d *Descriptor) ean13Input(text string) (Value, error) {
	invalid := func() (Value, error) {
		return Value{}, failure("22P02", fmt.Sprintf("invalid input syntax for EAN13 number: \"%s\"", text))
	}
	var digits [14]byte
	n, family := 0, ""
	magic, valid := false, true
	for i := range len(text) {
		c := text[i]
		last := i+1 == len(text) || text[i+1] == '!'
		digit := c >= '0' && c <= '9'
		if c == '?' && last {
			magic, digit = true, true
		}
		switch {
		case n == 0 && (c == 'M' || c == 'm'):
			if family != "" { return invalid() }
			family, c = "ISMN", 'M'
		case n == 7 && (digit || c == 'X' || c == 'x') && last:
			if family != "" { return invalid() }
			family = "ISSN"
		case n == 9 && (digit || c == 'X' || c == 'x') && last:
			if family != "" && family != "ISMN" { return invalid() }
			if family == "" { family = "ISBN" }
		case n == 11 && digit && last:
			if family != "" { return invalid() }
			family = "UPC"
		case c == '-' || c == ' ':
			continue
		case c == '!' && i+1 == len(text):
			if !magic { valid = false }
			magic = true
			continue
		case !digit:
			return invalid()
		}
		digits[n] = c
		n++
		if n > 13 {
			return Value{}, failure("22003", fmt.Sprintf("value \"%s\" is out of range for EAN13 type", text))
		}
	}
	switch n {
	case 13:
		if family != "" { return invalid() }
	case 12:
		if family != "UPC" { return invalid() }
	case 10:
		if family != "ISBN" && family != "ISMN" { return invalid() }
	case 8:
		if family != "" && family != "ISSN" { return invalid() }
		family = "ISSN"
	default:
		return invalid()
	}
	if family != "" {
		return Value{}, failure("22P02", fmt.Sprintf("cannot cast %s to EAN13 for number: \"%s\"", family, text))
	}
	sum := 0
	var number int64
	for i, c := range digits[:12] {
		weight := 1
		if i%2 == 1 { weight = 3 }
		sum += int(c-'0') * weight
		number = number*10 + int64(c-'0')
	}
	check := byte((10-sum%10)%10) + '0'
	if !magic && digits[12] != check {
		return Value{}, failure("22P02", fmt.Sprintf("invalid check digit for EAN13 number: \"%s\", should be %c", text, check))
	}
	number = (number*10 + int64(check-'0')) << 1
	if !valid { number |= 1 }
	return Value{typ: d, integer: number}, nil
}

// compareEanPrefix compares only the table's significant digits, without
// normalizing strings or allocating a per-value range key.
func compareEanPrefix(digits []byte, pattern string) int {
	i := 0
	for j := range len(pattern) {
		if pattern[j] == '-' { continue }
		if digits[i] < pattern[j] { return -1 }
		if digits[i] > pattern[j] { return 1 }
		i++
	}
	return 0
}

func eanPattern(digits []byte, ranges []eanRange) string {
	low, high := 0, len(ranges)
	for low < high {
		mid := low + (high-low)/2
		r := ranges[mid]
		if compareEanPrefix(digits, r.low) < 0 {
			high = mid
		} else if compareEanPrefix(digits, r.high) > 0 {
			low = mid + 1
		} else {
			return r.low
		}
	}
	return ""
}

func (d *Descriptor) ean13Output(v Value) (string, error) {
	if !v.validEan13() { return "", fmt.Errorf("invalid ean13 payload") }
	var digits [13]byte
	number := v.integer >> 1
	for i := 12; i >= 0; i-- {
		digits[i] = byte(number%10) + '0'
		number /= 10
	}
	var separators [13]bool
	separators[12] = true
	if eanPattern(digits[:], eanEAN13_range[:]) != "" {
		separators[3] = true
		var ranges []eanRange
		switch string(digits[:3]) {
		case "978": ranges = eanISBN_range[:]
		case "977": ranges = eanISSN_range[:]
		case "979":
			if digits[3] == '0' { ranges = eanISMN_range[:] } else { ranges = eanISBN_range_new[:] }
		}
		pattern := eanPattern(digits[3:], ranges)
		pos := 3
		for i := range len(pattern) {
			if pattern[i] == '-' { separators[pos] = true } else { pos++ }
		}
		if pattern != "" { separators[pos] = true }
	}
	var out [19]byte
	n := 0
	for i, c := range digits {
		if separators[i] { out[n] = '-'; n++ }
		out[n] = c
		n++
	}
	if v.integer&1 != 0 { out[n] = '!'; n++ }
	return string(out[:n]), nil
}
