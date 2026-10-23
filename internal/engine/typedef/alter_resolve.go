package typedef

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// utilityLookupName clears the schema part's position: utility object lookup
// has no cursor position, unlike expression type input.
func utilityLookupName(name parser.ObjectName) parser.ObjectName {
	lookup := name
	if len(name.Parts) == 2 {
		parts := [2]parser.Identifier{name.Parts[0], name.Parts[1]}
		parts[0].Pos = parser.Position{}
		lookup.Parts = parts[:]
	}
	return lookup
}

// ResolveAlterEnumLocked resolves the enum an ALTER TYPE ... VALUE statement
// names, in PostgreSQL's precedence: existence, kind, then ownership. The
// caller holds Engine.mu; h is used only for the type search path and the
// authority's view.
func ResolveAlterEnumLocked(h ddl.Host, auth ddl.Authority, name parser.ObjectName) (*catalog.Type, ids.ObjectRef, error) {
	path, err := h.SearchNamespaces(auth, utilityLookupName(name), ids.TypeClassID)
	if err != nil {
		return nil, ids.ObjectRef{}, err
	}
	view := h.AccessView(auth)
	for _, namespace := range path {
		found := view.Catalog.Lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: namespace, Name: name.Parts[len(name.Parts)-1].Text})
		if len(found) == 0 {
			continue
		}
		d, ok := view.Catalog.Type(found[0])
		if !ok {
			return nil, ids.ObjectRef{}, fmt.Errorf("stuntgres: invalid type descriptor")
		}
		ref := ddl.ObjectRef(h, auth, d.Address())
		if !d.SQLType.IsEnum() {
			display := d.SQLType.DiagnosticName()
			if d.SQLType.IsUserDefined() || d.SQLType.IsDomain() || d.SQLType.IsComposite() {
				path, err := auth.ReferencePath()
				if err != nil {
					return nil, ref, err
				}
				display = catalog.TypeDiagnosticNameAt(view.Catalog, d, path)
			}
			return nil, ref, sqlError("42809", display+" is not an enum")
		}
		if err := ddl.Check(h, auth, access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
			return nil, ref, err
		}
		return d, ref, nil
	}
	return nil, ids.ObjectRef{}, sqlError("42704", fmt.Sprintf("type %q does not exist", catalog.ObjectNameText(name)))
}

// ResolveAlterDomainLocked resolves the domain an ALTER DOMAIN statement
// names: existence, the reference path, kind, then ownership, whose 42501
// names the type as dependency diagnostics do. The caller holds Engine.mu; h
// is used only for the type search path and the authority's view.
func ResolveAlterDomainLocked(h ddl.Host, auth ddl.Authority, name parser.ObjectName) (*catalog.Type, ids.ObjectRef, error) {
	path, err := h.SearchNamespaces(auth, utilityLookupName(name), ids.TypeClassID)
	if err != nil {
		return nil, ids.ObjectRef{}, err
	}
	view := h.AccessView(auth)
	for _, namespace := range path {
		found := view.Catalog.Lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: namespace, Name: name.Parts[len(name.Parts)-1].Text})
		if len(found) == 0 {
			continue
		}
		d := view.Catalog.MustType(found[0])
		ref := ddl.ObjectRef(h, auth, d.Address())
		diagnosticPath, err := auth.ReferencePath()
		if err != nil {
			return nil, ref, err
		}
		if !d.SQLType.IsDomain() {
			return nil, ref, sqlError("42809", d.SQLType.DiagnosticName()+" is not a domain")
		}
		if err := ddl.Check(h, auth, access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
			if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42501" {
				err = sqlError("42501", "must be owner of type "+catalog.TypeDiagnosticNameAt(view.Catalog, d, diagnosticPath))
			}
			return nil, ref, err
		}
		return d, ref, nil
	}
	return nil, ids.ObjectRef{}, sqlError("42704", fmt.Sprintf("type %q does not exist", catalog.ObjectNameText(name)))
}
