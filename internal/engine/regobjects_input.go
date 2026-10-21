package engine

import (
	"context"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/parser"
)

// Namespace input uses only catalog names. Collation input additionally owns an
// immutable collation path, database name and qualified-schema authorization.
type capturedRegObjectInput struct{ capture regobj.InputCapture }

func (r capturedRegObjectInput) ResolveRegnamespace(ctx context.Context, input string) (uint32, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	parts, err := parser.ParseRegclassName(input)
	if err != nil || len(parts.Parts) != 1 {
		return 0, sqlError("42602", "invalid name syntax")
	}
	name := parts.Parts[0].Text
	a := r.capture.Authority
	address := catalog.DefinitionLookupAt(a.View.Catalog, a.Definitions, ids.NameKey{ClassID: ids.NamespaceClassID, Name: name})
	if _, exists := catalog.DefinitionNamespaceAt(a.View.Catalog, a.Definitions, address); !exists {
		return 0, sqlError("3F000", `schema "`+name+`" does not exist`)
	}
	return uint32(address.ObjectID), nil
}

func (r capturedRegObjectInput) ResolveRegcollation(ctx context.Context, input string) (uint32, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	raw, err := parser.ParseRegclassName(input)
	if err != nil {
		return 0, sqlError("42602", "invalid name syntax")
	}
	a := r.capture.Authority
	parts := raw.Parts
	spelling := func() string {
		if len(raw.Parts) == 1 {
			return raw.Parts[0].Text
		}
		var name strings.Builder
		for i, part := range raw.Parts {
			if i != 0 {
				name.WriteByte('.')
			}
			name.WriteString(part.Text)
		}
		return name.String()
	}
	if len(parts) > 3 {
		return 0, sqlError("42601", "improper qualified name (too many dotted names): "+spelling())
	}
	if len(parts) == 3 {
		if parts[0].Text != r.capture.Database {
			return 0, sqlError("0A000", "cross-database references are not implemented: "+spelling())
		}
		parts = parts[1:]
	}
	missing := func() (uint32, error) {
		return 0, sqlError("42704", `collation "`+spelling()+`" for encoding "UTF8" does not exist`)
	}
	path := analyze.NamespaceSelection{Path: r.capture.Path}
	if len(parts) == 2 {
		namespace := catalog.DefinitionLookupAt(a.View.Catalog, a.Definitions, ids.NameKey{ClassID: ids.NamespaceClassID, Name: parts[0].Text})
		if parts[0].Text == "pg_temp" {
			namespace = r.capture.Temporary
		}
		ns, exists := catalog.DefinitionNamespaceAt(a.View.Catalog, a.Definitions, namespace)
		if !exists {
			return missing()
		}
		if ns.Temporary != (ids.SessionToken{}) && ns.Temporary != r.capture.Token {
			actor, exists := a.View.Roles.Find(a.View.State.CurrentUser)
			if !exists || !actor.Superuser {
				return 0, sqlError("42501", "permission denied for schema "+ns.Name())
			}
		}
		if err := a.View.CanUse(a.Environment, a.View.Ref(namespace)); err != nil {
			return 0, err
		}
		path = analyze.NamespaceSelection{Qualified: namespace}
	}
	for i := range path.Count() {
		address := catalog.DefinitionLookupAt(a.View.Catalog, a.Definitions, ids.NameKey{ClassID: ids.CollationClassID, Namespace: path.At(i), Name: parts[len(parts)-1].Text})
		descriptor, _ := catalog.DescriptorAt(a.View.Catalog, a.Definitions, address)
		if d, exists := descriptor.(*catalog.Collation); exists {
			one := [1]regobj.CollationCandidate{{Address: address, Encoding: d.Encoding}}
			if regobj.SelectCollation(one[:], 6) != (ids.ObjectAddress{}) {
				return uint32(address.ObjectID), nil
			}
		}
	}
	return missing()
}
