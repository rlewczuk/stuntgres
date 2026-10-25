package sqltype

import "fmt"

// Upc owns the leading-zero EAN subset and its independent invalid-input flag.
func Upc(id ID, name, declaration string) (*Descriptor, error) {
	if id == 0 || id == Unknown || name == "" || declaration == "" { return nil, fmt.Errorf("invalid upc descriptor") }
	return &Descriptor{id: id, name: name, sql: declaration, database: "UPC", upc: true}, nil
}

func (d *Descriptor) IsUpc() bool { return d != nil && d.upc }

func validUpcNumber(packed int64) bool {
	if packed < 0 || packed > 1999999999999 { return false }
	n := packed >> 1
	check, sum := n%10, int64(0)
	n /= 10
	for i := range 11 {
		weight := int64(3)
		if i%2 == 1 { weight = 1 }
		sum += n%10 * weight
		n /= 10
	}
	return check == (10-sum%10)%10
}

func (v Value) validUpc() bool { return v.typ != nil && v.typ.BaseType().IsUpc() && validUpcNumber(v.integer) }

// RemapUpc requires exact source-pointer authorization from the catalog importer.
func (v Value) RemapUpc(target *Descriptor) (Value, error) {
	if !v.typ.IsUpc() || !target.IsUpc() || !v.null && !v.validUpc() { return Value{}, fmt.Errorf("incompatible imported upc") }
	v.typ = target
	return v, nil
}

func isnNumberFamily(digits []byte) string {
	if string(digits[:3]) == "978" || string(digits[:3]) == "979" && digits[3] != '0' { return "ISBN" }
	if string(digits[:3]) == "977" { return "ISSN" }
	if string(digits[:4]) == "9790" { return "ISMN" }
	return "EAN13"
}

func (d *Descriptor) upcInput(text string) (Value, error) {
	var digits [14]byte
	n, family, magic, valid, err := scanIsn(text, "UPC", &digits)
	if err != nil { return Value{}, err }
	if n == 13 { family = isnNumberFamily(digits[:]); if digits[0] == '0' { family = "UPC" } }
	if family != "UPC" { return Value{}, failure("22P02", fmt.Sprintf("cannot cast %s to UPC for number: \"%s\"", family, text)) }
	start, sum := 0, 0
	if n == 13 { start = 1 }
	var number int64
	for i, c := range digits[start:n-1] {
		weight := 3
		if i%2 == 1 { weight = 1 }
		sum += int(c-'0') * weight
		number = number*10 + int64(c-'0')
	}
	check := byte((10-sum%10)%10) + '0'
	if !magic && digits[n-1] != check { return Value{}, failure("22P02", fmt.Sprintf("invalid check digit for UPC number: \"%s\", should be %c", text, check)) }
	number = (number*10 + int64(check-'0')) << 1
	if !valid { number |= 1 }
	return Value{typ: d, integer: number}, nil
}

func (d *Descriptor) upcOutput(v Value) (string, error) {
	if !v.validUpc() { return "", fmt.Errorf("invalid upc payload") }
	var out [13]byte
	n := v.integer >> 1
	for i := 11; i >= 0; i-- { out[i] = byte(n%10)+'0'; n /= 10 }
	if v.integer&1 != 0 { out[12] = '!'; return string(out[:]), nil }
	return string(out[:12]), nil
}

func (d *Descriptor) upcSQLLiteral(v Value) (string, error) {
	if !v.validUpc() { return "", fmt.Errorf("invalid upc payload") }
	var out [16]byte
	out[0] = '\''
	number := v.integer >> 1
	for i := 13; i >= 1; i-- { out[i] = byte(number%10)+'0'; number /= 10 }
	n := 14
	if v.integer&1 != 0 { out[n] = '!'; n++ }
	out[n] = '\''
	return string(out[:n+1]), nil
}

// applyUpcEan never serializes through short display notation: the marker is
// payload, including when the PostgreSQL short formatter crops its display.
func (p CoercionPlan) applyUpcEan(v Value) (Value, error) {
	if !v.ValidResult(p.source) { return Value{}, fmt.Errorf("invalid ISN conversion source") }
	if p.target.Type.IsUpc() && !validUpcNumber(v.integer) {
		var digits [13]byte
		n := v.integer >> 1
		for i := 12; i >= 0; i-- { digits[i] = byte(n%10)+'0'; n /= 10 }
		family := isnNumberFamily(digits[:])
		if family != "EAN13" { family = "EAN13("+family+")" }
		return Value{}, failure("22P02", fmt.Sprintf("cannot cast %s to UPC for number: \"%s\"", family, string(digits[:])))
	}
	return Value{typ: p.target.Type, integer: v.integer}, nil
}
