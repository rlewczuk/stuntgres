package stored

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/sqltype"
)

// StringLiteral applies the same captured string policy to literals in retained
// expressions and in closed release declaration recipes.
func (r Deparser) StringLiteral(value string) string {
	if r.NonstandardStrings {
		value = strings.ReplaceAll(value, "\\", "\\\\")
	}
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// RoutineDeclarationSQL discloses a private release-analyzed metadata recipe.
// It never admits, binds, executes or checks EXECUTE on the target or callees.
func RoutineDeclarationSQL(ctx context.Context, view catalog.Read, d *catalog.Procedure, presentation Presentation) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if d == nil || view.Get(d.Address()) != d {
		return "", ids.ErrCatalogReplaced
	}
	recipe, ok := proc.RoutineDeclarationRecipe(d.DeclarationBody)
	if !ok {
		return "", fmt.Errorf("stuntgres: missing routine declaration recipe")
	}
	names, err := pgcatalog.ProcedureNameProjection(view, d)
	if err != nil {
		return "", err
	}
	inputs := 0
	if d.Signature != "" {
		inputs = strings.Count(string(d.Signature), ",") + 1
	}
	r := declarationRendering{view: view, target: d, names: names, inputs: inputs, policy: presentation, scalar: Deparser{View: view, Path: presentation.Path, Output: presentation.Output, NonstandardStrings: presentation.NonstandardStrings, RoutineDeclaration: true}}
	var out strings.Builder
	if recipe.Return() {
		out.WriteString("RETURN ")
	} else {
		out.WriteString("BEGIN ATOMIC\n ")
	}
	for i := range recipe.Len() {
		part := recipe.At(i)
		if err := ctx.Err(); err != nil {
			return "", err
		}
		text, err := r.part(part)
		if err != nil {
			return "", err
		}
		out.WriteString(text)
	}
	if recipe.Return() {
		out.WriteByte('\n')
	} else {
		out.WriteString(";\nEND\n")
	}
	return out.String(), nil
}

type declarationRendering struct {
	view   catalog.Read
	target *catalog.Procedure
	names  pgcatalog.ProcedureNames
	inputs int
	policy Presentation
	scalar Deparser
}

func (r declarationRendering) routine(id ids.OID, namespace ids.OID, name string, signature catalog.ProcedureSignature) (string, error) {
	if current, ok := r.view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: id}); ok {
		if current.Signature != signature {
			return "", ids.ErrCatalogReplaced
		}
		namespace, name, signature = current.Namespace().ObjectID, current.Name(), current.Signature
	}
	ns, ok := r.view.Namespace(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: namespace})
	if !ok {
		return "", ids.ErrCatalogReplaced
	}
	selected, count := regobj.SelectExactRoutine(r.view, analyze.NamespaceSelection{Path: r.policy.Path}, name, signature)
	bare := r.scalar.Identifier(name)
	if count == 1 && selected.ID == id {
		return bare, nil
	}
	// Released metadata-only callees need not have executable/catalog rows here.
	// An earlier exact signature masks the released reference, not other overloads.
	if count == 0 {
		for _, candidate := range r.policy.Path {
			if candidate == ns.Address() {
				return bare, nil
			}
			if _, n := regobj.SelectExactRoutine(r.view, analyze.NamespaceSelection{Qualified: candidate}, name, signature); n != 0 {
				break
			}
		}
	}
	return r.scalar.Identifier(ns.Name()) + "." + bare, nil
}

func (r declarationRendering) part(p proc.DeclarationPart) (string, error) {
	switch p.Kind {
	case proc.DeclarationSyntax:
		return p.Text, nil
	case proc.DeclarationIdentifier:
		return r.scalar.Identifier(p.Name), nil
	case proc.DeclarationSelfRoutine:
		return r.scalar.Identifier(r.target.Name()), nil
	case proc.DeclarationFormal:
		if p.Slot < 0 || p.Slot >= r.inputs {
			return "", ids.ErrCatalogReplaced
		}
		if name := r.names.At(p.Slot); name != "" {
			return r.scalar.Identifier(name), nil
		}
		return "$" + strconv.Itoa(p.Slot+1), nil
	case proc.DeclarationLiteral:
		return r.scalar.StringLiteral(p.Text), nil
	case proc.DeclarationType:
		return regobj.FormatDeclarationType(r.view, r.policy.Path, sqltype.ID(p.OID), r.scalar.Identifier)
	case proc.DeclarationRoutine:
		identity, ok := catalog.BuiltinRoutineIdentityBySignature(p.Name, catalog.ProcedureSignature(p.Signature))
		if !ok {
			return "", fmt.Errorf("stuntgres: unknown released declaration routine %s(%s)", p.Name, p.Signature)
		}
		return r.routine(identity.ID, identity.Namespace, identity.Name, identity.Signature)
	case proc.DeclarationRelation:
		relation, ok := r.view.Relation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(p.OID)})
		if !ok {
			return "", ids.ErrCatalogReplaced
		}
		return r.scalar.RelationName(relation, false)
	case proc.DeclarationColumn:
		relation, ok := r.view.Relation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(p.OID)})
		if !ok {
			return "", ids.ErrCatalogReplaced
		}
		column, err := r.scalar.Attribute(relation, p.Attribute)
		if err != nil {
			return "", err
		}
		return r.scalar.Identifier(relation.Name()) + "." + column, nil
	case proc.DeclarationAlias:
		d := r.view.Get(ids.ObjectAddress{ClassID: ids.OID(p.Class), ObjectID: ids.OID(p.OID)})
		if d == nil {
			return "", ids.ErrCatalogReplaced
		}
		text := r.scalar.Identifier(d.Name())
		if p.Class == uint32(ids.RelationClassID) {
			relation, ok := d.(*catalog.Relation)
			if !ok {
				return "", ids.ErrCatalogReplaced
			}
			var err error
			text, err = r.scalar.RelationName(relation, false)
			if err != nil {
				return "", err
			}
		}
		return r.scalar.StringLiteral(text), nil
	case proc.DeclarationOperator:
		current, ok := r.view.Operator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: ids.OID(p.OID)})
		if !ok {
			return "", ids.ErrCatalogReplaced
		}
		if uint32(current.Left) != p.Arguments[0] || uint32(current.Right) != p.Arguments[1] {
			return "", ids.ErrCatalogReplaced
		}
		namespace := current.Namespace()
		name := current.Name()
		key := ids.NameKey{ClassID: ids.OperatorClassID, Namespace: namespace, Name: name, Signature: p.Signature}
		for _, ns := range r.policy.Path {
			if ns == namespace {
				return name, nil
			}
			key.Namespace = ns
			if _, ok := r.view.LookupName(key); ok {
				break
			}
		}
		ns, ok := r.view.Namespace(namespace)
		if !ok {
			return "", ids.ErrCatalogReplaced
		}
		return "OPERATOR(" + r.scalar.Identifier(ns.Name()) + "." + name + ")", nil
	case proc.DeclarationExtensionMember:
		d, err := extension.DeclarationMember(r.view, r.target, p)
		if err != nil {
			return "", err
		}
		if current, ok := d.(*catalog.Procedure); ok {
			return r.routine(current.Address().ObjectID, current.Namespace().ObjectID, current.Name(), current.Signature)
		}
		if current, ok := d.(*catalog.Type); ok {
			return regobj.FormatDeclarationType(r.view, r.policy.Path, current.SQLType.ID(), r.scalar.Identifier)
		}
		return "", ids.ErrCatalogReplaced
	default:
		return "", fmt.Errorf("stuntgres: unknown declaration part")
	}
}
