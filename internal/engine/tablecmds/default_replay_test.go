package tablecmds_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqltype"
)

func TestPGDependDefaultReplayDeferrable(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, `CREATE TABLE provider(a INT)`)
	enginetest.Run(t, e, `CREATE TABLE consumer(b BOOL DEFAULT ('provider'::regclass IS NOT NULL), n INT DEFAULT CASE WHEN 'provider'::regclass IS NOT NULL THEN 1 ELSE 0 END, s SERIAL)`)
	d := e.Catalog.State.MustRelation(enginetest.RoleTableRef(t, e, "consumer").Address)
	for _, attribute := range d.Attributes {
		definition := d.ColumnMeta.Defaults[attribute]
		if !table.DefaultReplayDeferrable(definition) {
			t.Fatalf("captured definition %d not deferrable", attribute)
		}
		definition.Dependencies = nil
		if table.DefaultReplayDeferrable(definition) {
			t.Fatalf("definition %d deferred without captured provider authority", attribute)
		}
	}

	view := e.CatalogRead()
	regclass, _ := view.LookupType(sqltype.Regclass)
	oid, _ := view.LookupType(sqltype.Oid)
	boolean, _ := view.LookupType(sqltype.Bool)
	ref := enginetest.RoleTableRef(t, e, "provider")
	literal := plan.LiteralExpr{Info: expr.Info{Usage: regclass.Usage(), RegclassRef: ref}, Value: sqltype.String(regclass, "10000")}
	definition := catalog.ColumnDefault{Expression: literal, Dependencies: []ids.ObjectRef{ref}}
	if !table.DefaultReplayDeferrable(definition) {
		t.Fatal("genuine scalar Const not deferrable")
	}
	for _, properties := range []expr.Properties{{Volatility: expr.Stable}, {Volatility: expr.Volatile}, {RowDependent: true}, {ParameterDependent: true}, {StageDependent: true}} {
		changed := literal
		changed.Property = properties
		definition.Expression = changed
		if table.DefaultReplayDeferrable(definition) {
			t.Fatalf("nonclosed scalar properties %#v admitted", properties)
		}
	}
	for _, mutate := range []func(*plan.LiteralExpr){
		func(n *plan.LiteralExpr) { n.RegclassRef.Epoch++ },
		func(n *plan.LiteralExpr) { n.RegclassRef.Address.SubID = 1 },
		func(n *plan.LiteralExpr) {
			n.Usage = oid.Usage()
			value, err := sqltype.OIDValue(oid, 10000)
			if err != nil {
				t.Fatal(err)
			}
			n.Value = value
		},
		func(n *plan.LiteralExpr) { n.Value = sqltype.Null(regclass) },
	} {
		changed := literal
		mutate(&changed)
		// These mutations must not create captured authority for a pure BOOL
		// result. REGCLASS results independently belong to the selected subset.
		definition.Expression = plan.NullTestExpr{Info: expr.Info{Usage: boolean.Usage()}, Child: changed, Not: true}
		if table.DefaultReplayDeferrable(definition) {
			t.Fatal("noncaptured, mismatched, nonscalar or NULL reference admitted")
		}
	}
	if table.DefaultReplayDeferrable(catalog.ColumnDefault{}) {
		t.Fatal("missing definition admitted")
	}
}

func TestPGDependDefaultReplayMandatoryCycles(t *testing.T) {
	for _, owner := range []string{"check", "generated", "nondefault", "stable_default"} {
		t.Run(owner, func(t *testing.T) {
			e := engine.New()
			enginetest.Run(t, e, `CREATE TABLE seed(a INT)`)
			enginetest.Run(t, e, `CREATE VIEW provider AS SELECT a FROM seed`)
			declaration := `CREATE TABLE consumer(a INT`
			if owner == "stable_default" {
				declaration += `, c DATE DEFAULT CASE WHEN 'provider'::regclass IS NOT NULL THEN CURRENT_DATE ELSE CURRENT_DATE END`
			} else if owner != "nondefault" {
				declaration += `, c BOOL DEFAULT ('provider'::regclass IS NOT NULL)`
			}
			if owner == "generated" {
				declaration += `, g BOOL GENERATED ALWAYS AS ('provider'::regclass IS NOT NULL) STORED`
			} else if owner != "stable_default" {
				declaration += `, CONSTRAINT ck CHECK ('provider'::regclass IS NOT NULL)`
			}
			enginetest.Run(t, e, declaration+`)`)
			enginetest.Run(t, e, `CREATE OR REPLACE VIEW provider AS SELECT a FROM consumer`)
			var dump bytes.Buffer
			err := engine.Dump(context.Background(), e, &dump)
			if err == nil || !strings.Contains(err.Error(), "cyclic view dump dependencies") {
				t.Fatalf("mandatory cycle returned %v", err)
			}
			if dump.Len() != 0 {
				t.Fatal("unsupported cycle emitted partial SQL")
			}
		})
	}
}
