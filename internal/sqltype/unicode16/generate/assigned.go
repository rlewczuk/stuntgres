package main

import (
	"bytes"
	"fmt"
	"go/format"
	"path/filepath"
	"strings"
)

func assignedData(source string) ([]interval, error) {
	var ranges []interval
	var first *interval
	var firstName, firstCategory string
	var previous uint32
	seen := false
	err := lines(filepath.Join(source, "UnicodeData.txt"), func(line string) error {
		f := strings.Split(line, ";")
		if len(f) != 15 {
			return fmt.Errorf("invalid UnicodeData record")
		}
		c, err := caseCode(f[0])
		if err != nil {
			return err
		}
		if seen && c <= previous {
			return fmt.Errorf("unordered UnicodeData record")
		}
		previous, seen = c, true
		category := f[2]
		switch category {
		case "Lu", "Ll", "Lt", "Lm", "Lo", "Mn", "Mc", "Me", "Nd", "Nl", "No", "Pc", "Pd", "Ps", "Pe", "Pi", "Pf", "Po", "Sm", "Sc", "Sk", "So", "Zs", "Zl", "Zp", "Cc", "Cf", "Cs", "Co", "Cn":
		default:
			return fmt.Errorf("invalid UnicodeData category %q", category)
		}
		if strings.HasSuffix(f[1], ", First>") {
			if first != nil {
				return fmt.Errorf("nested UnicodeData range")
			}
			first = &interval{lo: c}
			firstName = strings.TrimSuffix(f[1], ", First>")
			firstCategory = category
			return nil
		}
		if strings.HasSuffix(f[1], ", Last>") {
			if first == nil || first.lo >= c || firstCategory != category || firstName != strings.TrimSuffix(f[1], ", Last>") {
				return fmt.Errorf("unmatched UnicodeData range")
			}
			if category != "Cn" {
				ranges = append(ranges, interval{lo: first.lo, hi: c})
			}
			first = nil
			return nil
		}
		if first != nil {
			return fmt.Errorf("unterminated UnicodeData range")
		}
		if category != "Cn" {
			ranges = append(ranges, interval{lo: c, hi: c})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if first != nil {
		return nil, fmt.Errorf("unterminated UnicodeData range")
	}
	return mergeCaseRanges(ranges), nil
}

func generateAssigned(source string) ([]byte, error) {
	ranges, err := assignedData(source)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	fmt.Fprintln(&out, "// Code generated from Unicode16 UCD by ./generate; DO NOT EDIT.")
	fmt.Fprintln(&out, "// Unicode data copyright Unicode, Inc.; see LICENSE.\npackage unicode16")
	fmt.Fprintln(&out, "var assignedRanges = [...]caseRange{")
	for _, r := range ranges {
		fmt.Fprintf(&out, "{0x%X,0x%X},\n", r.lo, r.hi)
	}
	fmt.Fprintln(&out, "}")
	return format.Source(out.Bytes())
}
