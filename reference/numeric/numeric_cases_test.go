package reference_test

import "strings"

// Independent numeric lexical expectations copied from driver_test.go.
// These expectations come from the PostgreSQL numeric lexical contract, not
// from native decoding or observed parity.
func numericLiteralErrors() []struct{ token, reason string } {
	var cases []struct{ token, reason string }
	for _, prefix := range []struct{ spelling, name string }{{"b", "binary"}, {"o", "octal"}, {"x", "hexadecimal"}} {
		for _, token := range []string{"0" + prefix.spelling, "0" + strings.ToUpper(prefix.spelling), "0" + prefix.spelling + "_"} {
			cases = append(cases, struct{ token, reason string }{token, "invalid " + prefix.name + " integer"})
		}
	}
	for _, token := range []string{"0b2", "0b102", "0o8", "0o78", "0xg", "0x1g", "0b__1", "0o__1", "0x__1", "0b_2", "0x_g", "1_", "1__0", "1_000_", "1_000__0", "0x1_", "0x1__f", "1z", "1b", "1o", "1x", "0b1junk", "0o1$", "10é", "1efoo", "1e+", "1e3foo", "1e3_", "1_000e3__0", "1E3foo", "1e+3foo", "1e-3_", "1_000E+3__0"} {
		cases = append(cases, struct{ token, reason string }{token, "trailing junk after numeric literal"})
	}
	for _, token := range []string{strings.Repeat("9", 200) + "z", "0x" + strings.Repeat("F", 100) + "z", "0o" + strings.Repeat("7", 100) + "z", "0b" + strings.Repeat("1", 200) + "z"} {
		cases = append(cases, struct{ token, reason string }{token, "trailing junk after numeric literal"})
	}
	return cases
}

func numericEndpoints() []string {
	return []string{
		"9_223_372_036_854_775_807", "-9_223_372_036_854_775_808",
		"0x7FFF_FFFF_FFFF_FFFF", "-0x8000_0000_0000_0000",
		"0o777777777777777777777", "-0o1000000000000000000000",
		"0b" + strings.Repeat("1", 63), "-0b1" + strings.Repeat("0", 63),
		"+0x7FFF_FFFF_FFFF_FFFF", "- /*comment*/ 0x8000_0000_0000_0000",
	}
}

func numericLiteralSpellings() []struct {
	literal string
	value   int64
} {
	cases := []struct {
		literal string
		value   int64
	}{
		{"0B_1_010", 10}, {"0O_1_2", 10}, {"0X_A", 10}, {"001_000", 1000}, {"010", 10}, {"08", 8},
		{"0x1EEE_FFFF", 518979583}, {"0o2_73", 187}, {"0b_10_0101", 37}, {"0x1e3", 483},
	}
	for _, prefix := range []string{"", "0b", "0o", "0x"} {
		for _, sign := range []string{"", "+", "-"} {
			cases = append(cases, struct {
				literal string
				value   int64
			}{sign + prefix + "0", 0})
			value := int64(1)
			if sign == "-" {
				value = -1
			}
			cases = append(cases, struct {
				literal string
				value   int64
			}{sign + prefix + "1", value})
		}
	}
	return cases
}
