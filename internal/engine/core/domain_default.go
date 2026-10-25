package core

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (e *Engine) BindDomainDefault(ctx context.Context, op *session.Operation, auth AuthorizationView, stage *CatalogStage, d *catalog.Type, raw parser.Expr, input sqltype.InputEnvironment, output sqltype.OutputEnvironment) error {
	return e.BindDomainDefaultUsage(ctx, op, auth, stage, d, raw, d.SQLType.DomainBase(), input, output)
}

func (e *Engine) BindDomainDefaultUsage(ctx context.Context, op *session.Operation, auth AuthorizationView, stage *CatalogStage, d *catalog.Type, raw parser.Expr, base sqltype.Usage, input sqltype.InputEnvironment, output sqltype.OutputEnvironment) error {
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
		b := NewExpressionBinder(ctx, e, input, op, nil)
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
		if _, err = (stored.ScalarRenderer{Definition: b.DefinitionReader(), ACLOutput: regobj.ACLItemOutput{Owner: &e.Catalog.Owner, Roles: auth.Roles}, Dependencies: &definition.Dependencies, CommentSnapshot: &definition.CatalogSnapshot, CatalogAliasReferences: &definition.CatalogAliasReferences}).SQL(ctx, auth.Catalog, node); err != nil {
			return err
		}
		var path []ids.ObjectAddress
		if op != nil {
			path, err = ReferencePath(auth)
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
			stage.DomainBases = append(stage.DomainBases, provider)
		}
	}
	return nil
}
