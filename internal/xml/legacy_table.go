package xml

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// LegacyCell is an owned string or SQL NULL. A row passed to emit is borrowed
// until emit returns; no XML node or parser buffer leaves this operation.
type LegacyCell struct {
	Text string
	Null bool
}

// LegacyXPathTable owns one invocation's literal path list and accumulated XML
// diagnostics. It is never retained by a prepared expression or shared by calls.
type LegacyXPathTable struct {
	paths       []string
	columns     int
	diagnostics []string
}

func NewLegacyXPathTable(paths string, columns int) (*LegacyXPathTable, error) {
	if columns < 0 {
		return nil, fmt.Errorf("invalid legacy XPath table width")
	}
	n := &LegacyXPathTable{columns: columns}
	for range columns {
		path, rest, more := strings.Cut(paths, "|")
		n.paths = append(n.paths, path)
		if !more {
			break
		}
		paths = rest
	}
	return n, nil
}

// Document parses before compiling, then zips nodesets by index. Scalar strings
// repeat on nodeset-driven rows; unsupported scalar notices also include the
// final exhausted iteration. NULL/unusable documents emit one all-NULL row.
func (n *LegacyXPathTable) Document(ctx context.Context, input *string, emit func([]LegacyCell) error, notice func(int)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cells := make([]LegacyCell, n.columns)
	clearCells := func() {
		for i := range cells {
			cells[i] = LegacyCell{Null: true}
		}
	}
	clearCells()
	if input == nil {
		return emit(cells)
	}
	_, document, events, err := parseConfigured(ctx, *input, Options{Mode: Document, InputKind: RawMemory}, parsePolicy{collect: true, strictNamespaces: true, strictValidity: true, omitDefaultAttributes: true, documentContext: true, legacyDiagnostics: true})
	p := parser{ctx: ctx}
	for _, event := range events {
		n.diagnostics = checkedAppend(&p, n.diagnostics, event.String())
		if p.err != nil {
			return p.err
		}
	}
	if err != nil {
		if legacyDocumentFailure(err) {
			if err := ctx.Err(); err != nil {
				return err
			}
			return emit(cells)
		}
		return err
	}
	if document == nil {
		return fmt.Errorf("missing legacy XPath table document")
	}
	if n.columns == 0 {
		return ctx.Err()
	}
	frame, err := newXPathTree(ctx, document)
	if err != nil {
		return err
	}
	frame.node = nil
	for _, child := range frame.tree.root.children {
		if !frame.poll() {
			return frame.p.err
		}
		if child.kind == xpathElement {
			frame.node = child
			break
		}
	}
	if frame.node == nil {
		return fmt.Errorf("missing legacy XPath table document element")
	}
	for index := 0; ; index++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		clearCells()
		driven := false
		for slot, path := range n.paths {
			compiled, err := compileXPath(ctx, path)
			if err != nil {
				var xp *XPathError
				if !errors.As(err, &xp) || xp.Stage != "compile" {
					return err
				}
				if !appendSize(len(n.diagnostics), len(n.diagnostics)) || !appendSize(len(n.diagnostics)*2, 1) {
					return appendSizeError(len(n.diagnostics), len(n.diagnostics))
				}
				parts := make([]string, 0, len(n.diagnostics)*2+1)
				for _, diagnostic := range n.diagnostics {
					if err := ctx.Err(); err != nil {
						return err
					}
					parts = append(parts, diagnostic, "\n")
				}
				parts = append(parts, xp.Detail)
				detail := p.joinMessage(parts...)
				if p.err != nil {
					return p.err
				}
				return &LegacyXPathError{Detail: detail}
			}
			value := frame.eval(compiled)
			if frame.p.err != nil {
				if err := ctx.Err(); err != nil {
					return err
				}
				var xp *XPathError
				if !errors.As(frame.p.err, &xp) || xp.Stage != "evaluate" {
					return frame.p.err
				}
				if xp.Detail != "" {
					n.diagnostics = checkedAppend(&p, n.diagnostics, xp.Detail)
					if p.err != nil {
						return p.err
					}
				}
				frame.p.err = nil
				continue
			}
			switch value.kind {
			case xpathNodes:
				if index < len(value.nodes) {
					cells[slot] = LegacyCell{Text: frame.stringValue(value.nodes[index])}
					driven = true
				}
			case xpathString:
				cells[slot] = LegacyCell{Text: value.text}
			case xpathNumber, xpathBoolean:
				kind := 3
				if value.kind == xpathBoolean {
					kind = 2
				}
				notice(kind)
				cells[slot] = LegacyCell{Text: "<unsupported/>"}
			default:
				return fmt.Errorf("invalid legacy XPath table result")
			}
			if frame.p.err != nil {
				return frame.p.err
			}
		}
		if !driven {
			return ctx.Err()
		}
		if err := emit(cells); err != nil {
			return err
		}
	}
}
