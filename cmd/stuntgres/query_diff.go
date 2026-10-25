package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
)

const (
	maxDiffLines = 50
	maxDiffCells = 1 << 20 // larger unmatched spans are compared by position
	maxDiffShown = 120     // bytes of a message summary
)

// ignoredDiffFields always differ between two servers for reasons unrelated
// to SQL behavior: the server source location and the notifying backend PID.
var ignoredDiffFields = map[string][]string{
	"E": {"file", "line", "routine"},
	"N": {"file", "line", "routine"},
	"A": {"pid"},
}

// diffMessage is a backend message in comparable form: its readable form as
// generic JSON with ignored fields removed (raw data when malformed).
type diffMessage struct {
	typ, name string
	form      any
	key       string // type plus canonical form; equal keys are equal messages
}

func diffForm(m queryMessage) diffMessage {
	var form any = map[string]any{"data": m.Data}
	if m.Decoded != nil {
		if raw, err := json.Marshal(m.Decoded); err == nil {
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.UseNumber()
			var decoded any
			if decoder.Decode(&decoded) == nil {
				form = decoded
			}
		}
	}
	if fields, ok := form.(map[string]any); ok {
		for _, name := range ignoredDiffFields[m.Type] {
			delete(fields, name)
		}
	}
	return diffMessage{typ: m.Type, name: m.Name, form: form, key: m.Type + compactJSON(form)}
}

// responseDiff describes how the reference response differs from stuntgres's
// in readable lines; it aligns equal messages first, then pairs messages of
// the same type and compares their fields.
type responseDiff struct {
	native, reference []diffMessage
	lines             []string
	total             int
}

// diffResponses returns nil when the responses are equal.
func diffResponses(native, reference []queryMessage) []string {
	d := &responseDiff{}
	for _, m := range native {
		d.native = append(d.native, diffForm(m))
	}
	for _, m := range reference {
		d.reference = append(d.reference, diffForm(m))
	}
	a0, a1, b0, b1 := 0, len(d.native), 0, len(d.reference)
	for a0 < a1 && b0 < b1 && d.native[a0].key == d.reference[b0].key {
		a0, b0 = a0+1, b0+1
	}
	for a1 > a0 && b1 > b0 && d.native[a1-1].key == d.reference[b1-1].key {
		a1, b1 = a1-1, b1-1
	}
	pairs, ok := lcsPairs(d.keys(d.native[a0:a1]), d.keys(d.reference[b0:b1]))
	if !ok {
		d.positional(a0, a1, b0, b1)
	} else {
		ai, bi := a0, b0
		for _, p := range pairs {
			d.run(ai, a0+p[0], bi, b0+p[1])
			ai, bi = a0+p[0]+1, b0+p[1]+1
		}
		d.run(ai, a1, bi, b1)
	}
	if d.total > len(d.lines) {
		d.lines = append(d.lines, fmt.Sprintf("… %d more differences", d.total-len(d.lines)))
	}
	return d.lines
}

func (d *responseDiff) keys(messages []diffMessage) []string {
	keys := make([]string, len(messages))
	for i, m := range messages {
		keys[i] = m.key
	}
	return keys
}

// run handles a span with no equal messages: same-type messages are paired
// in order and compared field by field; the rest exist on one side only.
func (d *responseDiff) run(a0, a1, b0, b1 int) {
	types := func(messages []diffMessage) []string {
		out := make([]string, len(messages))
		for i, m := range messages {
			out[i] = m.typ
		}
		return out
	}
	pairs, ok := lcsPairs(types(d.native[a0:a1]), types(d.reference[b0:b1]))
	if !ok {
		d.positional(a0, a1, b0, b1)
		return
	}
	ai, bi := a0, b0
	for _, p := range pairs {
		d.only(ai, a0+p[0], bi, b0+p[1])
		d.pair(a0+p[0], b0+p[1])
		ai, bi = a0+p[0]+1, b0+p[1]+1
	}
	d.only(ai, a1, bi, b1)
}

// positional compares a span too large to align, message by message.
func (d *responseDiff) positional(a0, a1, b0, b1 int) {
	for i, j := a0, b0; i < a1 || j < b1; i, j = i+1, j+1 {
		switch {
		case i < a1 && j < b1 && d.native[i].typ == d.reference[j].typ:
			d.pair(i, j)
		default:
			d.only(i, min(i+1, a1), j, min(j+1, b1))
		}
	}
}

