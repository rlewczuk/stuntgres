package engine

import (
	"context"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// Namespace input uses only catalog names. Collation input additionally owns an
// immutable collation path, database name and qualified-schema authorization.
type capturedRegObjectInput struct {
	auth      AuthorizationView
	path      []ids.ObjectAddress
	database  string
	temporary ids.ObjectAddress
	token     ids.SessionToken
}

func (e *Engine) captureRegObjectInput(auth AuthorizationView) (capturedRegObjectInput, error) {
	path, err := e.lookupPath(auth, analyze.CollationLookup)
	if err != nil {
		return capturedRegObjectInput{}, err
	}
	return captureRegObjectsOnPath(auth, path), nil
}

func captureRegObjectsOnPath(auth AuthorizationView, path []ids.ObjectAddress) capturedRegObjectInput {
	r := capturedRegObjectInput{auth: auth, path: path, temporary: temporaryNamespace(auth)}
	if auth.session != nil {
		r.database, r.token = auth.session.DatabaseName, auth.session.SessionToken
	}
	r.auth.session, r.auth.settings = nil, nil
	return r
}

func (r capturedRegObjectInput) ResolveRegnamespace(ctx context.Context, input string) (uint32, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	parts, err := parser.ParseRegclassName(input)
	if err != nil || len(parts.Parts) != 1 {
		return 0, sqlError("42602", "invalid name syntax")
	}
	name := parts.Parts[0].Text
	address := r.auth.definitionLookup(ids.NameKey{ClassID: ids.NamespaceClassID, Name: name})
	if _, exists := r.auth.definitionNamespace(address); !exists {
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
		if parts[0].Text != r.database {
			return 0, sqlError("0A000", "cross-database references are not implemented: "+spelling())
		}
		parts = parts[1:]
	}
	missing := func() (uint32, error) {
		return 0, sqlError("42704", `collation "`+spelling()+`" for encoding "UTF8" does not exist`)
	}
	path := analyze.NamespaceSelection{Path: r.path}
	if len(parts) == 2 {
		namespace := r.auth.definitionLookup(ids.NameKey{ClassID: ids.NamespaceClassID, Name: parts[0].Text})
		if parts[0].Text == "pg_temp" {
			namespace = r.temporary
		}
		ns, exists := r.auth.definitionNamespace(namespace)
		if !exists {
			return missing()
		}
		if ns.Temporary != (ids.SessionToken{}) && ns.Temporary != r.token {
			actor, exists := r.auth.Roles.Find(r.auth.State.CurrentUser)
			if !exists || !actor.Superuser {
				return 0, sqlError("42501", "permission denied for schema "+ns.Name())
			}
		}
		if err := r.auth.CanUse(r.auth.Ref(namespace)); err != nil {
			return 0, err
		}
		path = analyze.NamespaceSelection{Qualified: namespace}
	}
	for i := range path.Count() {
		address := r.auth.definitionLookup(ids.NameKey{ClassID: ids.CollationClassID, Namespace: path.At(i), Name: parts[len(parts)-1].Text})
		descriptor, _ := r.auth.definitionDescriptor(address)
		if d, exists := descriptor.(*catalog.Collation); exists {
			one := [1]collationCandidate{{address, d.Encoding}}
			if selectCollation(one[:], 6) != (ids.ObjectAddress{}) {
				return uint32(address.ObjectID), nil
			}
		}
	}
	return missing()
}
