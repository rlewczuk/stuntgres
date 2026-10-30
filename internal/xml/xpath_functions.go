package xml

import (
	"math"
	"strings"
	"unicode/utf8"
)

func xpathRound(n float64) float64 {
	if n >= -0.5 && n < 0 {
		return math.Copysign(0, -1)
	}
	if math.IsNaN(n) || math.IsInf(n, 0) || n == 0 {
		return n
	}
	floor := math.Floor(n)
	if n-floor >= 0.5 {
		return floor + 1
	}
	return floor
}
func (f *xpathFrame) call(e *xpathExpr) xpathValue {
	var inline [3]xpathValue
	args := inline[:min(len(e.args), len(inline))]
	if len(e.args) > len(inline) {
		args = make([]xpathValue, len(e.args))
	}
	for i, a := range e.args {
		args[i] = f.eval(a)
		if !f.poll() {
			return xpathValue{}
		}
	}
	var contextNode [1]*xpathNode
	contextNode[0] = f.node
	context := xpathValue{nodes: contextNode[:]}
	first := context
	if len(args) > 0 {
		first = args[0]
	}
	// libxml demands argument expressions before resolving the function or arity.
	if prefix, _, qualified := strings.Cut(e.name, ":"); qualified {
		if _, resolved := f.namespaceURI(prefix); !resolved {
			f.runtimeError("")
			return xpathValue{}
		}
	}
	minimum, maximum := 1, 1
	switch e.name {
	case "last", "position", "true", "false":
		minimum, maximum = 0, 0
	case "local-name", "namespace-uri", "name", "string", "string-length", "normalize-space", "number":
		minimum = 0
	case "concat":
		minimum, maximum = 2, int(^uint(0)>>1)
	case "starts-with", "contains", "substring-before", "substring-after":
		minimum, maximum = 2, 2
	case "substring":
		minimum, maximum = 2, 3
	case "translate":
		minimum, maximum = 3, 3
	case "count", "id", "boolean", "not", "lang", "sum", "floor", "ceiling", "round":
	default:
		f.runtimeError("Unregistered function")
		return xpathValue{}
	}
	if len(e.args) < minimum || len(e.args) > maximum {
		f.runtimeError("Invalid number of arguments")
		return xpathValue{}
	}
	switch e.name {
	case "last":
		return xpathNum(float64(f.size))
	case "position":
		return xpathNum(float64(f.position))
	case "true":
		return xpathBool(true)
	case "false":
		return xpathBool(false)
	case "count":
		if first.kind != xpathNodes {
			f.runtimeError("Invalid type")
			return xpathValue{}
		}
		return xpathNum(float64(len(first.nodes)))
	case "local-name", "namespace-uri", "name":
		if first.kind != xpathNodes {
			f.runtimeError("Invalid type")
			return xpathValue{}
		}
		if len(first.nodes) == 0 {
			return xpathStr("")
		}
		n := first.nodes[0]
		switch e.name {
		case "name":
			return xpathStr(n.name)
		case "namespace-uri":
			return xpathStr(n.uri)
		default:
			return xpathStr(n.local)
		}
	case "string":
		return xpathStr(f.string(first))
	case "number":
		return xpathNum(f.number(first))
	case "boolean":
		return xpathBool(f.boolean(first))
	case "not":
		return xpathBool(!f.boolean(first))
	case "floor":
		return xpathNum(math.Floor(f.number(first)))
	case "ceiling":
		return xpathNum(math.Ceil(f.number(first)))
	case "round":
		return xpathNum(xpathRound(f.number(first)))
	case "sum":
		if first.kind != xpathNodes {
			f.runtimeError("Invalid type")
			return xpathValue{}
		}
		sum := 0.0
		for _, n := range first.nodes {
			if !f.poll() {
				break
			}
			sum += f.number(xpathStr(f.stringValue(n)))
		}
		return xpathNum(sum)
	case "id":
		var out []*xpathNode
		add := func(s string) {
			for i := 0; i < len(s) && f.poll(); {
				for i < len(s) && space(s[i]) && f.poll() {
					i++
				}
				start := i
				for i < len(s) && !space(s[i]) && f.poll() {
					i++
				}
				if n := f.tree.ids[s[start:i]]; n != nil {
					out = checkedAppend(&f.p, out, n)
				}
			}
		}
		if first.kind == xpathNodes {
			for _, n := range first.nodes {
				if !f.poll() {
					break
				}
				add(f.stringValue(n))
			}
		} else {
			add(f.string(first))
		}
		return xpathValue{nodes: f.normalize(out)}
	case "lang":
		requested := f.string(first)
		for n := f.node; n != nil && f.poll(); n = n.parent {
			if n.kind != xpathElement {
				continue
			}
			for _, a := range n.attributes {
				if !f.poll() {
					return xpathValue{}
				}
				if a.uri == xmlNamespaceURI && a.local == "lang" {
					language := a.value
					return xpathBool(f.equalFoldASCII(language, requested) || len(language) > len(requested) && language[len(requested)] == '-' && f.equalFoldASCII(language[:len(requested)], requested))
				}
			}
		}
		return xpathBool(false)
	case "concat":
		var b strings.Builder
		for _, v := range args {
			if !f.poll() {
				break
			}
			f.p.appendValue(&b, f.string(v))
		}
		return xpathStr(b.String())
	case "string-length":
		s := f.string(first)
		count := 0
		for range s {
			if !f.poll() {
				break
			}
			count++
		}
		return xpathNum(float64(count))
	case "normalize-space":
		s := f.string(first)
		var b strings.Builder
		pending := false
		for _, r := range s {
			if !f.poll() {
				break
			}
			if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
				pending = b.Len() != 0
				continue
			}
			if pending {
				f.p.builderByte(&b, ' ')
				pending = false
			}
			f.p.appendScalar(&b, r)
		}
		return xpathStr(b.String())
	case "translate":
		source, from, to := f.string(first), f.string(args[1]), f.string(args[2])
		mapping := make(map[rune]rune)
		deleted := make(map[rune]bool)
		toAt := 0
		for _, r := range from {
			if !f.poll() {
				break
			}
			replacement := rune(-1)
			if toAt < len(to) {
				var w int
				replacement, w = utf8.DecodeRuneInString(to[toAt:])
				toAt += w
			}
			if _, found := mapping[r]; found || deleted[r] {
				continue
			}
			if replacement < 0 {
				deleted[r] = true
			} else {
				mapping[r] = replacement
			}
		}
		var b strings.Builder
		for _, r := range source {
			if !f.poll() {
				break
			}
			if deleted[r] {
				continue
			}
			if replacement, ok := mapping[r]; ok {
				r = replacement
			}
			f.p.appendScalar(&b, r)
		}
		return xpathStr(b.String())
	case "substring":
		s := f.string(first)
		start := xpathRound(f.number(args[1]))
		end := math.Inf(1)
		if len(args) == 3 {
			end = start + xpathRound(f.number(args[2]))
		}
		var b strings.Builder
		position := 1.0
		for _, r := range s {
			if !f.poll() {
				break
			}
			if position >= start && position < end {
				f.p.appendScalar(&b, r)
			}
			position++
		}
		return xpathStr(b.String())
	case "starts-with", "contains", "substring-before", "substring-after":
		s, needle := f.string(first), f.string(args[1])
		if e.name == "starts-with" {
			return xpathBool(len(s) >= len(needle) && f.p.namespaceEqual(s[:len(needle)], needle))
		}
		index := f.find(s, needle)
		switch e.name {
		case "contains":
			return xpathBool(index >= 0)
		case "substring-before":
			if index < 0 {
				return xpathStr("")
			}
			return xpathStr(s[:index])
		default:
			if index < 0 {
				return xpathStr("")
			}
			return xpathStr(s[index+len(needle):])
		}
	}
	f.runtimeError("Unregistered function")
	return xpathValue{}
}

