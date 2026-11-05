package xml

import "math"

type xsltSortValue struct {
	text   string
	number float64
}
type xsltSortDefinition struct {
	instruction         *xsltInstruction
	numeric, descending bool
	lang, caseOrder     string
	values              []xsltSortValue
}

func (r *xsltRuntime) sortNodes(i *xsltInstruction, nodes []*xpathNode, c xsltContext) []*xpathNode {
	if len(nodes) < 2 {
		return nodes
	}
	var definitions []xsltSortDefinition
	for _, child := range i.children {
		if child.op != "sort" {
			continue
		}
		dataType, order := r.avt(child, "data-type", c), r.avt(child, "order", c)
		if dataType != "text" && dataType != "number" && xsltAVTDynamic(child.avts["\x00data-type"]) || order != "ascending" && order != "descending" && xsltAVTDynamic(child.avts["\x00order"]) {
			r.fail()
		}
		definitions = append(definitions, xsltSortDefinition{instruction: child, numeric: dataType == "number", descending: order == "descending", lang: r.avt(child, "lang", c), caseOrder: r.avt(child, "case-order", c)})
	}
	if len(definitions) == 0 {
		return nodes
	}
	f := r.frame
	// Keys see the original selected list, not the evolving sorted list. Secondary
	// key expressions are demanded only when equal preceding keys require them.
	compute := func(index int) {
		d := &definitions[index]
		if d.values != nil {
			return
		}
		d.values = make([]xsltSortValue, len(nodes))
		for at, n := range nodes {
			if !f.poll() {
				return
			}
			next := c
			next.node, next.position, next.size = n, at+1, len(nodes)
			expr := d.instruction.expressions["select"]
			value := xpathValue{nodes: []*xpathNode{n}}
			if expr != nil {
				value = r.evaluate(d.instruction, expr, next)
			}
			d.values[at].text = f.string(value)
			if d.numeric {
				d.values[at].number = f.number(xpathStr(d.values[at].text))
			}
		}
	}
	compare := func(a, b int) int {
		for level := range definitions {
			if !f.poll() {
				return 0
			}
			compute(level)
			d := &definitions[level]
			x, y := d.values[a], d.values[b]
			cmp := 0
			if d.numeric {
				if math.IsNaN(x.number) {
					if !math.IsNaN(y.number) {
						cmp = -1
					}
				} else if math.IsNaN(y.number) || x.number > y.number {
					cmp = 1
				} else if x.number < y.number {
					cmp = -1
				}
			} else {
				var err error
				cmp, err = compareXSLTText(f.p.ctx, d.lang, d.caseOrder, x.text, y.text)
				if err != nil {
					f.p.err = err
					return 0
				}
			}
			if d.descending {
				cmp = -cmp
			}
			if cmp != 0 {
				return cmp
			}
		}
		return 0
	}
	order := make([]int, len(nodes))
	scratch := make([]int, len(nodes))
	for at := range order {
		order[at] = at
	}
	// Bottom-up stable mergesort permits immediate context/error exit; equal keys
	// retain selection order, independently of implementation sorting behavior.
	for width := 1; width < len(order) && f.poll(); {
		for start := 0; start < len(order) && f.poll(); start += 2 * width {
			middle, end := min(start+width, len(order)), min(start+2*width, len(order))
			a, b := start, middle
			for at := start; at < end && f.poll(); at++ {
				if b == end || a < middle && compare(order[a], order[b]) <= 0 {
					scratch[at] = order[a]
					a++
				} else {
					scratch[at] = order[b]
					b++
				}
			}
		}
		order, scratch = scratch, order
		if width > len(order)/2 {
			break
		}
		width *= 2
	}
	if f.p.err != nil {
		return nil
	}
	out := make([]*xpathNode, len(nodes))
	for at, index := range order {
		if !f.poll() {
			return nil
		}
		out[at] = nodes[index]
	}
	return out
}

func xsltAVTDynamic(parts xsltAVT) bool {
	for _, part := range parts {
		if part.expression != nil {
			return true
		}
	}
	return false
}
