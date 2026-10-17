//go:build !noservertests

package reference_test

import (
	"encoding/binary"
	"encoding/hex"
	"strings"
	"testing"
)

// Independent REL_18_6 receive/send and output facts (the corresponding
// utils/adt type files), not native encoders or observed native rows. UTC is
// fixed by the fixture. Base inputs remain in the original inventory; these
// canonical output facts deliberately differ from input spelling where needed.
// Every alternate is greater than base except BOOL (false < true) and ACLITEM
// (different privileges, equality only). VOID has only one nonnull value: the
// empty value, with no admitted comparison cells; it has no fake alternate.
type serverComparisonFreshFact struct {
	text, wire, low, high string
}

var serverComparisonFreshFacts = map[uint32]serverComparisonFreshFact{
	16:   {"f", "00", "t", "f"},
	17:   {"\\x02", "02", "\\x01", "\\x02"},
	18:   {"a", "61", "", "a"},
	19:   {"a", "61", "", "a"},
	21:   {"2", "0002", "1", "2"},
	23:   {"2", "00000002", "1", "2"},
	20:   {"2", "0000000000000002", "1", "2"},
	700:  {"2", "40000000", "1", "2"},
	701:  {"2", "4000000000000000", "1", "2"},
	1700: {"2", "00010000000000000002", "1", "2"},
	790:  {"2.00", "00000000000000c8", "$1.00", "$2.00"},
	25:   {"a", "61", "", "a"},
	1043: {"a", "61", "", "a"},
	1042: {"a", "61", "", "a"},
	26:   {"2", "00000002", "1", "2"},
	24:   {"2", "00000002", "1", "2"},
	2202: {"2", "00000002", "1", "2"},
	2203: {"2", "00000002", "1", "2"},
	2204: {"2", "00000002", "1", "2"},
	2205: {"2", "00000002", "1", "2"},
	2206: {"2", "00000002", "1", "2"},
	4096: {"2", "00000002", "1", "2"},
	4089: {"2", "00000002", "1", "2"},
	4191: {"2", "00000002", "1", "2"},
	3734: {"2", "00000002", "1", "2"},
	3769: {"2", "00000002", "1", "2"},
	27:   {"(2,1)", "", "(1,1)", "(2,1)"},
	28:   {"2", "00000002", "1", "2"},
	29:   {"2", "00000002", "1", "2"},
	5069: {"2", "0000000000000002", "1", "2"},
	1082: {"2000-01-02", "00000001", "2000-01-01", "2000-01-02"},
	1083: {"01:00:00", "00000000d693a400", "00:00:00", "01:00:00"},
	1266: {"01:00:00+00", "00000000d693a40000000000", "00:00:00+00", "01:00:00+00"},
	1114: {"2000-01-02 00:00:00", "000000141dd76000", "2000-01-01 00:00:00", "2000-01-02 00:00:00"},
	1184: {"2000-01-02 00:00:00+00", "000000141dd76000", "2000-01-01 00:00:00+00", "2000-01-02 00:00:00+00"},
	1186: {"01:00:00", "00000000d693a4000000000000000000", "00:00:00", "01:00:00"},
	1560: {"1", "0000000180", "0", "1"},
	1562: {"1", "0000000180", "0", "1"},
	2950: {"00000000-0000-0000-0000-000000000002", "00000000000000000000000000000002", "00000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-000000000002"},
	869:  {"1.0.0.0/8", "0208000401000000", "0.0.0.0/0", "1.0.0.0/8"},
	650:  {"1.0.0.0/8", "0208010401000000", "0.0.0.0/0", "1.0.0.0/8"},
	114:  {"2", "32", "1", "2"},
	3802: {"2", "0132", "1", "2"},
	4072: {"$.a", "01242e61", "$", "$.\"a\""},
	1033: {"=w/stuntgres", "", "=r/stuntgres", "=w/stuntgres"},
	2278: {"", "", "", ""},
	3904: {"[2,3)", "0200000004000000020000000400000003", "[1,2)", "[2,3)"},
	3926: {"[2,3)", "02000000080000000000000002000000080000000000000003", "[1,2)", "[2,3)"},
	3906: {"[2,3)", "020000000a000100000000000000020000000a00010000000000000003", "[1,2)", "[2,3)"},
	3912: {"[2000-01-02,2000-01-03)", "0200000004000000010000000400000002", "[2000-01-01,2000-01-02)", "[2000-01-02,2000-01-03)"},
	3908: {"[2000-01-02,2000-01-03)", "0200000008000000141dd7600000000008000000283baec000", "[\"2000-01-01 00:00:00\",\"2000-01-02 00:00:00\")", "[\"2000-01-02 00:00:00\",\"2000-01-03 00:00:00\")"},
	3910: {"[2000-01-02,2000-01-03)", "0200000008000000141dd7600000000008000000283baec000", "[\"2000-01-01 00:00:00+00\",\"2000-01-02 00:00:00+00\")", "[\"2000-01-02 00:00:00+00\",\"2000-01-03 00:00:00+00\")"},
	4451: {"{[2,3)}", "00000001000000110200000004000000020000000400000003", "{[1,2)}", "{[2,3)}"},
	4536: {"{[2,3)}", "000000010000001902000000080000000000000002000000080000000000000003", "{[1,2)}", "{[2,3)}"},
	4532: {"{[2,3)}", "000000010000001d020000000a000100000000000000020000000a00010000000000000003", "{[1,2)}", "{[2,3)}"},
}

