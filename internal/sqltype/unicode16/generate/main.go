// Command generate emits deterministic Unicode16 normalization and case tables from the
// original UCD files. No host Unicode tables, network or runtime dependencies.
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type entry struct {
	code          uint32
	ccc           uint8
	compat        bool
	decomposition []uint32
}
type interval struct {
	lo, hi uint32
	value  uint8
}

func hex(s string) (uint32, error) {
	n, e := strconv.ParseUint(strings.TrimSpace(s), 16, 32)
	return uint32(n), e
}
func lines(path string, fn func(string) error) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		if e = fn(s.Text()); e != nil {
			return fmt.Errorf("%s: %w", path, e)
		}
	}
	return s.Err()
}
func generate(source string) ([]byte, error) {
	var entries []entry
	if err := lines(filepath.Join(source, "UnicodeData.txt"), func(line string) error {
		fields := strings.Split(line, ";")
		if len(fields) != 15 {
			return fmt.Errorf("invalid UnicodeData record")
		}
		code, e := hex(fields[0])
		if e != nil {
			return e
		}
		ccc, e := strconv.ParseUint(fields[3], 10, 8)
		if e != nil {
			return e
		}
		mapping := strings.Fields(fields[5])
		compat := len(mapping) > 0 && strings.HasPrefix(mapping[0], "<")
		if compat {
			mapping = mapping[1:]
		}
		if ccc == 0 && len(mapping) == 0 {
			return nil
		}
		x := entry{code: code, ccc: uint8(ccc), compat: compat}
		for _, s := range mapping {
			v, e := hex(s)
			if e != nil {
				return e
			}
			x.decomposition = append(x.decomposition, v)
		}
		entries = append(entries, x)
		return nil
	}); err != nil {
		return nil, err
	}
	exclusions := map[uint32]bool{}
	var properties [4][]interval
	if err := lines(filepath.Join(source, "DerivedNormalizationProps.txt"), func(line string) error {
		raw, _, _ := strings.Cut(line, "#")
		if strings.TrimSpace(raw) == "" {
			return nil
		}
		f := strings.Split(raw, ";")
		if len(f) < 2 {
			return fmt.Errorf("invalid property record")
		}
		bounds := strings.Split(strings.TrimSpace(f[0]), "..")
		lo, e := hex(bounds[0])
		if e != nil {
			return e
		}
		hi := lo
		if len(bounds) == 2 {
			hi, e = hex(bounds[1])
			if e != nil {
				return e
			}
		}
		prop := strings.TrimSpace(f[1])
		if prop == "Full_Composition_Exclusion" {
			for c := lo; c <= hi; c++ {
				exclusions[c] = true
			}
			return nil
		}
		idx := -1
		switch prop {
		case "NFC_QC":
			idx = 0
		case "NFD_QC":
			idx = 1
		case "NFKC_QC":
			idx = 2
		case "NFKD_QC":
			idx = 3
		}
		if idx < 0 {
			return nil
		}
		if len(f) < 3 {
			return fmt.Errorf("missing quickcheck value")
		}
		value := uint8(0)
		switch strings.TrimSpace(f[2]) {
		case "N":
			value = 1
		case "M":
			value = 2
		case "Y":
			return nil
		default:
			return fmt.Errorf("invalid quickcheck value")
		}
		properties[idx] = append(properties[idx], interval{lo, hi, value})
		return nil
	}); err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].code < entries[j].code })
	type composition struct {
		key  uint64
		code uint32
	}
	var compositions []composition
	for _, e := range entries {
		if !e.compat && len(e.decomposition) == 2 && !exclusions[e.code] {
			compositions = append(compositions, composition{uint64(e.decomposition[0])<<32 | uint64(e.decomposition[1]), e.code})
		}
	}
	sort.Slice(compositions, func(i, j int) bool { return compositions[i].key < compositions[j].key })
	var out bytes.Buffer
	fmt.Fprintln(&out, "// Code generated from Unicode16 UCD by ./generate; DO NOT EDIT.")
	fmt.Fprintln(&out, "// Unicode data copyright Unicode, Inc.; see LICENSE.\npackage unicode16\nconst Version = \"16.0.0\"")
	fmt.Fprintln(&out, "var decompositionRunes = [...]rune{")
	for _, e := range entries {
		for _, c := range e.decomposition {
			fmt.Fprintf(&out, "0x%X,", c)
		}
	}
	fmt.Fprintln(&out, "\n}")
	fmt.Fprintln(&out, "var entries = [...]entry{")
	offset := 0
	for _, e := range entries {
		fmt.Fprintf(&out, "{0x%X,%d,%d,%d,%t},\n", e.code, e.ccc, offset, len(e.decomposition), e.compat)
		offset += len(e.decomposition)
	}
	fmt.Fprintln(&out, "}")
	fmt.Fprintln(&out, "var compositions = [...]composition{")
	for _, c := range compositions {
		fmt.Fprintf(&out, "{0x%X,0x%X},\n", c.key, c.code)
	}
	fmt.Fprintln(&out, "}")
	names := []string{"nfcProperties", "nfdProperties", "nfkcProperties", "nfkdProperties"}
	for i, props := range properties {
		sort.Slice(props, func(a, b int) bool { return props[a].lo < props[b].lo })
		fmt.Fprintf(&out, "var %s = [...]property{\n", names[i])
		for _, p := range props {
			fmt.Fprintf(&out, "{0x%X,0x%X,%d},\n", p.lo, p.hi, p.value)
		}
		fmt.Fprintln(&out, "}")
	}
	return format.Source(out.Bytes())
}
func main() {
	source := flag.String("source", "", "directory containing Unicode16 UCD files")
	output := flag.String("output", "", "generated Go file")
	caseOutput := flag.String("case-output", "", "optional generated case Go file")
	flag.Parse()
	if *source == "" || *output == "" || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "-source and -output are required")
		os.Exit(2)
	}
	data, e := generate(*source)
	if e == nil {
		e = os.WriteFile(*output, data, 0644)
	}
	if e == nil && *caseOutput != "" {
		var caseData []byte
		caseData, e = generateCase(*source)
		if e == nil {
			e = os.WriteFile(*caseOutput, caseData, 0644)
		}
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
