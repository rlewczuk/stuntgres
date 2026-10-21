package engine

import (
	"context"
	"fmt"
	"slices"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (e *Engine) bindDomainDefault(ctx context.Context, op *session.Operation, auth AuthorizationView, stage *CatalogStage, d *catalog.Type, raw parser.Expr, input sqltype.InputEnvironment, output sqltype.OutputEnvironment) error {
	base := d.SQLType.DomainBase()
	var reader catalog.DefinitionReader
	if op != nil {
		reader = op.Definition
	}
	if raw == nil {
		if parent, ok := catalog.DescriptorAt(auth.Catalog, reader, ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.Type.ID())}); ok {
			d.DomainDefault = parent.(*catalog.Type).DomainDefault
			d.DomainDefaultText = parent.(*catalog.Type).DomainDefaultText
		}
	} else {
		b := newExpressionBinder(ctx, e, input, op, nil)
		b.Query.Auth = &auth
		b.Query.Namespace = &analyze.QueryNamespace{}
		b.Params.NoParameters = true
		node, err := b.BindDefault(raw, catalog.Column{Name: d.Name(), Usage: base})
		if err != nil {
			return err
		}
		// PostgreSQL omits only a final bare builtin Const NULL. Real
		// domain/typmod conversions and expressions that may return NULL
		// remain executable recipes; never evaluate or unwrap them here.
		if literal, ok := node.(plan.LiteralExpr); ok && literal.Value.IsNull() && !literal.Usage.Type.IsDomain() && !literal.Usage.Type.IsUserDefined() {
			return nil
		}
		d.DomainDefaultOwn = true
		definition := catalog.ColumnDefault{Expression: node}
		if _, err = (stored.ScalarRenderer{Definition: b.DefinitionReader(), ACLOutput: regobj.ACLItemOutput{Owner: e.catalog.owner(), Roles: auth.Roles}, Dependencies: &definition.Dependencies, CommentSnapshot: &definition.CatalogSnapshot, CatalogAliasReferences: &definition.CatalogAliasReferences}).SQL(ctx, auth.Catalog, node); err != nil {
			return err
		}
		var path []ids.ObjectAddress
		if op != nil {
			path, err = e.referencePath(auth)
			if err != nil {
				return err
			}
		}
		text, err := (stored.Deparser{View: auth.Catalog, Path: path, Output: output, NonstandardStrings: op != nil && op.NonstandardStrings}).SQL(ctx, node, stored.DeparseParent{})
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

func applyDefaultOrigin(o analyze.DefaultOrigin, c *exec.Context) {
	c.QuoteOrigin = o.Quote
	c.RoutineRecipe, c.RoutineGraft = true, false
	c.RoutineChecks = nil
	c.RoutineReferences, c.RoutineConsumer = o.Definition.Dependencies, o.Consumer
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
		parent := typedef.TypeDefault(view, d.SQLType.DomainBase().Type)
		if (parent == nil) != (d.DomainDefault == nil) {
			return fmt.Errorf("stuntgres: inconsistent inherited domain default")
		}
		if parent != nil && (!analyze.SameGroupExpression(parent.Expression, d.DomainDefault.Expression) || !slices.Equal(parent.Dependencies, d.DomainDefault.Dependencies)) {
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
	_, err := (stored.ScalarRenderer{Validation: true, CapturedManifest: definition.Dependencies, Dependencies: &refs, CommentSnapshot: &snapshot, CatalogAliasReferences: &aliases}).SQL(context.Background(), view, definition.Expression)
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

func evalDefaultExpression(c *exec.Context, node expr.Expr, origin analyze.DefaultOrigin) (sqltype.Value, error) {
	quote, recipe, graft := c.QuoteOrigin, c.RoutineRecipe, c.RoutineGraft
	checks, refs, consumer := c.RoutineChecks, c.RoutineReferences, c.RoutineConsumer
	applyDefaultOrigin(origin, c)
	defer func() {
		c.QuoteOrigin, c.RoutineRecipe, c.RoutineGraft = quote, recipe, graft
		c.RoutineChecks, c.RoutineReferences, c.RoutineConsumer = checks, refs, consumer
	}()
	return node.Eval(c)
}

func evalBindingDefault(b *analyze.Binding, c *exec.Context, slot int, node expr.Expr) (sqltype.Value, error) {
	return evalDefaultExpression(c, node, bindingDefaultOrigin(b, slot))
}
