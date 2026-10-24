package sqltype

import "fmt"

// Ismn owns an ISMN-family packed identifier and its independent invalid flag.
func Ismn(id ID, name, declaration string) (*Descriptor, error) {
	if id == 0 || id == Unknown || name == "" || declaration == "" {
		return nil, fmt.Errorf("invalid ismn descriptor")
	}
	return &Descriptor{id: id, name: name, sql: declaration, database: "ISMN", ismn: true}, nil
}

func (d *Descriptor) IsIsmn() bool { return d != nil && d.ismn }

func (v Value) validIsmn() bool {
	if v.typ == nil || !v.typ.BaseType().IsIsmn() || v.integer < 0 {
		return false
	}
	n := v.integer >> 1
	if n < 9790000000000 || n > 9790999999999 {
		return false
	}
	check := n % 10
	n /= 10
	sum := int64(0)
	for i := range 12 {
		weight := int64(3)
		if i%2 == 1 { weight = 1 }
		sum += n%10 * weight
		n /= 10
	}
	return check == (10-sum%10)%10
}

// RemapIsmn requires source-pointer authorization from the catalog importer.
func (v Value) RemapIsmn(target *Descriptor) (Value, error) {
	if !v.typ.IsIsmn() || !target.IsIsmn() || !v.null && !v.validIsmn() {
		return Value{}, fmt.Errorf("incompatible imported ismn")
	}
	v.typ = target
	return v, nil
}

func (d *Descriptor) ismnInput(text string) (Value, error) {
	var digits [14]byte
	n, family, magic, valid, err := scanIsn(text, "ISMN", &digits)
	if err != nil { return Value{}, err }
	if n == 13 {
		family = "EAN13"
		if digits[0] == '0' { family = "UPC" }
		if string(digits[:3]) == "977" { family = "ISSN" }
		if string(digits[:3]) == "978" || string(digits[:3]) == "979" { family = "ISBN" }
		if string(digits[:4]) == "9790" { family = "ISMN" }
	}
	if family != "ISMN" {
		return Value{}, failure("22P02", fmt.Sprintf("cannot cast %s to ISMN for number: \"%s\"", family, text))
	}
	if n == 10 {
		copy(digits[4:13], digits[1:10])
		copy(digits[:4], "9790")
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
		return Value{}, failure("22P02", fmt.Sprintf("invalid check digit for ISMN number: \"%s\", should be %c", text, check))
	}
	number = (number*10 + int64(check-'0')) << 1
	if !valid { number |= 1 }
	return Value{typ: d, integer: number}, nil
}

func (d *Descriptor) ismnOutput(v Value) (string, error) {
	if !v.validIsmn() { return "", fmt.Errorf("invalid ismn payload") }
	var digits [13]byte
	number := v.integer >> 1
	for i := 12; i >= 0; i-- {
		digits[i] = byte(number%10) + '0'
		number /= 10
	}
	var separators [13]bool
	separators[4], separators[12] = true, true
	pattern := eanPattern(digits[3:], eanISMN_range[:])
	pos := 3
	for i := range len(pattern) {
		if pattern[i] == '-' { separators[pos] = true } else { pos++ }
	}
	if pattern != "" { separators[pos] = true }
	var out [14]byte
	out[0] = 'M'
	n := 1
	for i := 4; i < 13; i++ {
		if separators[i] { out[n] = '-'; n++ }
		out[n] = digits[i]
		n++
	}
	if v.integer&1 != 0 { out[n] = '!'; n++ }
	return string(out[:n]), nil
}
