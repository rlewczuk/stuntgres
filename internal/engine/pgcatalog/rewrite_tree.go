package pgcatalog

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// A declaration owns parsed sealed syntax, not a checked executable stored view.
// Its reference facts are display inventory facts, not new dependency authority.
type builtinRewriteDeclaration struct {
	relation, rule ids.OID
	query          *parser.Select
	references     []BuiltinViewName
}
type builtinRewriteQual struct{ declaration *builtinRewriteDeclaration }

func newBuiltinRewriteDeclaration(record builtinView, statements []parser.Statement) (*builtinRewriteDeclaration, error) {
	if record.oid == 0 || record.returnRule == 0 || len(statements) != 1 {
		return nil, fmt.Errorf("stuntgres: invalid builtin rewrite declaration")
	}
	query, ok := statements[0].(*parser.Select)
	if !ok || query == nil {
		return nil, fmt.Errorf("stuntgres: builtin rewrite declaration is not SELECT")
	}
	declaration := &builtinRewriteDeclaration{relation: record.oid, rule: record.returnRule, query: query}
	for _, part := range record.parts {
		if part.name.Class != 0 {
			declaration.references = append(declaration.references, part.name)
		}
	}
	return declaration, nil
}

func builtinRewriteFact(ctx context.Context, read catalog.Read, owner *catalog.Relation, typ *sqltype.Descriptor) (RewriteCatalogFact, error) {
	if err := ctx.Err(); err != nil {
		return RewriteCatalogFact{}, err
	}
	if read.Owner() == nil || read.State() == nil || owner == nil || read.Get(owner.Address()) != owner || owner.RelationKind != catalog.CatalogRelation || !owner.CatalogView || owner.View != nil {
		return RewriteCatalogFact{}, fmt.Errorf("stuntgres: foreign builtin rewrite owner")
	}
	captured, ok := read.LookupType(sqltype.PgNodeTree)
	if !ok || captured != typ {
		return RewriteCatalogFact{}, fmt.Errorf("stuntgres: foreign builtin rewrite type")
	}
	record, ok := builtinViews[owner.Address().ObjectID]
	if !ok || record.returnRule == 0 || owner.CatalogReturnRule != record.returnRule {
		return RewriteCatalogFact{}, fmt.Errorf("stuntgres: invalid builtin rewrite identity")
	}
	if record.declarationErr != nil {
		return RewriteCatalogFact{}, record.declarationErr
	}
	if record.declaration == nil || record.declaration.relation != record.oid || record.declaration.rule != record.returnRule {
		return RewriteCatalogFact{}, fmt.Errorf("stuntgres: missing builtin rewrite declaration")
	}
	qual, err := sqltype.Tree(typ, builtinRewriteQual{record.declaration})
	if err != nil {
		return RewriteCatalogFact{}, err
	}
	action, err := sqltype.Tree(typ, record.declaration)
	if err != nil {
		return RewriteCatalogFact{}, err
	}
	return RewriteCatalogFact{OID: record.returnRule, Name: "_RETURN", Event: "1", Mode: catalog.RuleMode('O'), Instead: true, Qual: qual, Action: action}, nil
}

func (q builtinRewriteQual) StructuralOutput(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if q.declaration == nil {
		return "", fmt.Errorf("stuntgres: missing builtin rewrite qualification owner")
	}
	return "<>", nil
}
func (d *builtinRewriteDeclaration) StructuralOutput(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if d == nil || d.query == nil {
		return "", fmt.Errorf("stuntgres: missing builtin rewrite declaration")
	}
	var out strings.Builder
	out.WriteString("{BuiltinRewriteDeclaration ")
	if err := parser.AppendStructuralSyntax(ctx, &out, d.query); err != nil {
		return "", err
	}
	out.WriteString(" references [")
	for _, ref := range d.references {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		fmt.Fprintf(&out, " {class %d namespace %d name %q signature %q routine %d regclass %t}", ref.Class, ref.Namespace, ref.Name, ref.Signature, ref.RoutineOID, ref.RegclassLiteral)
	}
	out.WriteString("]}")
	return out.String(), ctx.Err()
}

// These predicates disclose only carrier kind, never mutable declaration syntax.
func IsBuiltinRewriteAction(tree sqltype.StructuralTree) bool {
	_, ok := tree.(*builtinRewriteDeclaration)
	return ok
}
func IsBuiltinRewriteQual(tree sqltype.StructuralTree) bool {
	_, ok := tree.(builtinRewriteQual)
	return ok
}
