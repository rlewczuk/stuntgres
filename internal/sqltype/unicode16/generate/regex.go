package main

import (
	"bytes"
	"fmt"
	"go/format"
	"path/filepath"
	"strings"
)

func generateRegex(source string) ([]byte, error) {
	var properties [7][]interval
	add := func(lo, hi uint32, category string) {
		r := interval{lo: lo, hi: hi}
		if category != "Cc" && category != "Cs" && category != "Cn" {
			properties[2] = append(properties[2], r)
		}
		if strings.HasPrefix(category, "Z") {
			properties[3] = append(properties[3], r)
		}
		if category == "Zs" {
			properties[4] = append(properties[4], r)
		}
		if strings.HasPrefix(category, "P") {
			properties[5] = append(properties[5], r)
		}
		if strings.HasPrefix(category, "S") {
			properties[6] = append(properties[6], r)
		}
	}
	var first uint32
	var firstCategory string
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
			if firstCategory != "" {
				return fmt.Errorf("nested UnicodeData range")
			}
			first = c
			firstCategory = f[2]
			return nil
		}
		if strings.HasSuffix(f[1], ", Last>") {
			if firstCategory != f[2] || c < first {
				return fmt.Errorf("unmatched UnicodeData range")
			}
			add(first, c, f[2])
			firstCategory = ""
			return nil
		}
		if firstCategory != "" {
			return fmt.Errorf("unterminated UnicodeData range")
		}
		add(c, c, f[2])
		return nil
	}); err != nil {
		return nil, err
	}
	if firstCategory != "" {
		return nil, fmt.Errorf("unterminated UnicodeData range")
	}
	if err := lines(filepath.Join(source, "DerivedCoreProperties.txt"), func(line string) error {
		f := caseFields(line)
		if f == nil {
			return nil
		}
		if len(f) < 2 {
			return fmt.Errorf("invalid derived property")
		}
		i := -1
		if f[1] == "Lowercase" {
			i = 0
		}
		if f[1] == "Uppercase" {
			i = 1
		}
		if i < 0 {
			return nil
		}
		r, err := caseBounds(f[0])
		if err != nil {
			return err
		}
		properties[i] = append(properties[i], r)
		return nil
	}); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	fmt.Fprintln(&out, "// Code generated from pinned Unicode16 UCD by generate; DO NOT EDIT.\npackage unicode16")
	names := [...]string{"regexLowercaseRanges", "regexUppercaseRanges", "regexAssignedGraphicRanges", "regexSeparatorRanges", "regexSpaceSeparatorRanges", "regexPunctuationRanges", "regexSymbolRanges"}
	for i, name := range names {
		fmt.Fprintf(&out, "var %s = [...]caseRange{\n", name)
		for _, r := range mergeCaseRanges(properties[i]) {
			fmt.Fprintf(&out, "{0x%X,0x%X},\n", r.lo, r.hi)
		}
		fmt.Fprintln(&out, "}")
	}
	return format.Source(out.Bytes())
}
