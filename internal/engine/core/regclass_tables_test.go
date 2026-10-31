package core_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqltype"
)

func TestRegclassDeferredDefinitionSelection(t *testing.T) {
	e := engine.New()
	ownershipUnitExec(t, e, "CREATE TABLE t(v TEXT,r REGCLASS)")
	view := e.CatalogRead()
	descriptor := retiredUnitTable(t, e, "t")
	regclass, _ := view.LookupType(sqltype.Regclass)
	text, _ := view.LookupType(sqltype.Text)
	scalar := plan.LiteralExpr{Info: expr.Info{Usage: regclass.Usage()}, Value: sqltype.String(regclass, "1259")}
	inline := plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: sqltype.String(text, "inline")}
	if !table.DefaultReplayDeferrable(catalog.ColumnDefault{Expression: scalar}) || table.DefaultReplayDeferrable(catalog.ColumnDefault{Expression: inline}) {
		t.Fatal("selected result eligibility or primitive inline definition changed")
	}
	table := table.DumpRelation{
		Schema: "public", Name: "t", Attributes: descriptor.Attributes,
		Columns: descriptor.Columns, Descriptor: descriptor,
		Defaults:            map[int32]catalog.ColumnDefault{1: {Expression: inline}, 2: {Expression: scalar}},
		DeferredDefinitions: map[int32]bool{2: true},
	}
	var emitted []string
	err := table.EmitDeferredDefaults(context.Background(), view, func(statement string) error {
		emitted = append(emitted, statement)
		return nil
	})
	if err != nil || len(emitted) != 1 || !strings.HasPrefix(emitted[0], `ALTER TABLE ONLY "public"."t" ALTER COLUMN "r" SET DEFAULT `) || !strings.Contains(emitted[0], "pg_class") {
		t.Fatal(emitted, err)
	}
	sentinel := errors.New("deferred emission")
	if err := table.EmitDeferredDefaults(context.Background(), view, func(string) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
}

func TestRegclassOpaqueInlineDefaultReplayPrerequisites(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		t.Run(fmt.Sprint(mixed), func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			for _, query := range []string{
				"CREATE TABLE rr_opaque_owner(v BOOL)",
				"CREATE TABLE rr_opaque_target(n INT)",
				"ALTER TABLE rr_opaque_owner ALTER COLUMN v SET DEFAULT ('{rr_opaque_target}'::regclass[] IS NOT NULL)",
			} {
				enginetest.RoleExecute(t, e, op, query)
			}
			if mixed {
				// This reverse opaque edge belongs only to an omitted eligible
				// definition, not CREATE. Making it mandatory would add a cycle.
				enginetest.RoleExecute(t, e, op, "ALTER TABLE rr_opaque_target ADD COLUMN a regclass[] DEFAULT '{rr_opaque_owner}'")
			}
			view := e.CatalogRead()
			ownerRef, targetRef := enginetest.RoleTableRef(t, e, "rr_opaque_owner"), enginetest.RoleTableRef(t, e, "rr_opaque_target")
			descriptor, _ := view.Descriptor(ownerRef.Address)
			owner := descriptor.(*catalog.Relation)
			definition := owner.ColumnMeta.Defaults[owner.Attributes[owner.Index["v"]]]
			if table.DefaultReplayDeferrable(definition) || definition.Expression.ResultUsage().Type.ID() != sqltype.Bool {
				t.Fatal("opaque BOOL default changed eligibility")
			}
			for _, dependency := range definition.Dependencies {
				if dependency.Address.ClassID == ids.RelationClassID {
					t.Fatal("opaque payload gained DROP dependency", dependency)
				}
			}
			var refs []ids.ObjectAddress
			if err := regobj.ClassReplayReferences(view, definition.Expression, func(address ids.ObjectAddress) error { refs = append(refs, address); return nil }); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(refs, []ids.ObjectAddress{targetRef.Address}) {
				t.Fatal("live replay-only prerequisite missing", refs)
			}
		})
	}
}

func ownershipUnitExec(t *testing.T, e *core.Engine, q string) {
	t.Helper()
	if _, err := enginetest.Execute(e, context.Background(), enginetest.Statement(t, q), nil); err != nil {
		t.Fatal(q, err)
	}
}

// These are package fixtures: SQL constructs physical descriptors through the
// real providers; assertions exercise the issuer and catalog validator directly.
func retiredUnitTable(t *testing.T, e *core.Engine, name string) *catalog.Relation {
	t.Helper()
	for _, d := range e.CatalogRead().Descriptors() {
		if r, ok := d.(*catalog.Relation); ok && r.Name() == name {
			return r
		}
	}
	t.Fatal("missing physical relation", name)
	return nil
}
