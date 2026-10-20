package engine

import (
	"bytes"
	"context"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPGDependDefaultReplayDeferrable(t *testing.T) {
	e := New()
	run(t, e, `CREATE TABLE provider(a INT)`)
	run(t, e, `CREATE TABLE consumer(b BOOL DEFAULT ('provider'::regclass IS NOT NULL), n INT DEFAULT CASE WHEN 'provider'::regclass IS NOT NULL THEN 1 ELSE 0 END, s SERIAL)`)
	d := e.catalog.state.MustRelation(roleTableRef(t, e, "consumer").Address)
	for _, attribute := range d.Attributes {
		definition := d.ColumnMeta.Defaults[attribute]
		if !defaultReplayDeferrable(definition) {
			t.Fatalf("captured definition %d not deferrable", attribute)
		}
		definition.Dependencies = nil
		if defaultReplayDeferrable(definition) {
			t.Fatalf("definition %d deferred without captured provider authority", attribute)
		}
	}

	view := e.CatalogRead()
	regclass, _ := view.LookupType(sqltype.Regclass)
	oid, _ := view.LookupType(sqltype.Oid)
	boolean, _ := view.LookupType(sqltype.Bool)
	ref := roleTableRef(t, e, "provider")
	literal := plan.LiteralExpr{Info: expr.Info{Usage: regclass.Usage(), RegclassRef: ref}, Value: sqltype.String(regclass, "10000")}
	definition := catalog.ColumnDefault{Expression: literal, Dependencies: []ids.ObjectRef{ref}}
	if !defaultReplayDeferrable(definition) {
		t.Fatal("genuine scalar Const not deferrable")
	}
	for _, properties := range []expr.Properties{{Volatility: expr.Stable}, {Volatility: expr.Volatile}, {RowDependent: true}, {ParameterDependent: true}, {StageDependent: true}} {
		changed := literal
		changed.Property = properties
		definition.Expression = changed
		if defaultReplayDeferrable(definition) {
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
		if defaultReplayDeferrable(definition) {
			t.Fatal("noncaptured, mismatched, nonscalar or NULL reference admitted")
		}
	}
	if defaultReplayDeferrable(catalog.ColumnDefault{}) {
		t.Fatal("missing definition admitted")
	}
}

func TestPGDependDefaultReplayMandatoryCycles(t *testing.T) {
	for _, owner := range []string{"check", "generated", "nondefault", "stable_default"} {
		t.Run(owner, func(t *testing.T) {
			e := New()
			run(t, e, `CREATE TABLE seed(a INT)`)
			run(t, e, `CREATE VIEW provider AS SELECT a FROM seed`)
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
			run(t, e, declaration+`)`)
			run(t, e, `CREATE OR REPLACE VIEW provider AS SELECT a FROM consumer`)
			var dump bytes.Buffer
			err := e.Dump(context.Background(), &dump)
			if err == nil || !strings.Contains(err.Error(), "cyclic view dump dependencies") {
				t.Fatalf("mandatory cycle returned %v", err)
			}
			if dump.Len() != 0 {
				t.Fatal("unsupported cycle emitted partial SQL")
			}
		})
	}
}
