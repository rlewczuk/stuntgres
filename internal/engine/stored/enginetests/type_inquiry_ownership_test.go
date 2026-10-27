package enginetests_test

import (
	"context"
	"maps"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestTypeDescribeStoredOwnership(t *testing.T) {
	t.Run("StringIO", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, s, "CREATE TABLE td_io_source(v text,c bpchar(4))")
		enginetest.SessionSQL(t, s, "CREATE VIEW td_io_view AS SELECT pg_typeof(v::unknown) AS v,pg_typeof(c::unknown) AS c FROM td_io_source")
		enginetest.SessionSQL(t, s, "CREATE POLICY td_io_policy ON td_io_source USING (pg_typeof(v::unknown)::oid=705::oid)")
		view := e.CatalogRead()
		relation := preparedFoldStoredRelation(t, e, "td_io_view")
		definition := stored.ViewOf(relation)
		root := definition.Query().Root.(*plan.ProjectNode)
		if len(root.Expressions) != 2 || len(definition.CastUsages()) != 2 {
			t.Fatal("logical IO casts lost original frozen paths")
		}
		for _, node := range root.Expressions {
			call := node.(expr.Composite)
			transport, ok := call.Children()[0].(plan.CoercionExpr)
			if !ok || transport.UnknownInputCast == nil {
				t.Fatal("stored IO call has no existing coercion transport")
			}
			marker := transport.UnknownInputCast
			cast, err := transport.LogicalInputCast()
			if err != nil || cast.Target.Type.ID() != sqltype.Unknown || marker.Source != transport.Child.ResultUsage() {
				t.Fatal("stored IO source/target mismatch", err)
			}
			mapped, changed, err := stored.RetagTypes(view, node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
			if err != nil || changed || mapped.(expr.Composite).Children()[0].(plan.CoercionExpr).UnknownInputCast != marker {
				t.Fatal("identity retag replaced immutable IO backing", err)
			}
			remapped, err := stored.RemapExpression(node, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
			if err != nil || !reflect.DeepEqual(remapped, node) {
				t.Fatal("reference remap changed logical IO recipe", err)
			}
			for _, mutate := range []func(*plan.CoercionExpr){
				func(c *plan.CoercionExpr) { m := *c.UnknownInputCast; m.Target = c.Usage.Type; c.UnknownInputCast = &m },
				func(c *plan.CoercionExpr) {
					m := *c.UnknownInputCast
					integer, _ := view.LookupType(sqltype.Int4)
					m.Source = integer.Usage()
					c.UnknownInputCast = &m
				},
			} {
				bad := transport
				mutate(&bad)
				if _, err := call.Rebuild(expr.InfoOf(node), []expr.Expr{bad}); err == nil {
					t.Fatal("malformed stored IO recipe accepted")
				}
			}
			if transport.UnknownInputCast != marker || marker.Target != cast.Target.Type {
				t.Fatal("rejected IO reconstruction mutated accepted marker")
			}
		}
		if err := definition.ValidateLiterals(view); err != nil {
			t.Fatal("authentic frozen IO paths invalid", err)
		}
		if err := definition.Validate(view, relation); err != nil {
			t.Fatal("logical IO target failed actual stored publication validation", err)
		}
		for _, display := range []bool{false, true} {
			sql, err := stored.ViewDefinitionSQL(context.Background(), view, relation, stored.Presentation{Deparse: display})
			if err != nil || strings.Count(sql, ` AS "pg_catalog"."unknown")`) != 2 {
				t.Fatal("frozen rendering lost genuine IO target", sql, err)
			}
		}
		for path, usage := range definition.CastUsages() {
			original := usage.Type
			foreign := *original
			rejected := definition.With(func(parts *stored.ViewParts) {
				parts.CastUsages = maps.Clone(parts.CastUsages)
				usage.Type = &foreign
				parts.CastUsages[path] = usage
			})
			if err := rejected.Validate(view, relation); err == nil {
				t.Fatal("foreign frozen IO target published")
			}
			if definition.CastUsages()[path].Type != original {
				t.Fatal("rejected frozen IO target changed accepted backing")
			}
		}
	})
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE DOMAIN td_owned_domain AS integer")
	enginetest.SessionSQL(t, s, "CREATE VIEW td_owned_view AS SELECT pg_typeof(NULL::td_owned_domain) AS nominal,format_type(23,NULL) AS label,pg_basetype(23::oid::regtype) AS base,pg_typeof('x') AS unknown,pg_typeof('x'::anyelement) AS pseudo,pg_typeof('x'::text) AS concrete")
	view := e.CatalogRead()
	relation := preparedFoldStoredRelation(t, e, "td_owned_view")
	definition := stored.ViewOf(relation)
	if definition == nil || definition.Query() == nil {
		t.Fatal("missing actual stored definition")
	}
	root, ok := definition.Query().Root.(*plan.ProjectNode)
	if !ok || len(root.Expressions) != 6 {
		t.Fatal("missing stored projection owner")
	}
	original := append([]expr.Expr(nil), root.Expressions...)
	identity := func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }
	for i, node := range original {
		composite, ok := node.(expr.Composite)
		if !ok {
			t.Fatalf("stored inquiry is not protocol owned: %T", node)
		}
		info := expr.InfoOf(node)
		declaration, err := exec.ExpressionRoutineDescriptor(node)
		want := []ids.OID{1619, 1081, 6315, 1619, 1619, 1619}[i]
		if err != nil || declaration == nil || declaration.Address().ObjectID != want || info.Occurrence == 0 || info.RoutineReference == nil {
			t.Fatal("stored selected declaration/occurrence absent", i, err)
		}
		children := composite.Children()
		if len(children) == 0 {
			t.Fatal("stored inquiry discarded demanded operands")
		}
		if i >= 3 {
			literal, ok := children[0].(plan.LiteralExpr)
			if !ok || (literal.UnknownInput != nil) != (i != 5) {
				t.Fatal("unknown source marker confused typed TEXT with unknown transport")
			}
			if i == 4 {
				if literal.UnknownInput.CastType == nil || literal.UnknownInput.CastType.ID() != 2283 {
					t.Fatal("transparent cast lost its actual resolved target authority")
				}
				if _, _, err := stored.RetagTypes(view, node, func(d *sqltype.Descriptor) *sqltype.Descriptor {
					if d == literal.UnknownInput.CastType {
						return nil
					}
					return d
				}); err == nil {
					t.Fatal("missing remapped pseudo authority accepted")
				}
			}
		}
		for _, rebuild := range []func(expr.Info, []expr.Expr) (expr.Expr, error){composite.Rebuild, func(info expr.Info, children []expr.Expr) (expr.Expr, error) {
			return expr.Substitute(composite, info, children)
		}} {
			next, err := rebuild(info, children)
			if err != nil || !reflect.DeepEqual(next, node) {
				t.Fatal("reconstruction replaced source authority, operand occurrence or logical witness", i, err)
			}
		}
		retagged, changed, err := stored.RetagTypes(view, node, identity)
		if err != nil || changed || !reflect.DeepEqual(retagged, node) {
			t.Fatal("identity retag changed actual stored backing/provenance", i, err)
		}
		remapped, err := stored.RemapExpression(node, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
		if err != nil || !reflect.DeepEqual(remapped, node) {
			t.Fatal("identity remap changed stored witness", i, err)
		}
		badInfo := info
		foreign := *info.Usage.Type
		badInfo.Usage.Type = &foreign
		if _, err := composite.Rebuild(badInfo, children); err == nil {
			t.Fatal("same-OID foreign result descriptor acquired selected provider authority")
		}
		if !reflect.DeepEqual(root.Expressions, original) || root != definition.Query().Root {
			t.Fatal("rejected reconstruction mutated original stored definition")
		}
	}
	if err := definition.Validate(view, relation); err != nil {
		t.Fatal("source-authenticated pseudo cast failed actual publication validation", err)
	}
	for path, usage := range definition.CastUsages() {
		if usage.Type.ID() != 2283 {
			continue
		}
		literalPath := path
		literalPath.Node++
		acceptedMarker := definition.Literals()[literalPath].UnknownInput
		if acceptedMarker == nil || acceptedMarker.CastType != usage.Type {
			t.Fatal("pseudo cast usage lacks its exact source occurrence")
		}
		rejected := definition.With(func(parts *stored.ViewParts) {
			parts.Literals = maps.Clone(parts.Literals)
			literal := parts.Literals[literalPath]
			literal.UnknownInput = nil
			parts.Literals[literalPath] = literal
		})
		if err := rejected.Validate(view, relation); err == nil {
			t.Fatal("unbacked pseudo cast metadata acquired publication authority")
		}
		if definition.Literals()[literalPath].UnknownInput != acceptedMarker {
			t.Fatal("rejected pseudo cast presentation mutated accepted marker backing")
		}
	}
	if err := definition.ValidateLiterals(view); err != nil {
		t.Fatal("genuine frozen unknown provenance rejected", err)
	}
	for path, literal := range definition.Literals() {
		if literal.UnknownInput == nil {
			continue
		}
		originalMarker := literal.UnknownInput
		rejected := definition.With(func(parts *stored.ViewParts) {
			parts.Literals = maps.Clone(parts.Literals)
			marker := *literal.UnknownInput
			marker.Literal.Value.Text += "changed"
			literal.UnknownInput = &marker
			parts.Literals[path] = literal
		})
		if err := rejected.ValidateLiterals(view); err == nil {
			t.Fatal("changed frozen source marker published as valid")
		}
		if definition.Literals()[path].UnknownInput != originalMarker || !reflect.DeepEqual(root.Expressions, original) {
			t.Fatal("rejected marker validation mutated accepted backing")
		}
	}
	// Presentation revision is not executable generation replacement. Retain
	// the actual old view and expression backing while catalog labels change.
	stamp := relation.Revision()
	enginetest.SessionSQL(t, s, "ALTER VIEW td_owned_view RENAME TO td_owned_renamed")
	current := preparedFoldStoredRelation(t, e, "td_owned_renamed")
	if current.Revision() <= stamp || stored.ViewOf(current) != definition || !reflect.DeepEqual(root.Expressions, original) {
		t.Fatal("presentation successor replaced immutable recipe backing or failed effective revision advance")
	}
	enginetest.SessionSQL(t, s, "CREATE OR REPLACE VIEW td_owned_renamed AS SELECT pg_typeof(NULL::td_owned_domain) AS nominal,format_type(25,NULL) AS label,pg_basetype(25::oid::regtype) AS base,pg_typeof('y') AS unknown,pg_typeof('y'::anyelement) AS pseudo,pg_typeof('y'::text) AS concrete")
	replacement := stored.ViewOf(preparedFoldStoredRelation(t, e, "td_owned_renamed"))
	if replacement == definition || replacement.Query().Root == root || !reflect.DeepEqual(root.Expressions, original) {
		t.Fatal("executable replacement adopted or mutated predecessor backing")
	}
}
