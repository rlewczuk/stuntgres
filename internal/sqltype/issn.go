package sqltype

import "fmt"

// Issn owns a complete serial identifier, including issue digits and validity.
func Issn(id ID, name, declaration string) (*Descriptor, error) {
	if id == 0 || id == Unknown || name == "" || declaration == "" {
		return nil, fmt.Errorf("invalid issn descriptor")
	}
	return &Descriptor{id: id, name: name, sql: declaration, database: "ISSN", issn: true}, nil
}

func (d *Descriptor) IsIssn() bool { return d != nil && d.issn }

func (v Value) validIssn() bool {
	return v.typ != nil && v.typ.BaseType().IsIssn() && validIssnNumber(v.integer)
}

func validIssnNumber(packed int64) bool {
	if packed < 0 { return false }
	n := packed >> 1
	if n < 9770000000000 || n > 9779999999999 {
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

// RemapIssn requires source-pointer authorization from the catalog importer.
func (v Value) RemapIssn(target *Descriptor) (Value, error) {
	if !v.typ.IsIssn() || !target.IsIssn() || !v.null && !v.validIssn() {
		return Value{}, fmt.Errorf("incompatible imported issn")
	}
	v.typ = target
	return v, nil
}

func issnCheck(digits []byte) byte {
	sum := 0
	for i, c := range digits[:7] { sum += int(c-'0') * (8-i) }
	check := (11-sum%11)%11
	if check == 10 { return 'X' }
	return byte(check) + '0'
}

func (d *Descriptor) issnInput(text string) (Value, error) {
	var digits [14]byte
	n, family, magic, valid, err := scanIsn(text, "ISSN", &digits)
	if err != nil { return Value{}, err }
	if n == 13 {
		family = "EAN13"
		if digits[0] == '0' { family = "UPC" }
		if string(digits[:3]) == "977" { family = "ISSN" }
		if string(digits[:3]) == "978" || string(digits[:3]) == "979" { family = "ISBN" }
		if string(digits[:4]) == "9790" { family = "ISMN" }
	}
	if family != "ISSN" {
		return Value{}, failure("22P02", fmt.Sprintf("cannot cast %s to ISSN for number: \"%s\"", family, text))
	}
	if n == 8 {
		check := issnCheck(digits[:7])
		actual := digits[7]
		if actual == 'x' { actual = 'X' }
		if !magic && actual != check {
			return Value{}, failure("22P02", fmt.Sprintf("invalid check digit for ISSN number: \"%s\", should be %c", text, check))
		}
		copy(digits[3:10], digits[:7])
		copy(digits[:3], "977")
		digits[10], digits[11] = '0', '0'
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
	if n == 13 && !magic && digits[12] != check {
		return Value{}, failure("22P02", fmt.Sprintf("invalid check digit for ISSN number: \"%s\", should be %c", text, check))
	}
	number = (number*10 + int64(check-'0')) << 1
	if !valid { number |= 1 }
	return Value{typ: d, integer: number}, nil
}

func (d *Descriptor) issnOutput(v Value) (string, error) {
	if !v.validIssn() { return "", fmt.Errorf("invalid issn payload") }
	if (v.integer>>1)/10%100 != 0 { return isnLongOutput(v.integer), nil }
	var digits [7]byte
	number := (v.integer >> 1)/1000
	for i := 6; i >= 0; i-- {
		digits[i] = byte(number%10) + '0'
		number /= 10
	}
	var out [11]byte
	copy(out[:4], digits[:4])
	out[4] = '-'
	copy(out[5:8], digits[4:])
	out[8] = issnCheck(digits[:])
	n := 9
	if v.integer&1 != 0 { out[n] = '!'; n++ }
	return string(out[:n]), nil
}

// Serialization never relies on short display, even for zero issue digits.
func (d *Descriptor) issnSQLLiteral(v Value) (string, error) {
	if !v.validIssn() && !v.validIssn13() { return "", fmt.Errorf("invalid issn payload") }
	var out [16]byte
	out[0] = '\''
	number := v.integer >> 1
	for i := 13; i >= 1; i-- {
		out[i] = byte(number%10) + '0'
		number /= 10
	}
	n := 14
	if v.integer&1 != 0 { out[n] = '!'; n++ }
	out[n] = '\''
	return string(out[:n+1]), nil
}
