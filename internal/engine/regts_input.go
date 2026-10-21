package engine

import (
	"context"
	"slices"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/parser"
)

type capturedRegTSInput struct {
	capture   regobj.InputCapture
	qualified *[]ids.ObjectAddress
	resolved  *[]catalog.ExpressionDependency
}

func (r capturedRegTSInput) name(ctx context.Context, text string) ([]parser.Identifier, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	raw, err := parser.ParseRegclassName(text)
	if err != nil {
		return nil, "", sqlError("42602", "invalid name syntax")
	}
	parts := raw.Parts
	names := make([]string, len(parts))
	for i, p := range parts {
		names[i] = p.Text
	}
	spelling := strings.Join(names, ".")
	if len(parts) > 3 {
		return nil, "", sqlError("42601", "improper qualified name (too many dotted names): "+spelling)
	}
	if len(parts) == 3 {
		if parts[0].Text != r.capture.Database {
			return nil, "", sqlError("0A000", "cross-database references are not implemented: "+spelling)
		}
		parts = parts[1:]
	}
	return parts, spelling, nil
}
func (r capturedRegTSInput) ResolveRegconfig(ctx context.Context, text string) (uint32, error) {
	if _, _, err := r.name(ctx, text); err != nil {
		return 0, err
	}
	return 0, sqlError("0A000", "text search configuration name lookup is not supported")
}
func (r capturedRegTSInput) ResolveRegdictionary(ctx context.Context, text string) (uint32, error) {
	parts, spelling, err := r.name(ctx, text)
	if err != nil {
		return 0, err
	}
	a := r.capture.Authority
	selection := analyze.NamespaceSelection{Path: r.capture.Path}
	if len(parts) == 2 {
		ns := a.View.Catalog.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: parts[0].Text})
		if parts[0].Text == "pg_temp" {
			ns = r.capture.Temporary
		}
		if ns != (ids.ObjectAddress{}) {
			if err := a.View.CanUse(a.Environment, a.View.Ref(ns)); err != nil {
				return 0, err
			}
			if r.qualified != nil && !slices.Contains(*r.qualified, ns) {
				*r.qualified = append(*r.qualified, ns)
			}
		}
		selection = analyze.NamespaceSelection{Qualified: ns}
	}
	for i := range selection.Count() {
		ns := selection.At(i)
		address := a.View.Catalog.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ns, Name: parts[len(parts)-1].Text})
		if d, ok := a.View.Catalog.TSDictionary(address); ok {
			if r.resolved != nil {
				ref := a.View.Ref(d.Address())
				*r.resolved = append(*r.resolved, catalog.ExpressionDependency{Ref: ref, Revision: d.Revision()})
			}
			return uint32(d.Address().ObjectID), nil
		}
	}
	return 0, sqlError("42704", "text search dictionary \""+spelling+"\" does not exist")
}
