package views_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
)

// remintViewRules publishes each named view's _RETURN rule under a new
// identity, as a selective restore remap (control.RemapViewRuleOIDs) does,
// leaving every view's own relation identity unchanged.
func remintViewRules(t *testing.T, e *core.Engine, rules map[string]ids.OID) {
	t.Helper()
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	for name, rule := range rules {
		relation := base.MustRelation(enginetest.RoleTableRef(t, e, name).Address)
		replacement := *relation
		replacement.View = stored.ViewOf(relation).With(func(p *stored.ViewParts) { p.Rule = rule })
		if err := stage.Replace(&replacement); err != nil {
			t.Fatal(err)
		}
	}
	enginetest.PublishStage(t, e, stage)
	for name, rule := range rules {
		if got := stored.ViewOf(e.CatalogRead().MustRelation(enginetest.RoleTableRef(t, e, name).Address)).RuleOID(); got != rule {
			t.Fatalf("view %s rule %d, want %d", name, got, rule)
		}
	}
}

func viewIdentity(t *testing.T, e *core.Engine, name string) (relation, rule ids.OID) {
	t.Helper()
	d := e.CatalogRead().MustRelation(enginetest.RoleTableRef(t, e, name).Address)
	return d.Address().ObjectID, stored.ViewOf(d).RuleOID()
}

func extensionRestrictDetail(t *testing.T, e *core.Engine) string {
	t.Helper()
	_, err := enginetest.Execute(e, context.Background(), enginetest.Statement(t, "DROP EXTENSION hstore"), nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "2BP01" || diagnostic.Message != "cannot drop extension hstore because other objects depend on it" {
		t.Fatalf("DROP EXTENSION hstore: %v", err)
	}
	return diagnostic.Detail
}

// PostgreSQL sorts the type's dependents by the recorded dependent, the
// _RETURN rule, before the rule reverses to its view. A rule reminted below
// the competing column therefore loses the view to that column's path, even
// though the view's own identity is newer than the column's table. The type
// itself is entered through its newest member, the generated array, so this
// also exercises rule ordering inside the array-owner redirect.
func TestExtensionDropRuleIdentityStraddle(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE EXTENSION hstore")
	enginetest.Run(t, e, "CREATE TABLE hs_pk_spare()")
	spare := enginetest.RoleTableRef(t, e, "hs_pk_spare").Address.ObjectID
	enginetest.Run(t, e, "DROP TABLE hs_pk_spare")
	enginetest.Run(t, e, "CREATE TABLE hs_pk_t(v hstore)")
	enginetest.Run(t, e, "CREATE VIEW hs_pk_typ AS SELECT v, NULL::hstore AS n FROM hs_pk_t")
	table := enginetest.RoleTableRef(t, e, "hs_pk_t").Address.ObjectID
	view, rule := viewIdentity(t, e, "hs_pk_typ")
	if !(table < view && view < rule) {
		t.Fatalf("allocation table %d view %d rule %d", table, view, rule)
	}
	if got, want := extensionRestrictDetail(t, e), "column v of table hs_pk_t depends on type hstore\nview hs_pk_typ depends on type hstore"; got != want {
		t.Fatalf("detail\ngot  %q\nwant %q", got, want)
	}
	remintViewRules(t, e, map[string]ids.OID{"hs_pk_typ": spare})
	if !(spare < table && table < view) {
		t.Fatalf("remint rule %d table %d view %d", spare, table, view)
	}
	if got, want := extensionRestrictDetail(t, e), "column v of table hs_pk_t depends on type hstore\nview hs_pk_typ depends on column v of table hs_pk_t"; got != want {
		t.Fatalf("reminted detail\ngot  %q\nwant %q", got, want)
	}
	enginetest.Run(t, e, "DROP EXTENSION hstore CASCADE")
	base := e.CatalogRead()
	public := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	for _, key := range []ids.NameKey{{ClassID: ids.RelationClassID, Namespace: public, Name: "hs_pk_typ"}, {ClassID: ids.TypeClassID, Namespace: public, Name: "hstore"}} {
		if len(base.Lookup(key)) != 0 {
			t.Fatalf("%s survived CASCADE", key.Name)
		}
	}
	if relation := base.MustRelation(enginetest.RoleTableRef(t, e, "hs_pk_t").Address); len(relation.Columns) != 0 {
		t.Fatalf("hs_pk_t kept columns %v", relation.Columns)
	}
}

// Two views reached from the same type are visited in rule order, not view
// order. Visitation is reported in reverse, so exchanging the views' rule
// identities exchanges their report order.
func TestExtensionDropRuleIdentityOrder(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE EXTENSION hstore")
	enginetest.Run(t, e, "CREATE VIEW hs_pk_a AS SELECT NULL::hstore AS n")
	enginetest.Run(t, e, "CREATE VIEW hs_pk_b AS SELECT NULL::hstore AS n")
	a, ruleA := viewIdentity(t, e, "hs_pk_a")
	b, ruleB := viewIdentity(t, e, "hs_pk_b")
	if !(a < ruleA && ruleA < b && b < ruleB) {
		t.Fatalf("allocation a %d/%d b %d/%d", a, ruleA, b, ruleB)
	}
	ordered := "view hs_pk_a depends on type hstore\nview hs_pk_b depends on type hstore"
	if got := extensionRestrictDetail(t, e); got != ordered {
		t.Fatalf("detail\ngot  %q\nwant %q", got, ordered)
	}
	remintViewRules(t, e, map[string]ids.OID{"hs_pk_a": ruleB, "hs_pk_b": ruleA})
	if got, want := extensionRestrictDetail(t, e), "view hs_pk_b depends on type hstore\nview hs_pk_a depends on type hstore"; got != want {
		t.Fatalf("exchanged detail\ngot  %q\nwant %q", got, want)
	}
}
