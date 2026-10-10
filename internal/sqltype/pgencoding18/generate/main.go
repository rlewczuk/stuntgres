// Command generate emits PostgreSQL REL_18_0 directional tables from vendored
// release inputs. Run from the repository root: go run ./internal/sqltype/pgencoding18/generate
package main

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const base = "internal/sqltype/pgencoding18/"

var comments = regexp.MustCompile(`(?s)/\*.*?\*/`)
var numbers = regexp.MustCompile(`0[xX][0-9a-fA-F]+|[0-9]+`)
var arrays = regexp.MustCompile(`(?s)static const (uint16|uint32|unsigned char|unsigned short|codes_t|pg_utf_to_local_combined|pg_local_to_utf_combined|struct\s*\{.*?\})\s+(\w+)\s*(\[[^=;]*)=\s*\{(.*?)\};`)
var trees = regexp.MustCompile(`(?s)static const pg_mb_radix_tree\s+(\w+)\s*=\s*\{(.*?)\};`)
var names = strings.Fields("SQL_ASCII EUC_JP EUC_CN EUC_KR EUC_TW EUC_JIS_2004 UTF8 MULE_INTERNAL LATIN1 LATIN2 LATIN3 LATIN4 LATIN5 LATIN6 LATIN7 LATIN8 LATIN9 LATIN10 WIN1256 WIN1258 WIN866 WIN874 KOI8R WIN1251 WIN1252 ISO_8859_5 ISO_8859_6 ISO_8859_7 ISO_8859_8 WIN1250 WIN1253 WIN1254 WIN1255 WIN1257 KOI8U SJIS BIG5 GBK UHC GB18030 JOHAB SHIFT_JIS_2004")

func must(err error) {
	if err != nil {
		panic(err)
	}
}
func ints(s string) []uint32 {
	s = comments.ReplaceAllString(s, "")
	toks := numbers.FindAllString(s, -1)
	remain := numbers.ReplaceAllString(s, "")
	if strings.Trim(remain, " \t\r\n{},") != "" {
		panic("unsupported initializer: " + remain)
	}
	out := make([]uint32, len(toks))
	for i, t := range toks {
		v, e := strconv.ParseUint(t, 0, 32)
		must(e)
		out[i] = uint32(v)
	}
	return out
}
func id(s string) int {
	s = strings.TrimPrefix(s, "PG_")
	for i, n := range names {
		if n == s {
			return i
		}
	}
	panic("unknown encoding " + s)
}

