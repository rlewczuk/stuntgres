package xml

import (
	"encoding/base64"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// These are source expectations, not a reference decoder. No helper below calls
// a candidate codec, generated mapping or parser. Sources and full pinned-reader
// qualification are documented in data/README.md. Round B parser tests consume
// the same cases on validation and collecting routes.
type rawSourceCase struct {
	Name, Label, Body, Decoded, Text, Class string
}

func rawModernCNS1Facts(t *testing.T) map[uint32]rune {
	t.Helper()
	data, err := os.ReadFile("data/Unihan-16.0.0-CNS1-extract.txt")
	if err != nil { t.Fatal(err) }
	facts := map[uint32]rune{}
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" || strings.HasPrefix(line, "#") { continue }
		fields := strings.Split(line, "\t")
		if len(fields) != 3 || !strings.HasPrefix(fields[0], "U+") || fields[1] != "kIRG_TSource" || !strings.HasPrefix(fields[2], "T1-") { t.Fatal("malformed modern CNS1 source row", line) }
		scalar, e1 := strconv.ParseUint(strings.TrimPrefix(fields[0], "U+"), 16, 32)
		cell, e2 := strconv.ParseUint(strings.TrimPrefix(fields[2], "T1-"), 16, 16)
		if e1 != nil || e2 != nil || !utf8.ValidRune(rune(scalar)) || scalar == 0 || cell>>8 < 0x21 || cell>>8 > 0x7e || cell&255 < 0x21 || cell&255 > 0x7e { t.Fatal("invalid modern CNS1 source row", line) }
		if _, exists := facts[uint32(cell)]; exists { t.Fatal("duplicate modern CNS1 source cell", line) }
		facts[uint32(cell)] = rune(scalar)
	}
	if !reflect.DeepEqual(facts, map[uint32]rune{0x2728: 0x4ea0, 0x272f: 0x51ab, 0x2734: 0x52f9}) { t.Fatal("modern CNS1 source inventory drift", facts) }
	return facts
}

// Every one of the 35344 cells is yielded, including holes. CNS2 uses its own
// original width-four key; the three CNS1 facts must be absent from the PG map.
func rawKRCNSourceCells(t *testing.T, yield func(rawSourceCase)) {
	t.Helper()
	kr := rawJP2ReleasedMap(t, "euc_kr", 2)
	gb := rawJP2ReleasedMap(t, "euc_cn", 2)
	cns1 := rawJP2ReleasedMap(t, "euc_tw", 2)
	cns2 := rawJP2ReleasedMap(t, "euc_tw", 4)
	for cell, scalar := range rawModernCNS1Facts(t) {
		key := cell | 0x8080
		if _, exists := cns1[key]; exists { t.Fatalf("CNS1 supplement precondition changed at %04X", cell) }
		cns1[key] = scalar
	}
	for _, set := range []struct {
		name, label, prefix, suffix string
		values map[uint32]rune
		plane uint32
		assigned int
	}{
		{"ksc", "ISO-2022-KR", "\x1b$)C\x0e", "\x0f", kr, 0, 8227},
		{"gb", "ISO-2022-CN", "\x1b$)A\x0e", "\x0f", gb, 0, 7445},
		{"cns1", "ISO-2022-CN", "\x1b$)G\x0e", "\x0f", cns1, 0, 5867},
		{"cns2", "ISO-2022-CN", "\x1b$*H\x1bN", "", cns2, 0x8ea20000, 7650},
	} {
		assigned := 0
		for row := 0x21; row <= 0x7e; row++ {
			for col := 0x21; col <= 0x7e; col++ {
				key := set.plane | uint32((row|0x80)<<8 | (col|0x80))
				scalar := set.values[key]
				// Matching raw KSC framing excludes row49 and row7E independently
				// of the PostgreSQL byte-string verifier.
				if set.name == "ksc" && (row == 0x49 || row == 0x7e) { scalar = 0 }
				tc := rawSourceCase{Name: fmt.Sprintf("%s-%02X%02X", set.name, row, col), Label: set.label,
					Body: set.prefix + string([]byte{byte(row), byte(col)}) + set.suffix, Class: "conversion"}
				if scalar != 0 { assigned++; tc.Class, tc.Decoded, tc.Text = "scalar", string(scalar), string(scalar) }
				yield(tc)
			}
		}
		if assigned != set.assigned { t.Fatalf("source %s assigned=%d want=%d", set.name, assigned, set.assigned) }
	}
}

