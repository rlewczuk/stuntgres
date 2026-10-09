package main

import (
	"bytes"
	"fmt"
	"go/format"
	"path/filepath"
	"sort"
	"strings"
)

type caseData struct {
	code   uint32
	simple [4]uint32
	full   [4][]uint32
}

func caseCode(s string) (uint32, error) {
	c, err := hex(s)
	if err != nil || c > 0x10FFFF {
		return 0, fmt.Errorf("invalid Unicode codepoint %q", s)
	}
	return c, nil
}
func caseSequence(s string) ([]uint32, error) {
	fields := strings.Fields(s)
	if len(fields) < 1 || len(fields) > 3 {
		return nil, fmt.Errorf("invalid case expansion %q", s)
	}
	out := make([]uint32, len(fields))
	for i, field := range fields {
		c, err := caseCode(field)
		if err != nil {
			return nil, err
		}
		if c >= 0xD800 && c <= 0xDFFF {
			return nil, fmt.Errorf("surrogate in case expansion")
		}
		out[i] = c
	}
	return out, nil
}
func caseBounds(s string) (interval, error) {
	bounds := strings.Split(strings.TrimSpace(s), "..")
	if len(bounds) > 2 {
		return interval{}, fmt.Errorf("invalid codepoint range")
	}
	lo, err := caseCode(bounds[0])
	if err != nil {
		return interval{}, err
	}
	hi := lo
	if len(bounds) == 2 {
		hi, err = caseCode(bounds[1])
		if err != nil {
			return interval{}, err
		}
	}
	if lo > hi {
		return interval{}, fmt.Errorf("reversed codepoint range")
	}
	return interval{lo: lo, hi: hi}, nil
}
func mergeCaseRanges(ranges []interval) []interval {
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].lo < ranges[j].lo })
	out := ranges[:0]
	for _, r := range ranges {
		if len(out) != 0 && r.lo <= out[len(out)-1].hi+1 {
			out[len(out)-1].hi = max(out[len(out)-1].hi, r.hi)
		} else {
			out = append(out, r)
		}
	}
	return out
}
func caseFields(line string) []string {
	raw, _, _ := strings.Cut(line, "#")
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	fields := strings.Split(raw, ";")
	for i := range fields {
		fields[i] = strings.TrimSpace(fields[i])
	}
	return fields
}

