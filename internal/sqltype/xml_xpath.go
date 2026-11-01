package sqltype

import (
	"context"
	"errors"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/xml"
)

// XMLXPath borrows input/output environments for this invocation only. Namespace
// shape is checked before the XML provider's empty-path/document/pair precedence.
func XMLXPath(ctx context.Context, target *Descriptor, path string, value Value, mapping Value, exists bool, in InputEnvironment, out OutputEnvironment) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if mapping.ArrayRank() != 0 && (mapping.ArrayRank() != 2 || mapping.ArrayDimension(1).Length != 2) {
		return Value{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22000", Message: "invalid array for XML namespace mapping", Detail: "The array must be two-dimensional with length of the second axis equal to 2."}
	}
	var pairs []xml.XPathMapping
	if mapping.ArrayLen() != 0 {
		pairs = make([]xml.XPathMapping, mapping.ArrayLen()/2)
		for i := range pairs {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
			alias, uri := mapping.ArrayElement(i*2), mapping.ArrayElement(i*2+1)
			pairs[i].Null = alias.IsNull() || uri.IsNull()
			if !alias.IsNull() {
				text, err := alias.Text()
				if err != nil {
					return Value{}, err
				}
				pairs[i].Alias = text
			}
			if !uri.IsNull() {
				text, err := uri.Text()
				if err != nil {
					return Value{}, err
				}
				pairs[i].URI = text
			}
		}
	}
	source, err := value.XML()
	if err != nil {
		return Value{}, err
	}
	result, events, err := xml.XPath(ctx, path, source, pairs, exists)
	xmlWarnings(events, in)
	if err != nil {
		var ns *xml.XPathNamespaceError
		if errors.As(err, &ns) {
			code := "XX000"
			if ns.Null {
				code = "22004"
			}
			return Value{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: ns.Error()}
		}
		return Value{}, xmlInputError(err, in)
	}
	if exists {
		return Boolean(target, result.Exists), nil
	}
	if result.Numeric {
		result.Items = []string{floatOutputWithEnvironment(result.Number, Float8, out)}
	}
	elements := make([]Value, len(result.Items))
	for i, text := range result.Items {
		// Generated XML is CONTENT, independently of the session's input option.
		elements[i], err = XMLInput(ctx, target.Element(), text, XMLContent, InputEnvironment{})
		if err != nil {
			return Value{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	var dimensions []ArrayDimension
	if len(elements) != 0 {
		dimensions = []ArrayDimension{{Lower: 1, Length: int32(len(elements))}}
	}
	array, err := OwnedArrayValue(target, dimensions, elements)
	if err != nil {
		return Value{}, err
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	return array, nil
}