// Mapping-hole callback expectations use only the independently framed physical
// input and source-derived switched window. The pinned reader qualified every
// hole: duplicate6003, I/O1544, then grammar77 at line1/column48.
func rawKRCNSourceHoleDiagnostics(tc rawSourceCase) (int, string, []Diagnostic) {
	prefix := rawHeader(tc.Label) + "<r>"
	designation := 5
	if strings.HasPrefix(tc.Name, "cns2-") { designation = 4 }
	input := rawDocument(tc.Label, tc.Body)
	at := len(prefix) + designation
	bytes := input[at:at+min(4, len(input)-at)]
	preview := "input conversion failed due to input error, bytes"
	for _, b := range []byte(bytes) { preview += fmt.Sprintf(" 0x%02X", b) }
	if len(bytes) < 4 { preview += " 0x00" }
	return at, bytes, []Diagnostic{
		{Condition: "XML_I18N_CONV_FAILED", Domain: EncodingDomain, Severity: Fatal, Message: preview},
		{Condition: "XML_I18N_CONV_FAILED", Domain: EncodingDomain, Severity: Fatal, Message: preview},
		{Condition: "XML_IO_ENCODER", Domain: IODomain, Severity: Error, Message: "encoder error"},
		rawExpectedDiagnosticAtBase(prefix, len(rawHeader(tc.Label))-2, "XML_ERR_TAG_NOT_FINISHED", "Premature end of data in tag r line 1", len(prefix), len(prefix)+1),
	}
}

// Explicit big-endian units preserve malformed surrogate inputs. Do not use a
// Unicode encoder that silently replaces them. RFC2152 rule2 supplies Base64.
func rawUTF7SourceUnits(units ...uint16) string {
	bytes := make([]byte, 2*len(units))
	for i, unit := range units { bytes[2*i], bytes[2*i+1] = byte(unit>>8), byte(unit) }
	return "+" + base64.RawStdEncoding.EncodeToString(bytes) + "-"
}

func rawSourceXMLClass(scalar rune) string {
	if scalar == 9 || scalar == 10 || scalar == 13 || scalar >= 0x20 && scalar <= 0xd7ff || scalar >= 0xe000 && scalar <= 0xfffd || scalar >= 0x10000 && scalar <= 0x10ffff { return "scalar" }
	return "grammar"
}

// All256 direct bytes, all65536 single UTF16 units and all1024 high surrogates
// paired with each low endpoint. Every case is intended inside CDATA so raw
// '<' and '&' are characters, not accidental markup/entities.
func rawUTF7SourceScalars(yield func(rawSourceCase)) {
	for value := range 256 {
		tc := rawSourceCase{Name: fmt.Sprintf("direct-%02X", value), Label: "UTF-7", Body: string([]byte{byte(value)}), Class: "conversion"}
		if value == '+' { tc.Class = "empty" } else if value == 9 || value == 10 || value == 13 || value >= 0x20 && value <= 0x7d && value != '\\' {
			tc.Class, tc.Decoded, tc.Text = "scalar", tc.Body, tc.Body
			if value == 13 { tc.Text = "\n" }
		}
		yield(tc)
	}
	for unit := range 0x10000 {
		tc := rawSourceCase{Name: fmt.Sprintf("unit-%04X", unit), Label: "UTF-7", Body: rawUTF7SourceUnits(uint16(unit)), Class: "surrogate"}
		if unit < 0xd800 || unit > 0xdfff {
			tc.Class, tc.Decoded, tc.Text = rawSourceXMLClass(rune(unit)), string(rune(unit)), string(rune(unit))
			if unit == 13 { tc.Text = "\n" }
		}
		yield(tc)
	}
	for high := 0xd800; high <= 0xdbff; high++ {
		for _, low := range []uint16{0xdc00, 0xdfff} {
			scalar := rune(0x10000 + ((high-0xd800)<<10) + int(low-0xdc00))
			yield(rawSourceCase{Name: fmt.Sprintf("pair-%04X-%04X", high, low), Label: "UTF-7", Body: rawUTF7SourceUnits(uint16(high), low), Decoded: string(scalar), Text: string(scalar), Class: "scalar"})
		}
	}
}

