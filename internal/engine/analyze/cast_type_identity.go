package analyze

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// A cast may reuse an executable source identity without acquiring its input or
// storage capability. Name lookup and authorization still use the current owner.
func (b *Binder) resolveCastIdentity(raw parser.TypeName, source *sqltype.Descriptor) (sqltype.Usage, error) {
	auth, err := b.BindingAuthorization()
	if err != nil {
		return sqltype.Usage{}, err
	}
	name := raw.Name
	if !raw.Quoted && raw.Schema == nil {
		name = typedef.CanonicalDeclarationTypeName(name)
	}
	object := parser.ObjectName{Parts: []parser.Identifier{{Text: name, Pos: raw.Pos}}}
	if raw.Schema != nil {
		object.Parts = []parser.Identifier{*raw.Schema, object.Parts[0]}
	}
	path, err := b.Host.LookupNamespaces(auth, object, TypeLookup)
	if err != nil {
		return sqltype.Usage{}, err
	}
	return ResolveTypeName(raw, name, object, path, TypeNameInput{Catalog: auth.ReadCatalog(), Definition: b.Host.DefinitionOf(auth), CastSource: source}, TypeNameOperation{Warn: b.Host.EmitWarning})
}

func recordCastPassthrough(source, target *sqltype.Descriptor) bool {
	return target.ID() == 2249 && source.BaseType().IsComposite() ||
		target.ID() == 2287 && source.IsArray() && source.Element().BaseType().IsComposite()
}

func (b *Binder) typeNameCallable(name parser.ObjectName) bool {
	auth, err := b.BindingAuthorization()
	if err != nil {
		return false
	}
	path, err := b.Host.LookupNamespaces(auth, name, TypeLookup)
	if err != nil {
		return false
	}
	for i := range path.Count() {
		if catalog.DefinitionLookupAt(auth.ReadCatalog(), b.Host.DefinitionOf(auth), ids.NameKey{ClassID: ids.TypeClassID, Namespace: path.At(i), Name: name.Parts[len(name.Parts)-1].Text}) != (ids.ObjectAddress{}) {
			return true
		}
	}
	return false
}

func (b *Binder) elideVoidParameters(raw parser.FunctionExpr) parser.FunctionExpr {
	if raw.Within || len(raw.ArgNames) != 0 {
		return raw
	}
	for i, arg := range raw.Args {
		p, ok := arg.(parser.ParameterExpr)
		if !ok || b.Params.Parameters[p.Number].Type == nil || b.Params.Parameters[p.Number].Type.ID() != sqltype.Void {
			continue
		}
		args := make([]parser.Expr, 0, len(raw.Args)-1)
		args = append(args, raw.Args[:i]...)
		for _, remaining := range raw.Args[i+1:] {
			if p, ok := remaining.(parser.ParameterExpr); ok && b.Params.Parameters[p.Number].Type != nil && b.Params.Parameters[p.Number].Type.ID() == sqltype.Void {
				continue
			}
			args = append(args, remaining)
		}
		raw.Args = args
		break
	}
	return raw
}