// Chunked searching keeps even one huge string operation interruptible without
// copying its input. Overlap preserves matches crossing chunk boundaries.
func (f *xpathFrame) find(s, needle string) int {
	if needle == "" {
		return 0
	}
	if len(needle) > len(s) {
		return -1
	}
	if len(needle) > 4096 {
		// KMP keeps a long needle from making a single library search unbounded.
		var prefix []int
		for i := range len(needle) {
			if !f.poll() {
				return -1
			}
			matched := 0
			if i > 0 {
				matched = prefix[i-1]
				for matched > 0 && needle[i] != needle[matched] && f.poll() {
					matched = prefix[matched-1]
				}
				if needle[i] == needle[matched] {
					matched++
				}
			}
			prefix = checkedAppend(&f.p, prefix, matched)
		}
		matched := 0
		for i := range len(s) {
			if !f.poll() {
				return -1
			}
			for matched > 0 && s[i] != needle[matched] && f.poll() {
				matched = prefix[matched-1]
			}
			if s[i] == needle[matched] {
				matched++
			}
			if matched == len(needle) {
				return i + 1 - matched
			}
		}
		return -1
	}
	for start := 0; start <= len(s)-len(needle) && f.poll(); {
		end := min(len(s), start+4096+len(needle)-1)
		if i := strings.Index(s[start:end], needle); i >= 0 {
			return start + i
		}
		start += 4096
	}
	return -1
}

func (f *xpathFrame) equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range len(a) {
		if i&4095 == 0 && !f.poll() {
			return false
		}
		x, y := a[i], b[i]
		if x >= 'A' && x <= 'Z' {
			x += 'a' - 'A'
		}
		if y >= 'A' && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}