// Source/state witnesses: RFC1557/1922 framing plus matching glibc2.36 input
// loops. Full mapping/direct/UTF16 inventories have pinned-reader qualification;
// additional combinations are source-derived, not claimed new observations.
// Active selection and announcement are distinct; no mail-only LF reset applies.
func rawKRCNUTF7SourceStates() []rawSourceCase {
	return []rawSourceCase{
		{"kr-unannounced", "ISO-2022-KR", "\x0eGQ\x0f", "한", "한", "scalar"},
		{"kr-designation-ascii", "ISO-2022-KR", "\x1b$)CGQ", "GQ", "GQ", "scalar"},
		{"kr-repeated", "ISO-2022-KR", "\x0f\x0f\x0e\x0eGQ\x0f\x0f", "한", "한", "scalar"},
		{"kr-designation-selected", "ISO-2022-KR", "\x0eGQ\x1b$)CGQ\x0f", "한한", "한한", "scalar"},
		{"kr-crlf", "ISO-2022-KR", "\x0eGQ\x0f\r\n\x0eGQ\x0f", "한\r\n한", "한\n한", "scalar"},
		{"kr-del", "ISO-2022-KR", "A\x7fB", "A\x7fB", "A\x7fB", "scalar"},
		{"cn-unannounced", "ISO-2022-CN", "\x0eVP\x0f", "中", "中", "scalar"},
		{"cn-designation-ascii", "ISO-2022-CN", "\x1b$)GVP", "VP", "VP", "scalar"},
		{"cn-delayed", "ISO-2022-CN", "\x1b$)A\x0eVP\x1b$)GVP\x0eVP\x0f", "中中烘", "中中烘", "scalar"},
		{"cn-retained", "ISO-2022-CN", "\x1b$)G\x0eVP\x1b$)AVP\x0eVP\x0f", "烘烘中", "烘烘中", "scalar"},
		{"cn-h-preserves", "ISO-2022-CN", "\x1b$)G\x1b$*H\x0eVP\x0f", "烘", "烘", "scalar"},
		{"cn-ss2-ascii", "ISO-2022-CN", "A\x1bN!!B", "A乂B", "A乂B", "scalar"},
		{"cn-ss2-gb", "ISO-2022-CN", "\x0eVP\x1bN!!VP\x0f", "中乂中", "中乂中", "scalar"},
		{"cn-ss2-cns1", "ISO-2022-CN", "\x1b$)G\x0eVP\x1bN!!VP\x0f", "烘乂烘", "烘乂烘", "scalar"},
		{"cn-lf", "ISO-2022-CN", "\x1b$)G\x0eVP\x0f\n\x0eVP\x0f", "烘\n烘", "烘\n烘", "scalar"},
		{"utf-literal-plus", "UTF-7", "+-", "+", "+", "scalar"},
		{"utf-empty-shift", "UTF-7", "+!", "!", "!", "scalar"},
		{"utf-multi-bmp", "UTF-7", "+AOkDsw-", "éγ", "éγ", "scalar"},
		{"utf-zero-residue", "UTF-7", rawUTF7SourceUnits(0x00e9, 0x03b3, 0x0041), "éγA", "éγA", "scalar"},
		{"utf-encoded-ascii", "UTF-7", rawUTF7SourceUnits(0x0041), "A", "A", "scalar"},
		{"utf-encoded-crlf", "UTF-7", rawUTF7SourceUnits(13, 10), "\r\n", "\n", "scalar"},
		{"utf-adjacent", "UTF-7", "+AOk-+AOk-", "éé", "éé", "scalar"},
		{"utf-mixed", "UTF-7", "A+AOk-B+-C", "AéB+C", "AéB+C", "scalar"},
		{"utf-supplementary", "UTF-7", "+2D3eAA-", "😀", "😀", "scalar"},
		{"utf-implicit-markup", "UTF-7", "+AOk", "é", "é", "scalar"},
		{"utf-implicit-comma", "UTF-7", "+AOk,", "é,", "é,", "scalar"},
		{"utf-implicit-equals", "UTF-7", "+AOk=", "é=", "é=", "scalar"},
		{"utf-implicit-crlf", "UTF-7", "+AOk\r\n", "é\r\n", "é\n", "scalar"},
		{"utf-six-bits", "UTF-7", "+A-", "", "", "conversion"},
		{"utf-twelve-bits", "UTF-7", "+AA-", "", "", "conversion"},
		{"utf-nonzero-two", "UTF-7", "+AOl-", "é", "é", "conversion"},
		{"utf-nonzero-four", "UTF-7", "+AOkDsx-", "éγ", "éγ", "conversion"},
		{"utf-low", "UTF-7", "+3AA-", "", "", "conversion"},
		{"utf-low-after-direct", "UTF-7", "A+3AA-", "A", "A", "conversion"},
		{"utf-low-same-shift", "UTF-7", "+AOncAA-", "é", "é", "conversion"},
		{"utf-low-after-terminator", "UTF-7", "+AOk-+3AA-", "é", "é", "conversion"},
		{"utf-high", "UTF-7", "+2AA-", "", "", "conversion"},
		{"utf-high-bmp", "UTF-7", "+2AAAQQ-", "", "", "conversion"},
		{"utf-high-high", "UTF-7", rawUTF7SourceUnits(0xd800, 0xdbff), "", "", "conversion"},
		{"utf-nul", "UTF-7", "+AAA-", "\x00", "", "grammar"},
		{"utf-esc", "UTF-7", "+ABs-", "\x1b", "", "grammar"},
	}
}

