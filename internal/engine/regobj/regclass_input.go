package regobj

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func ClassNumeric(text string, typ *sqltype.Descriptor) (uint32, bool, error) {
	input := text
	if input == "-" {
		input = "0"
	}
	if input == "" {
		return 0, false, nil
	}
	for _, c := range input {
		if c < '0' || c > '9' {
			return 0, false, nil
		}
	}
	value, err := typ.Input(context.Background(), input, -1)
	if err != nil {
		return 0, true, err
	}
	n, err := value.OID()
	return n, true, err
}

func ClassQualifiedName(name parser.ObjectName, text, database string) (parser.ObjectName, error) {
	if len(name.Parts) > 3 {
		return parser.ObjectName{}, sqlError("42601", "improper relation name (too many dotted names): "+text)
	}
	if len(name.Parts) == 3 {
		if name.Parts[0].Text != database {
			return parser.ObjectName{}, sqlError("0A000", fmt.Sprintf(`cross-database references are not implemented: "%s"`, catalog.ObjectNameText(name)))
		}
		name.Parts = name.Parts[1:]
	}
	return name, nil
}

// Element input retains the statement's immutable authority, including its
// already-selected relation path. It never locks or consults a later catalog.
type ClassInput struct{ capture InputCapture }

// NewClassInput returns the regclass resolver over c.
func NewClassInput(c InputCapture) ClassInput { return ClassInput{capture: c} }

func (r ClassInput) ResolveRegclass(ctx context.Context, text string) (uint32, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	a := r.capture.Authority
	typ, _ := catalog.DefinitionTypeAt(a.View.Catalog, a.Definitions, sqltype.Oid)
	if oid, numeric, err := ClassNumeric(text, typ); numeric {
		return oid, err
	}
	name, err := parser.ParseRegclassName(text)
	if err != nil {
		return 0, sqlError("42602", "invalid name syntax")
	}
	name, err = ClassQualifiedName(name, text, r.capture.Database)
	if err != nil {
		return 0, err
	}
	path := r.capture.Path
	if len(name.Parts) == 2 {
		ns := catalog.DefinitionLookupAt(a.View.Catalog, a.Definitions, ids.NameKey{ClassID: ids.NamespaceClassID, Name: name.Parts[0].Text})
		if name.Parts[0].Text == "pg_temp" {
			ns = r.capture.Temporary
		}
		if ns == (ids.ObjectAddress{}) {
			path = nil
		} else {
			namespace, _ := catalog.DefinitionNamespaceAt(a.View.Catalog, a.Definitions, ns)
			if namespace.Temporary != (ids.SessionToken{}) && namespace.Temporary != r.capture.Token {
				return 0, sqlError("42501", fmt.Sprintf("permission denied for schema %s", namespace.Name()))
			}
			if err := a.View.CanUse(a.Environment, a.View.Ref(ns)); err != nil {
				return 0, err
			}
			path = []ids.ObjectAddress{ns}
		}
	}
	for _, ns := range path {
		if address := catalog.DefinitionLookupAt(a.View.Catalog, a.Definitions, ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: name.Parts[len(name.Parts)-1].Text}); address != (ids.ObjectAddress{}) {
			descriptor, _ := catalog.DescriptorAt(a.View.Catalog, a.Definitions, address)
			if _, ok := descriptor.(*catalog.Relation); !ok {
				return 0, fmt.Errorf("stuntgres: invalid relation descriptor")
			}
			return uint32(address.ObjectID), nil
		}
	}
	return 0, sqlError("42P01", fmt.Sprintf(`relation "%s" does not exist`, catalog.ObjectNameText(name)))
}
