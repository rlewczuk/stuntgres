package engine

import (
	"context"
	"strings"

	"stuntgres/internal/parser"
)

// Namespace input uses only catalog names. Collation input additionally owns an
// immutable collation path, database name and qualified-schema authorization.
type capturedRegObjectInput struct {
	auth      AuthorizationView
	path      []ObjectAddress
	database  string
	temporary ObjectAddress
	token     SessionToken
}

func (e *Engine) captureRegObjectInput(auth AuthorizationView) (capturedRegObjectInput, error) {
	path, err := e.lookupPath(auth, collationLookup)
	if err != nil {
		return capturedRegObjectInput{}, err
	}
	return captureRegObjectsOnPath(auth, path), nil
}

func captureRegObjectsOnPath(auth AuthorizationView, path []ObjectAddress) capturedRegObjectInput {
	r := capturedRegObjectInput{auth: auth, path: path, temporary: temporaryNamespace(auth)}
	if auth.session != nil {
		r.database, r.token = auth.session.databaseName, auth.session.token
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
	address := r.auth.definitionLookup(NameKey{ClassID: NamespaceClassID, Name: name})
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
	path := namespaceSelection{path: r.path}
	if len(parts) == 2 {
		namespace := r.auth.definitionLookup(NameKey{ClassID: NamespaceClassID, Name: parts[0].Text})
		if parts[0].Text == "pg_temp" {
			namespace = r.temporary
		}
		ns, exists := r.auth.definitionNamespace(namespace)
		if !exists {
			return missing()
		}
		if ns.temporary != (SessionToken{}) && ns.temporary != r.token {
			actor, exists := r.auth.roles.Find(r.auth.state.CurrentUser)
			if !exists || !actor.Superuser {
				return 0, sqlError("42501", "permission denied for schema "+ns.name)
			}
		}
		if err := r.auth.CanUse(r.auth.engine.domain.Ref(r.auth.catalog.Epoch(), namespace)); err != nil {
			return 0, err
		}
		path = namespaceSelection{qualified: namespace}
	}
	for i := range path.count() {
		address := r.auth.definitionLookup(NameKey{ClassID: CollationClassID, Namespace: path.at(i), Name: parts[len(parts)-1].Text})
		descriptor, _ := r.auth.definitionDescriptor(address)
		if d, exists := descriptor.(*collationDescriptor); exists {
			one := [1]collationCandidate{{address, d.encoding}}
			if selectCollation(one[:], 6) != (ObjectAddress{}) {
				return uint32(address.ObjectID), nil
			}
		}
	}
	return missing()
}