// A singleton start is followed by explicit SI/ASCII lookahead. Selected cases
// use a complete b!' pair so each lead's hole/framing expectation comes from the
// independent original map, not from whatever byte the closing tag supplies.
// As with the direct UTF7 inventory, callers place these bodies inside CDATA.
func rawKRCNSourceStarts(t *testing.T, yield func(rawSourceCase)) {
	t.Helper()
	for _, set := range []struct { name, label, designation, source string }{
		{"kr", "ISO-2022-KR", "\x1b$)C", "euc_kr"},
		{"gb", "ISO-2022-CN", "\x1b$)A", "euc_cn"},
		{"cns1", "ISO-2022-CN", "\x1b$)G", "euc_tw"},
	} {
		values := rawJP2ReleasedMap(t, set.source, 2)
		for value := range 256 {
			b := string([]byte{byte(value)})
			initial := rawSourceCase{Name: fmt.Sprintf("%s-initial-%02X", set.name, value), Label: set.label, Body: b+"\x0fA", Class: "conversion"}
			if value < 128 && (set.name == "kr" || value != 127) {
				initial.Decoded, initial.Text, initial.Class = b+"A", b+"A", rawSourceXMLClass(rune(value))
				if value == 13 { initial.Text = "\nA" }
				if value == 14 || value == 15 { initial.Decoded, initial.Text, initial.Class = "A", "A", "scalar" }
			}
			yield(initial)
			selected := rawSourceCase{Name: fmt.Sprintf("%s-selected-%02X", set.name, value), Label: set.label, Body: set.designation+"\x0e"+b+"!\x0fA", Class: "conversion"}
			scalar := values[uint32((value|0x80)<<8 | 0xa1)]
			if value < 0x21 || value > 0x7e || set.name == "kr" && (value == 0x49 || value == 0x7e) { scalar = 0 }
			if scalar != 0 { selected.Decoded, selected.Text, selected.Class = string(scalar)+"A", string(scalar)+"A", "scalar" }
			if value == 15 { selected.Decoded, selected.Text, selected.Class = "!A", "!A", "scalar" }
			yield(selected)
		}
	}
}

// True physical EOF converter outcomes for zero-output tails after a completed
// root. Appending actual closing markup to the same tail is a different case:
// the bytes must remain real lookahead, never sliced at a replay boundary.
func rawKRCNSourceEOFTails() []struct { Label, Tail string; Consumed, Buffered int } {
	return []struct { Label, Tail string; Consumed, Buffered int }{
		{"ISO-2022-KR", "\x1b", 0, 1}, {"ISO-2022-KR", "\x1b$", 0, 2},
		{"ISO-2022-KR", "\x1b$)", 0, 3}, {"ISO-2022-KR", "\x1b$)C", 4, 0},
		{"ISO-2022-KR", "\x0eG", 1, 1}, {"ISO-2022-KR", "\x0e\x01", 1, 1},
		{"ISO-2022-CN", "\x1b", 0, 1}, {"ISO-2022-CN", "\x1b$", 0, 2},
		{"ISO-2022-CN", "\x1b$)", 0, 3}, {"ISO-2022-CN", "\x1b$*", 0, 3},
		{"ISO-2022-CN", "\x1bN", 0, 2}, {"ISO-2022-CN", "\x1bN!", 0, 3},
		{"ISO-2022-CN", "\x1b$)G", 4, 0}, {"ISO-2022-CN", "\x1b$*H", 4, 0},
		{"ISO-2022-CN", "\x1b$)G\x0eV", 5, 1},
	}
}

