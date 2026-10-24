package sqltype

import "fmt"

// Isbn owns an ISBN-family packed identifier and its independent invalid flag.
func Isbn(id ID, name, declaration string) (*Descriptor, error) {
	if id == 0 || id == Unknown || name == "" || declaration == "" { return nil, fmt.Errorf("invalid isbn descriptor") }
	return &Descriptor{id: id, name: name, sql: declaration, database: "ISBN", isbn: true}, nil
}

func (d *Descriptor) IsIsbn() bool { return d != nil && d.isbn }

func (v Value) validIsbn() bool {
	if v.typ == nil || !v.typ.BaseType().IsIsbn() || v.integer < 0 { return false }
	n := v.integer >> 1
	return n >= 9780000000000 && n <= 9789999999999 || n >= 9791000000000 && n <= 9799999999999
}

// RemapIsbn requires source-pointer authorization from the catalog importer.
func (v Value) RemapIsbn(target *Descriptor) (Value, error) {
	if !v.typ.IsIsbn() || !target.IsIsbn() || !v.null && !v.validIsbn() { return Value{}, fmt.Errorf("incompatible imported isbn") }
	v.typ = target
	return v, nil
}

func (d *Descriptor) isbnInput(text string) (Value, error) {
	var digits [14]byte
	n, family, magic, valid, err := scanIsn(text, "ISBN", &digits)
	if err != nil { return Value{}, err }
	if n == 13 {
		family = "EAN13"
		if digits[0] == '0' { family = "UPC" }
		if string(digits[:3]) == "977" { family = "ISSN" }
		if string(digits[:3]) == "978" || string(digits[:3]) == "979" { family = "ISBN" }
		if string(digits[:4]) == "9790" { family = "ISMN" }
	}
	if family != "ISBN" { return Value{}, failure("22P02", fmt.Sprintf("cannot cast %s to ISBN for number: \"%s\"", family, text)) }
	if n == 10 {
		sum := 0
		for i, c := range digits[:9] { sum += int(c-'0') * (10-i) }
		check := byte((11-sum%11)%11)+'0'; if check == ':' { check = 'X' }
		actual := digits[9]; if actual == 'x' { actual = 'X' }
		if !magic && actual != check { return Value{}, failure("22P02", fmt.Sprintf("invalid check digit for ISBN number: \"%s\", should be %c", text, check)) }
		copy(digits[3:12], digits[:9]); copy(digits[:3], "978")
	}
	sum := 0; var number int64
	for i, c := range digits[:12] { weight := 1; if i%2 == 1 { weight = 3 }; sum += int(c-'0')*weight; number = number*10+int64(c-'0') }
	check := byte((10-sum%10)%10)+'0'
	if n == 13 && !magic && digits[12] != check { return Value{}, failure("22P02", fmt.Sprintf("invalid check digit for ISBN number: \"%s\", should be %c", text, check)) }
	number = (number*10+int64(check-'0')) << 1; if !valid { number |= 1 }
	return Value{typ: d, integer: number}, nil
}

func (d *Descriptor) isbnOutput(v Value) (string, error) {
	if !v.validIsbn() { return "", fmt.Errorf("invalid isbn payload") }
	var digits [13]byte
	number := v.integer >> 1
	for i := 12; i >= 0; i-- { digits[i] = byte(number%10)+'0'; number /= 10 }
	var separators [13]bool
	separators[12] = true
	start := 0
	ranges := eanISBN_range_new[:]
	if digits[2] == '8' {
		start = 3; ranges = eanISBN_range[:]
		sum := 0; for i, c := range digits[3:12] { sum += int(c-'0')*(10-i) }
		digits[12] = byte((11-sum%11)%11)+'0'; if digits[12] == ':' { digits[12] = 'X' }
	} else { separators[3] = true }
	pattern := eanPattern(digits[3:], ranges)
	pos := 3
	for i := range len(pattern) { if pattern[i] == '-' { separators[pos] = true } else { pos++ } }
	if pattern != "" { separators[pos] = true }
	var out [19]byte; n := 0
	for i := start; i < 13; i++ { if separators[i] { out[n] = '-'; n++ }; out[n] = digits[i]; n++ }
	if v.integer&1 != 0 { out[n] = '!'; n++ }
	return string(out[:n]), nil
}