// array_out quotes empty/special/whitespace elements and escapes quote/backslash.
func serverComparisonArrayText(value string) string {
	if value == "" || strings.EqualFold(value, "NULL") || strings.ContainsAny(value, "{},\"\\ \t\n\r\v\f") {
		value = "\"" + strings.NewReplacer("\\", "\\\\", "\"", "\\\"").Replace(value) + "\""
	}
	return "{" + value + "}"
}

func (s serverComparisonSource) fresh(t *testing.T) serverComparisonFreshFact {
	t.Helper()
	oid := s.oid
	if s.element != 0 {
		oid = s.element
	}
	fact, ok := serverComparisonFreshFacts[oid]
	if !ok {
		t.Fatalf("missing independent fresh facts for %s", s.name)
	}
	if oid != 2278 && fact.low == fact.high {
		t.Fatalf("fresh value is not distinct for %s", s.name)
	}
	if s.element == 0 {
		return fact
	}
	// array_recv: ndim, hasnull, element OID, dimension/lower bound, length,
	// then the actual element receiver bytes. No native array encoder is used.
	if s.binaryInput {
		element, err := hex.DecodeString(fact.wire)
		if err != nil {
			t.Fatal(err)
		}
		var wire []byte
		for _, n := range []uint32{1, 0, s.element, 1, 1, uint32(len(element))} {
			wire = binary.BigEndian.AppendUint32(wire, n)
		}
		fact.wire = hex.EncodeToString(append(wire, element...))
	}
	fact.text = serverComparisonArrayText(fact.text)
	fact.low = serverComparisonArrayText(fact.low)
	fact.high = serverComparisonArrayText(fact.high)
	return fact
}

func (s serverComparisonSource) alternate(t *testing.T, format int) []byte {
	fact := s.fresh(t)
	s.text, s.wire = fact.text, fact.wire
	return s.input(t, format)
}

func serverComparisonWireTruth(op string, sign int) string {
	truth := false
	switch op {
	case "=":
		truth = sign == 0
	case "<>", "!=":
		truth = sign != 0
	case "<":
		truth = sign < 0
	case "<=":
		truth = sign <= 0
	case ">":
		truth = sign > 0
	case ">=":
		truth = sign >= 0
	}
	if truth {
		return "t"
	}
	return "f"
}
