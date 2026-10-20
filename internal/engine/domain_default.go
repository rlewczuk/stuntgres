package engine

import (
	"context"
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (e *Engine) bindDomainDefault(ctx context.Context, op *Operation, auth AuthorizationView, stage *CatalogStage, d *catalog.Type, raw parser.Expr, input sqltype.InputEnvironment, output sqltype.OutputEnvironment) error {
	base := d.SQLType.DomainBase()
	var reader definitionReader
	if op != nil {
		reader = op.definition
	}
	if raw == nil {
		if parent, ok := definitionDescriptorAt(auth.Catalog, reader, ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.Type.ID())}); ok {
			d.DomainDefault = parent.(*catalog.Type).DomainDefault
			d.DomainDefaultText = parent.(*catalog.Type).DomainDefaultText
		}
	} else {
		b := newExpressionBinder(ctx, e, input, op, nil)
		b.queryAuth = &auth
		b.queryNamespace = &queryNamespace{}
		b.noParameters = true
		node, err := b.bindDefault(raw, catalog.Column{Name: d.Name(), Usage: base})
		if err != nil {
			return err
		}
		// PostgreSQL omits only a final bare builtin Const NULL. Real
		// domain/typmod conversions and expressions that may return NULL
		// remain executable recipes; never evaluate or unwrap them here.
		if literal, ok := node.(literalExpression); ok && literal.value.IsNull() && !literal.Usage.Type.IsDomain() && !literal.Usage.Type.IsUserDefined() {
			return nil
		}
		d.DomainDefaultOwn = true
		definition := catalog.ColumnDefault{Expression: node}
		if _, err = (scalarRenderer{definition: b.definitionReader(), aclOutput: capturedACLItemOutput{owner: e.catalog.owner(), roles: auth.Roles}, dependencies: &definition.Dependencies, commentSnapshot: &definition.CatalogSnapshot, catalogAliasReferences: &definition.CatalogAliasReferences}).sql(ctx, auth.Catalog, node); err != nil {
			return err
		}
		var path []ids.ObjectAddress
		if op != nil {
			path, err = e.referencePath(auth)
			if err != nil {
				return err
			}
		}
		text, err := (pgRenderer{view: auth.Catalog, path: path, output: output, nonstandardStrings: op != nil && op.nonstandardStrings}).sql(ctx, node, pgRenderParent{})
		if err != nil {
			return err
		}
		d.DomainDefaultText = text
		d.DomainDefault = &definition
	}
	if d.DomainDefault == nil {
		return nil
	}
	if stage != nil {
		for _, ref := range d.DomainDefault.Dependencies {
			address := ref.Address
			address.SubID = 0
			provider, ok := auth.Catalog.Descriptor(address)
			if !ok {
				return fmt.Errorf("stuntgres: missing domain default provider")
			}
			stage.domainBases = append(stage.domainBases, provider)
		}
	}
	return nil
}

func typeDefault(view catalog.Read, typ *sqltype.Descriptor) *catalog.ColumnDefault {
	if !typ.IsDomain() {
		return nil
	}
	d, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())})
	if !ok || d.SQLType != typ {
		return nil
	}
	return d.DomainDefault
}

// Only meaningful NULL masks or deferred domain conversions survive ordinary
// column-default elision. Plain builtin DEFAULT NULL retains its old absence.
func omitColumnDefault(view catalog.Read, node expr.Expr, usage sqltype.Usage) bool {
	return structuralNull(node) && typeDefault(view, usage.Type) == nil && !node.Properties().RuntimeDomainConversion
}

func defaultDestination(node expr.Expr, target sqltype.Usage) expr.Expr {
	if node.ResultUsage() == target {
		return node
	}
	plan, err := sqltype.PlanCoercion(node.ResultUsage(), target, sqltype.Assignment)
	if err != nil {
		panic(err)
	} // Catalog validation guarantees assignment-compatible recipes.
	return coercionExpression{Info: expr.Info{Usage: target, Property: coercionProperties(node, plan)}, child: node, plan: plan, mode: sqltype.Assignment}
}

// Default origin is statement-private authority, not a fabricated pg_attrdef for
// a domain recipe. Views retain this origin while mapping to physical slots.
type defaultOrigin struct {
	definition catalog.ColumnDefault
	consumer   *catalog.Relation
	quote      quoteOrigin
	requested  bool // UPDATE explicitly selected DEFAULT, including fresh NULL.
}

func columnDefault(view catalog.Read, d *catalog.Relation, slot int) (expr.Expr, defaultOrigin) {
	attribute := d.Attributes[slot]
	usage := d.Columns[slot].Usage
	if definition, ok := d.ColumnMeta.Defaults[attribute]; ok {
		return definition.Expression, defaultOrigin{definition: definition, consumer: d, quote: quoteOrigin{owner: view.Owner().Domain.Ref(view.Epoch(), d.Address()), attribute: attribute, definition: definition.OID}}
	}
	if definition := typeDefault(view, usage.Type); definition != nil {
		owner := view.Owner().Domain.Ref(view.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(usage.Type.ID())})
		return defaultDestination(definition.Expression, usage), defaultOrigin{definition: *definition, quote: quoteOrigin{owner: owner}}
	}
	if usage.Type.HasDomainConstraints() {
		return domainNullInputExpression(usage), defaultOrigin{}
	}
	return nil, defaultOrigin{}
}