func generateCase(source string) ([]byte, error) {
	entries := map[uint32]*caseData{}
	get := func(c uint32) *caseData {
		if e := entries[c]; e != nil {
			return e
		}
		e := &caseData{code: c, simple: [4]uint32{c, c, c, c}}
		for i := range e.full {
			e.full[i] = []uint32{c}
		}
		entries[c] = e
		return e
	}
	var properties [4][]interval // Alphabetic, Cased, Case_Ignorable, Nd
	var first *interval
	var firstCategory, firstName string
	if err := lines(filepath.Join(source, "UnicodeData.txt"), func(line string) error {
		f := strings.Split(line, ";")
		if len(f) != 15 {
			return fmt.Errorf("invalid UnicodeData record")
		}
		c, err := caseCode(f[0])
		if err != nil {
			return err
		}
		if strings.HasSuffix(f[1], ", First>") {
			if first != nil {
				return fmt.Errorf("nested UnicodeData range")
			}
			first = &interval{lo: c}
			firstCategory = f[2]
			firstName = strings.TrimSuffix(f[1], ", First>")
			return nil
		}
		if strings.HasSuffix(f[1], ", Last>") {
			if first == nil || first.lo > c || firstCategory != f[2] || firstName != strings.TrimSuffix(f[1], ", Last>") {
				return fmt.Errorf("unmatched UnicodeData range")
			}
			if f[2] == "Nd" {
				properties[3] = append(properties[3], interval{lo: first.lo, hi: c})
			}
			first = nil
			return nil
		}
		if first != nil {
			return fmt.Errorf("unterminated UnicodeData range")
		}
		if f[2] == "Nd" {
			properties[3] = append(properties[3], interval{lo: c, hi: c})
		}
		for i, field := range []int{13, 12, 14} {
			index := i
			if i == 2 {
				index = 3
			}
			mapping := f[field]
			if i == 2 && mapping == "" {
				mapping = f[12]
			}
			if mapping == "" {
				continue
			}
			mapped, err := caseSequence(mapping)
			if err != nil {
				return err
			}
			if len(mapped) != 1 {
				return fmt.Errorf("non-simple UnicodeData mapping")
			}
			e := get(c)
			e.simple[index] = mapped[0]
			e.full[index] = mapped
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if first != nil {
		return nil, fmt.Errorf("unterminated UnicodeData range")
	}
	var sigma []uint32
	if err := lines(filepath.Join(source, "SpecialCasing.txt"), func(line string) error {
		f := caseFields(line)
		if f == nil {
			return nil
		}
		if len(f) < 5 || len(f) > 6 || len(f) == 6 && f[5] != "" {
			return fmt.Errorf("invalid SpecialCasing record")
		}
		c, err := caseCode(f[0])
		if err != nil {
			return err
		}
		// Excluded language rules can contain empty (deletion) mappings.
		if f[4] != "" && f[4] != "Final_Sigma" {
			return nil
		}
		var maps [3][]uint32
		for i := range maps {
			maps[i], err = caseSequence(f[i+1])
			if err != nil {
				return err
			}
		}
		switch f[4] {
		case "":
			e := get(c)
			e.full[0], e.full[3], e.full[1] = maps[0], maps[1], maps[2]
		case "Final_Sigma":
			if c != 0x03A3 || sigma != nil || len(maps[1]) != 1 || maps[1][0] != c || len(maps[2]) != 1 || maps[2][0] != c {
				return fmt.Errorf("invalid Final_Sigma record")
			}
			sigma = maps[0]
		default: // Language-specific tr/az/lt rules are not builtin locale rules.
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if len(sigma) != 1 || sigma[0] != 0x03C2 {
		return nil, fmt.Errorf("missing Final_Sigma mapping")
	}
	if err := lines(filepath.Join(source, "CaseFolding.txt"), func(line string) error {
		f := caseFields(line)
		if f == nil {
			return nil
		}
		if len(f) != 4 || f[3] != "" {
			return fmt.Errorf("invalid CaseFolding record")
		}
		c, err := caseCode(f[0])
		if err != nil {
			return err
		}
		mapped, err := caseSequence(f[2])
		if err != nil {
			return err
		}
		switch f[1] {
		case "C", "S":
			if len(mapped) != 1 {
				return fmt.Errorf("non-simple case fold")
			}
			e := get(c)
			e.simple[2] = mapped[0]
			if f[1] == "C" {
				e.full[2] = mapped
			}
		case "F":
			get(c).full[2] = mapped
		case "T": // Exclude Turkic locale tailoring.
		default:
			return fmt.Errorf("invalid case fold status")
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := lines(filepath.Join(source, "DerivedCoreProperties.txt"), func(line string) error {
		f := caseFields(line)
		if f == nil {
			return nil
		}
		if len(f) < 2 {
			return fmt.Errorf("invalid core property record")
		}
		index := -1
		switch f[1] {
		case "Alphabetic":
			index = 0
		case "Cased":
			index = 1
		case "Case_Ignorable":
			index = 2
		}
		if index < 0 {
			return nil
		}
		if len(f) != 2 {
			return fmt.Errorf("invalid case property record")
		}
		r, err := caseBounds(f[0])
		if err != nil {
			return err
		}
		properties[index] = append(properties[index], r)
		return nil
	}); err != nil {
		return nil, err
	}
	codes := make([]uint32, 0, len(entries))
	for c := range entries {
		codes = append(codes, c)
	}
	sort.Slice(codes, func(i, j int) bool { return codes[i] < codes[j] })
	var out bytes.Buffer
	fmt.Fprintln(&out, "// Code generated from Unicode16 UCD by ./generate; DO NOT EDIT.")
	fmt.Fprintln(&out, "// Unicode data copyright Unicode, Inc.; see LICENSE.\npackage unicode16")
	mapping := func(seq []uint32) {
		fmt.Fprint(&out, "{[3]rune{")
		for _, c := range seq {
			fmt.Fprintf(&out, "0x%X,", c)
		}
		fmt.Fprintf(&out, "},%d}", len(seq))
	}
	fmt.Fprint(&out, "var finalSigmaLower = caseMapping")
	mapping(sigma)
	fmt.Fprintln(&out)
	fmt.Fprintln(&out, "var caseEntries = [...]caseEntry{")
	for _, c := range codes {
		e := entries[c]
		fmt.Fprintf(&out, "{0x%X,[4]rune{", c)
		for _, r := range e.simple {
			fmt.Fprintf(&out, "0x%X,", r)
		}
		fmt.Fprint(&out, "},[4]caseMapping{")
		for _, seq := range e.full {
			mapping(seq)
			fmt.Fprint(&out, ",")
		}
		fmt.Fprintln(&out, "}},")
	}
	fmt.Fprintln(&out, "}")
	for i, name := range []string{"alphabeticRanges", "casedRanges", "caseIgnorableRanges", "decimalRanges"} {
		if len(properties[i]) == 0 {
			return nil, fmt.Errorf("missing case property %s", name)
		}
		fmt.Fprintf(&out, "var %s = [...]caseRange{\n", name)
		for _, r := range mergeCaseRanges(properties[i]) {
			fmt.Fprintf(&out, "{0x%X,0x%X},\n", r.lo, r.hi)
		}
		fmt.Fprintln(&out, "}")
	}
	return format.Source(out.Bytes())
}