// Physical converter cursors, not parser positions. In particular lone-low
// chained rejection rewinds to the last emission, including its residual bits
// and earlier terminator. Same-state failed retries must preserve that boundary.
func rawUTF7SourceCursors() []struct { Body, Prefix string; Consumed, Buffered int; Fault bool } {
	return []struct { Body, Prefix string; Consumed, Buffered int; Fault bool }{
		{"+", "", 0, 1, false}, {"+A", "", 2, 0, false}, {"+AA", "", 3, 0, false},
		{"+2AA", "", 4, 0, false}, {"+2D3eA", "", 6, 0, false},
		{"+AOk", "é", 4, 0, false}, {"+AOl", "é", 4, 0, false},
		{"+AOl-", "é", 4, 1, true}, {"+3AA-", "", 0, 5, true},
		{"A+3AA-", "A", 1, 5, true}, {"+AOncAA-", "é", 4, 4, true},
		{"+AOk-+3AA-", "é", 4, 6, true},
	}
}

func TestXMLRawKRCNUTF7CorpusSources(t *testing.T) {
	counts := map[string]int{}
	rawKRCNSourceCells(t, func(tc rawSourceCase) {
		counts[tc.Class]++
		if tc.Class == "conversion" {
			at, bytes, diagnostics := rawKRCNSourceHoleDiagnostics(tc)
			if len(bytes) != 4 || at < len(rawHeader(tc.Label)) || len(diagnostics) != 4 || diagnostics[0] != diagnostics[1] || diagnostics[3].Column != 48 { t.Fatal("source hole framing/diagnostic extent", tc.Name, at, bytes, diagnostics) }
		}
	})
	if !reflect.DeepEqual(counts, map[string]int{"scalar": 29189, "conversion": 6155}) { t.Fatal("mapping corpus inventory", counts) }
	counts = map[string]int{}
	rawUTF7SourceScalars(func(tc rawSourceCase) { counts[tc.Class]++ })
	if !reflect.DeepEqual(counts, map[string]int{"scalar": 65600, "surrogate": 2048, "conversion": 160, "grammar": 31, "empty": 1}) { t.Fatal("UTF7 corpus inventory", counts) }
	seen := map[string]bool{}
	for _, tc := range rawKRCNUTF7SourceStates() {
		if tc.Name == "" || seen[tc.Name] || tc.Label == "" || tc.Class == "" { t.Fatal("invalid source state identity", tc) }
		seen[tc.Name] = true
	}
	rawKRCNSourceStarts(t, func(tc rawSourceCase) {
		if seen[tc.Name] { t.Fatal("duplicate singleton source case", tc.Name) }
		seen[tc.Name] = true
	})
	for _, tc := range rawKRCNSourceEOFTails() {
		if tc.Consumed < 0 || tc.Consumed+tc.Buffered != len(tc.Tail) { t.Fatal("source EOF extent", tc) }
	}
	for _, tc := range rawUTF7SourceCursors() {
		if tc.Consumed < 0 || tc.Consumed+tc.Buffered != len(tc.Body) { t.Fatal("source cursor extent", tc) }
	}
	for _, tc := range []struct { units []uint16; want string }{
		{[]uint16{0x00e9}, "+AOk-"}, {[]uint16{0x00e9, 0x03b3}, "+AOkDsw-"},
		{[]uint16{0xd83d, 0xde00}, "+2D3eAA-"}, {[]uint16{0xdc00}, "+3AA-"},
	} { if got := rawUTF7SourceUnits(tc.units...); got != tc.want { t.Fatal("independent UTF16/Base64 witness", got, tc.want) } }
}