func (o defaultOrigin) apply(c *EvalContext) {
	c.quoteOrigin = o.quote
	c.routineRecipe, c.routineGraft = true, false
	c.routineChecks = nil
	c.routineReferences, c.routineConsumer = o.definition.Dependencies, o.consumer
}

func validateDomainDefault(view catalog.Read, d *catalog.Type) error {
	if !d.SQLType.IsDomain() {
		if d.DomainDefault != nil || d.DomainDefaultOwn || d.DomainDefaultText != "" {
			return fmt.Errorf("stuntgres: non-domain default recipe")
		}
		return nil
	}
	if (d.DomainDefault == nil) != (d.DomainDefaultText == "") {
		return fmt.Errorf("stuntgres: inconsistent domain default presentation")
	}
	if d.DomainDefault != nil && (d.DomainDefault.Expression == nil || d.DomainDefault.OID != 0) {
		return fmt.Errorf("stuntgres: invalid domain default destination")
	}
	if !d.DomainDefaultOwn {
		parent := typeDefault(view, d.SQLType.DomainBase().Type)
		if (parent == nil) != (d.DomainDefault == nil) {
			return fmt.Errorf("stuntgres: inconsistent inherited domain default")
		}
		if parent != nil && (!sameGroupExpression(parent.Expression, d.DomainDefault.Expression) || !slices.Equal(parent.Dependencies, d.DomainDefault.Dependencies)) {
			return fmt.Errorf("stuntgres: inconsistent inherited domain default recipe")
		}
		if parent != nil && d.DomainDefaultText != view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.SQLType.DomainBase().Type.ID())}).DomainDefaultText {
			return fmt.Errorf("stuntgres: inconsistent inherited domain default presentation")
		}
	} else if d.DomainDefault == nil {
		return fmt.Errorf("stuntgres: missing own domain default")
	}
	if d.DomainDefault == nil {
		return nil
	}
	definition := d.DomainDefault
	if d.DomainDefaultOwn && definition.Expression.ResultUsage() != d.SQLType.DomainBase() {
		return fmt.Errorf("stuntgres: invalid own domain default destination")
	}
	if _, err := sqltype.PlanCoercion(definition.Expression.ResultUsage(), d.SQLType.DomainBase(), sqltype.Assignment); err != nil {
		return fmt.Errorf("stuntgres: invalid domain default destination: %w", err)
	}

	var refs []ids.ObjectRef
	snapshot, aliases := false, false
	_, err := (scalarRenderer{validation: true, capturedManifest: definition.Dependencies, dependencies: &refs, commentSnapshot: &snapshot, catalogAliasReferences: &aliases}).sql(context.Background(), view, definition.Expression)
	if err != nil {
		return err
	}
	if !slices.Equal(refs, definition.Dependencies) || snapshot != definition.CatalogSnapshot || aliases != definition.CatalogAliasReferences {
		return fmt.Errorf("stuntgres: inconsistent domain default recipe")
	}
	for _, ref := range refs {
		address := ref.Address
		address.SubID = 0
		if _, ok := view.Descriptor(address); !ok || ref.Domain() != view.Owner().Domain || ref.Epoch != view.Epoch() {
			return fmt.Errorf("stuntgres: foreign domain default provider")
		}
	}
	return nil
}
func (b *binding) defaultOriginAt(slot int) defaultOrigin {
	if slot < len(b.insert.defaultOrigins) {
		return b.insert.defaultOrigins[slot]
	}
	physical := b.physicalBinding()
	attribute := physical.relation.Attributes[slot]
	definition := physical.relation.ColumnMeta.Defaults[attribute]
	if definition.Expression == nil {
		return defaultOrigin{}
	}
	return defaultOrigin{definition: definition, consumer: physical.relation, quote: quoteOrigin{owner: physical.ref, attribute: attribute, definition: definition.OID}}
}

func evalDefaultExpression(c *EvalContext, node expr.Expr, origin defaultOrigin) (sqltype.Value, error) {
	quote, recipe, graft := c.quoteOrigin, c.routineRecipe, c.routineGraft
	checks, refs, consumer := c.routineChecks, c.routineReferences, c.routineConsumer
	origin.apply(c)
	defer func() {
		c.quoteOrigin, c.routineRecipe, c.routineGraft = quote, recipe, graft
		c.routineChecks, c.routineReferences, c.routineConsumer = checks, refs, consumer
	}()
	return node.Eval(c)
}

func (b *binding) evalDefault(c *EvalContext, slot int, node expr.Expr) (sqltype.Value, error) {
	return evalDefaultExpression(c, node, b.defaultOriginAt(slot))
}
