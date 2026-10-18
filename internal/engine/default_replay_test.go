package engine

import (
	"bytes"
	"context"
	"strings"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPGDependDefaultReplayDeferrable(t *testing.T) {
	e := New()
	run(t, e, `CREATE TABLE provider(a INT)`)
	run(t, e, `CREATE TABLE consumer(b BOOL DEFAULT ('provider'::regclass IS NOT NULL), n INT DEFAULT CASE WHEN 'provider'::regclass IS NOT NULL THEN 1 ELSE 0 END, s SERIAL)`)
	d := e.catalog.state.descriptors[roleTableRef(t, e, "consumer").Address].(*relationDescriptor)
	for _, attribute := range d.attributes {
		definition := d.defaults[attribute]
		if !defaultReplayDeferrable(definition) {
			t.Fatalf("captured definition %d not deferrable", attribute)
		}
		definition.dependencies = nil
		if defaultReplayDeferrable(definition) {
			t.Fatalf("definition %d deferred without captured provider authority", attribute)
		}
	}

	catalog := e.CatalogRead()
	regclass, _ := catalog.LookupType(sqltype.Regclass)
	oid, _ := catalog.LookupType(sqltype.Oid)
	boolean, _ := catalog.LookupType(sqltype.Bool)
	ref := roleTableRef(t, e, "provider")
	literal := literalExpression{expressionInfo: expressionInfo{Usage: regclass.Usage(), RegclassRef: ref}, value: sqltype.String(regclass, "10000")}
	definition := columnDefault{expression: literal, dependencies: []ids.ObjectRef{ref}}
	if !defaultReplayDeferrable(definition) {
		t.Fatal("genuine scalar Const not deferrable")
	}
	for _, properties := range []ExprProperties{{Volatility: Stable}, {Volatility: Volatile}, {RowDependent: true}, {ParameterDependent: true}, {StageDependent: true}} {
		changed := literal
		changed.Property = properties
		definition.expression = changed
		if defaultReplayDeferrable(definition) {
			t.Fatalf("nonclosed scalar properties %#v admitted", properties)
		}
	}
	for _, mutate := range []func(*literalExpression){
		func(n *literalExpression) { n.RegclassRef.Epoch++ },
		func(n *literalExpression) { n.RegclassRef.Address.SubID = 1 },
		func(n *literalExpression) {
			n.Usage = oid.Usage()
			value, err := sqltype.OIDValue(oid, 10000)
			if err != nil {
				t.Fatal(err)
			}
			n.value = value
		},
		func(n *literalExpression) { n.value = sqltype.Null(regclass) },
	} {
		changed := literal
		mutate(&changed)
		// These mutations must not create captured authority for a pure BOOL
		// result. REGCLASS results independently belong to the selected subset.
		definition.expression = nullTestExpression{expressionInfo: expressionInfo{Usage: boolean.Usage()}, child: changed, not: true}
		if defaultReplayDeferrable(definition) {
			t.Fatal("noncaptured, mismatched, nonscalar or NULL reference admitted")
		}
	}
	if defaultReplayDeferrable(columnDefault{}) {
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
