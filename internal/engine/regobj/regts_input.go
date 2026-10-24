package regobj

import (
	"context"
	"slices"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

type TextSearchInput struct {
	capture   InputCapture
	qualified *[]ids.ObjectAddress
	resolved  *[]catalog.ExpressionDependency
}

// NewTextSearchInput returns the regconfig and regdictionary resolver over c;
// c.Path is the text-search path, which excludes TEMP.
func NewTextSearchInput(c InputCapture) TextSearchInput { return TextSearchInput{capture: c} }

func (r TextSearchInput) name(ctx context.Context, text string) ([]parser.Identifier, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	raw, err := parser.ParseRegclassName(text)
	if err != nil {
		return nil, "", sqlstate.SoftInputError(sqlError("42602", "invalid name syntax"))
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
func (r TextSearchInput) ResolveRegconfig(ctx context.Context, text string) (uint32, error) {
	if _, _, err := r.name(ctx, text); err != nil {
		return 0, err
	}
	return 0, sqlError("0A000", "text search configuration name lookup is not supported")
}
func (r TextSearchInput) ResolveRegdictionary(ctx context.Context, text string) (uint32, error) {
	parts, spelling, err := r.name(ctx, text)
	if err != nil {
		return 0, err
	}
	a := r.capture.Authority
	selection, err := r.namespaces(parts, false)
	if err != nil {
		return 0, err
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
	return 0, sqlstate.SoftInputError(sqlError("42704", "text search dictionary \""+spelling+"\" does not exist"))
}

// ResolveTextParser shares text-search identifier parsing and captured namespace
// authority without inventing a regparser type or a parser-owner privilege.
func (r TextSearchInput) ResolveTextParser(ctx context.Context, text string) (uint32, error) {
	parts, spelling, err := r.name(ctx, text)
	if err != nil {
		return 0, err
	}
	selection, err := r.namespaces(parts, true)
	if err != nil {
		return 0, err
	}
	p := catalog.DefaultTextParser()
	for i := range selection.Count() {
		ns := selection.At(i)
		if ns.ObjectID == p.Namespace && parts[len(parts)-1].Text == p.Name {
			return uint32(p.OID), nil
		}
	}
	return 0, sqlError("42704", "text search parser \""+spelling+"\" does not exist")
}

// The existing dictionary input preserves its missing-schema soft-input contract;
// parser lookup requires the explicit namespace diagnostic before parser lookup.
func (r TextSearchInput) namespaces(parts []parser.Identifier, requireNamespace bool) (analyze.NamespaceSelection, error) {
	if len(parts) != 2 {
		return analyze.NamespaceSelection{Path: r.capture.Path}, nil
	}
	a := r.capture.Authority
	ns := a.View.Catalog.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: parts[0].Text})
	if parts[0].Text == "pg_temp" {
		ns = r.capture.Temporary
	}
	if ns == (ids.ObjectAddress{}) && requireNamespace {
		return analyze.NamespaceSelection{}, sqlError("3F000", "schema \""+parts[0].Text+"\" does not exist")
	}
	if ns != (ids.ObjectAddress{}) {
		if err := a.View.CanUse(a.Environment, a.View.Ref(ns)); err != nil {
			return analyze.NamespaceSelection{}, err
		}
		if r.qualified != nil && !slices.Contains(*r.qualified, ns) {
			*r.qualified = append(*r.qualified, ns)
		}
	}
	return analyze.NamespaceSelection{Qualified: ns}, nil
}