func (d *responseDiff) only(a0, a1, b0, b1 int) {
	for i := a0; i < a1; i++ {
		d.add(fmt.Sprintf("stuntgres only #%d %s", i, summary(d.native[i])))
	}
	for j := b0; j < b1; j++ {
		d.add(fmt.Sprintf("reference only #%d %s", j, summary(d.reference[j])))
	}
}

func (d *responseDiff) pair(i, j int) {
	a, b := d.native[i], d.reference[j]
	if a.key == b.key {
		return
	}
	label := fmt.Sprintf("#%d", i)
	if i != j {
		label += fmt.Sprintf(" (reference #%d)", j)
	}
	label += " " + a.name
	compareFields("", a.form, b.form, func(path, x, y string) {
		d.add(fmt.Sprintf("%s %s: stuntgres %s, reference %s", label, path, x, y))
	})
}

func (d *responseDiff) add(line string) {
	d.total++
	if len(d.lines) < maxDiffLines {
		d.lines = append(d.lines, line)
	}
}

// compareFields reports every differing leaf as a dotted/indexed path.
func compareFields(path string, x, y any, emit func(path, x, y string)) {
	xm, xMap := x.(map[string]any)
	ym, yMap := y.(map[string]any)
	if xMap && yMap {
		names := make([]string, 0, len(xm)+len(ym))
		for name := range xm {
			names = append(names, name)
		}
		for name := range ym {
			if _, ok := xm[name]; !ok {
				names = append(names, name)
			}
		}
		slices.Sort(names)
		for _, name := range names {
			child := name
			if path != "" {
				child = path + "." + name
			}
			xv, xok := xm[name]
			yv, yok := ym[name]
			if !xok || !yok {
				emit(child, presence(xv, xok), presence(yv, yok))
				continue
			}
			compareFields(child, xv, yv, emit)
		}
		return
	}
	xs, xList := x.([]any)
	ys, yList := y.([]any)
	if xList && yList {
		for i := range max(len(xs), len(ys)) {
			child := path + "[" + strconv.Itoa(i) + "]"
			if i >= len(xs) || i >= len(ys) {
				emit(child, presence(index(xs, i)), presence(index(ys, i)))
				continue
			}
			compareFields(child, xs[i], ys[i], emit)
		}
		return
	}
	if xv, yv := compactJSON(x), compactJSON(y); xv != yv {
		if path == "" {
			path = "value"
		}
		emit(path, xv, yv)
	}
}

func index(values []any, i int) (any, bool) {
	if i < len(values) {
		return values[i], true
	}
	return nil, false
}

func presence(value any, ok bool) string {
	if !ok {
		return "<absent>"
	}
	return compactJSON(value)
}

// summary names a one-sided message with its most telling content.
func summary(m diffMessage) string {
	fields, _ := m.form.(map[string]any)
	text := ""
	switch m.typ {
	case "E", "N":
		text = fmt.Sprintf("%v %v", fields["code"], fields["message"])
	case "C":
		text = fmt.Sprint(fields["tag"])
	default:
		text = compactJSON(m.form)
	}
	if len(text) > maxDiffShown {
		text = text[:maxDiffShown] + "…"
	}
	return m.name + " " + text
}

func compactJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(raw)
}

// lcsPairs returns index pairs of a longest common subsequence, or false
// when the span is too large to align.
func lcsPairs(x, y []string) ([][2]int, bool) {
	if len(x) == 0 || len(y) == 0 {
		return nil, true
	}
	if len(x)*len(y) > maxDiffCells {
		return nil, false
	}
	width := len(y) + 1
	table := make([]int32, (len(x)+1)*width)
	for i := len(x) - 1; i >= 0; i-- {
		for j := len(y) - 1; j >= 0; j-- {
			if x[i] == y[j] {
				table[i*width+j] = table[(i+1)*width+j+1] + 1
			} else {
				table[i*width+j] = max(table[(i+1)*width+j], table[i*width+j+1])
			}
		}
	}
	var pairs [][2]int
	for i, j := 0, 0; i < len(x) && j < len(y); {
		switch {
		case x[i] == y[j]:
			pairs = append(pairs, [2]int{i, j})
			i, j = i+1, j+1
		case table[(i+1)*width+j] >= table[i*width+j+1]:
			i++
		default:
			j++
		}
	}
	return pairs, true
}