// Validate every reachable radix index without enumerating character products.
func validateTree(table, descriptor []uint32) {
	k := 0
	for n := 1; n <= 4; n++ {
		root := descriptor[k]
		k++
		bounds := descriptor[k : k+2*n]
		k += 2 * n
		nodes := map[uint32]bool{root: true}
		for depth := range n {
			lo, hi := bounds[2*depth], bounds[2*depth+1]
			if lo > hi || hi > 255 {
				panic("invalid radix bounds")
			}
			next := map[uint32]bool{}
			for node := range nodes {
				if uint64(node)+uint64(hi-lo) >= uint64(len(table)) {
					panic("radix index out of bounds")
				}
				if depth < n-1 {
					for b := lo; b <= hi; b++ {
						next[table[node+b-lo]] = true
					}
				}
			}
			nodes = next
		}
	}
}
func main() {
	var out bytes.Buffer
	fmt.Fprintln(&out, "// Code generated from PostgreSQL REL_18_0 release inputs; DO NOT EDIT.\n// Source: https://www.postgresql.org/ftp/source/v18.0/\n// Licensing: see data/COPYRIGHT and LICENSE.\npackage pgencoding18")
	files, e := filepath.Glob(base + "data/src/backend/utils/mb/Unicode/*.map")
	must(e)
	direct := []string{"cyrillic_and_mic/cyrillic_and_mic.c", "latin2_and_win1250/latin2_and_win1250.c", "euc_tw_and_big5/big5.c", "euc_jp_and_sjis/sjis.map"}
	for _, f := range direct {
		files = append(files, base+"data/src/backend/utils/mb/conversion_procs/"+f)
	}
	declared := map[string]int{}
	tableValues := map[string][]uint32{}
	treeNames := map[string]bool{}
	combined := map[string]string{}
	for _, file := range files {
		data, e := os.ReadFile(file)
		must(e)
		s := string(data)
		for _, a := range arrays.FindAllStringSubmatch(s, -1) {
			typ, name := a[1], a[2]
			vals := ints(a[4])
			width := 1
			gotype := "uint32"
			switch typ {
			case "uint32":
			case "uint16", "unsigned short":
				gotype = "uint16"
			case "unsigned char":
				gotype = "byte"
			case "codes_t":
				width = 2
				gotype = "[2]uint32"
			case "pg_utf_to_local_combined", "pg_local_to_utf_combined":
				width = 3
				gotype = "[3]uint32"
			default:
				if strings.HasPrefix(typ, "struct") {
					width = 3
					gotype = "[3]uint32"
				} else {
					panic(typ)
				}
			}
			if name == "b1c4" || name == "b2c3" {
				width = 2
				gotype = "[2]uint32"
			}
			if strings.Contains(name, "combined") {
				combined[name] = typ
			}
			if len(vals)%width != 0 {
				panic("wrong record width " + name)
			}
			dimensions := regexp.MustCompile(`\[(\d*)\]`).FindAllStringSubmatch(a[3], -1)
			if len(dimensions) == 0 {
				panic("missing array dimensions")
			}
			if dimensions[0][1] != "" {
				expected, e := strconv.Atoi(dimensions[0][1])
				must(e)
				if expected != len(vals)/width {
					panic("wrong array length " + name)
				}
			}
			if len(dimensions) > 1 && (len(dimensions) != 2 || dimensions[1][1] != "2" || width != 2) {
				panic("unsupported array dimensions " + name)
			}
			for _, v := range vals {
				if gotype == "byte" && v > 255 || gotype == "uint16" && v > 65535 {
					panic("table value out of bounds " + name)
				}
			}
			if typ == "unsigned char" && len(vals) != 128 {
				panic("wrong direct table length " + name)
			}
			tableValues[name] = vals
			if _, ok := declared[name]; ok {
				panic("duplicate table " + name)
			}
			declared[name] = len(vals) / width
			fmt.Fprintf(&out, "var %s = [...]%s{", name, gotype)
			for i, v := range vals {
				if width > 1 && i%width == 0 {
					fmt.Fprint(&out, "{")
				}
				fmt.Fprintf(&out, "0x%x,", v)
				if width > 1 && i%width == width-1 {
					fmt.Fprint(&out, "},")
				}
			}
			fmt.Fprintln(&out, "}")
		}
		for _, t := range trees.FindAllStringSubmatch(s, -1) {
			body := comments.ReplaceAllString(t[2], "")
			parts := strings.Split(body, ",")
			if len(parts) != 26 {
				panic(fmt.Sprintf("wrong radix descriptor %s: %d", t[1], len(parts)))
			}
			p16, p32 := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
			tab := p32
			if tab == "NULL" {
				tab = p16
			}
			size, ok := declared[tab]
			if !ok {
				panic("missing table " + tab)
			}
			vals := ints(strings.Join(parts[2:], ","))
			if len(vals) != 24 {
				panic("wrong radix bounds")
			}
			validateTree(tableValues[tab], vals)
			fmt.Fprintf(&out, "var %s = radix{", t[1])
			if p32 != "NULL" {
				fmt.Fprintf(&out, "wide:%s[:],", tab)
			} else {
				fmt.Fprintf(&out, "narrow:%s[:],", tab)
			}
			k := 0
			fmt.Fprint(&out, "levels:[4]level{")
			for n := 1; n <= 4; n++ {
				root := vals[k]
				k++
				if int(root) >= size {
					panic("radix root out of bounds")
				}
				fmt.Fprintf(&out, "{root:0x%x, bounds:[4][2]byte{", root)
				for range n {
					lo, hi := vals[k], vals[k+1]
					k += 2
					if lo > hi || hi > 255 {
						panic("invalid radix bound")
					}
					fmt.Fprintf(&out, "{0x%x,0x%x},", lo, hi)
				}
				fmt.Fprint(&out, "}},")
			}
			fmt.Fprintln(&out, "}}")
			treeNames[t[1]] = true
		}
	}
	if len(treeNames) != 76 || len(declared) != 101 {
		panic("incomplete release table inventory")
	}
	if declared["b1c4"] != 4 || declared["b2c3"] != 7 {
		panic("wrong BIG5 exception inventory")
	}
	ibm := tableValues["ibmkanji"]
	if len(ibm) < 3 || ibm[len(ibm)-3] != 0xffff || ibm[len(ibm)-2] != 0xffff || ibm[len(ibm)-1] != 0xffff {
		panic("missing IBM table sentinel")
	}
	cat, e := os.ReadFile(base + "data/src/include/catalog/pg_conversion.dat")
	must(e)
	records := regexp.MustCompile(`(?s)\{[^}]*conforencoding => '(PG_\w+)'[^}]*contoencoding => '(PG_\w+)'[^}]*conproc => '(\w+)'[^}]*\}`).FindAllStringSubmatch(string(cat), -1)
	if len(records) != 128 {
		panic("expected exactly 128 pairs")
	}
	fmt.Fprintln(&out, "var pairs = [42][42]bool{")
	seen := map[[2]int]bool{}
	rows := make([][]int, 42)
	for _, r := range records {
		a, b := id(r[1]), id(r[2])
		key := [2]int{a, b}
		if seen[key] {
			panic("duplicate pair")
		}
		seen[key] = true
		rows[a] = append(rows[a], b)
	}
	for a, row := range rows {
		if len(row) == 0 {
			continue
		}
		fmt.Fprintf(&out, "%d:{", a)
		for b := range 42 {
			if seen[[2]int{a, b}] {
				fmt.Fprintf(&out, "%d:true,", b)
			}
		}
		fmt.Fprintln(&out, "},")
	}
	fmt.Fprintln(&out, "}")
	fmt.Fprintln(&out, "var unicodeTrees = [42][2]*radix{")
	aliases := map[string]string{"EUC_JIS_2004": "euc_jis_2004", "SHIFT_JIS_2004": "shift_jis_2004", "ISO_8859_5": "iso8859_5", "ISO_8859_6": "iso8859_6", "ISO_8859_7": "iso8859_7", "ISO_8859_8": "iso8859_8"}
	for i, iso := range []int{1, 2, 3, 4, 9, 10, 13, 14, 15, 16} {
		aliases[names[8+i]] = fmt.Sprintf("iso8859_%d", iso)
	}
	for i, n := range names {
		if i == 0 || i == 6 || i == 7 || i == 8 {
			continue
		}
		stem := strings.ToLower(n)
		if a, ok := aliases[n]; ok {
			stem = a
		}
		to, from := stem+"_to_unicode_tree", stem+"_from_unicode_tree"
		if !treeNames[to] || !treeNames[from] {
			panic("missing directional trees " + stem)
		}
		fmt.Fprintf(&out, "%d:{&%s,&%s},\n", i, to, from)
	}
	fmt.Fprintln(&out, "}")
	fmt.Fprintln(&out, "var unicodeCombined = [42][2][][3]uint32{")
	for _, i := range []int{5, 41} {
		to, from := "LUmap"+names[i]+"_combined", "ULmap"+names[i]+"_combined"
		if _, ok := combined[to]; !ok {
			panic("missing combined " + to)
		}
		if _, ok := combined[from]; !ok {
			panic("missing combined " + from)
		}
		if declared[to] != 25 || declared[from] != 25 {
			panic("wrong combined inventory")
		}
		fmt.Fprintf(&out, "%d:{%s[:],%s[:]},\n", i, to, from)
	}
	fmt.Fprintln(&out, "}")
	formatted, e := format.Source(out.Bytes())
	must(e)
	must(os.WriteFile(base+"tables_generated.go", formatted, 0644))
}
