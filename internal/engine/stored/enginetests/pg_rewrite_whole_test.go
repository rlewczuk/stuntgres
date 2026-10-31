package enginetests_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestPGRewriteWholeRow(t *testing.T) {
	e := engine.New()
	session := enginetest.StartSession(t, e, nil)
	for _, query := range []string{
		"CREATE TABLE src(v integer)",
		"CREATE RULE old_row AS ON UPDATE TO src WHERE OLD.* IS DISTINCT FROM NULL DO ALSO NOTHING",
		"CREATE RULE new_row AS ON UPDATE TO src WHERE NEW.* IS DISTINCT FROM NULL DO ALSO NOTHING",
	} {
		enginetest.SessionSQL(t, session, query)
	}
	read := e.CatalogRead()
	owner := read.MustRelation(enginetest.CatalogRef(t, e, read, "src").Address)
	typ, _ := read.LookupType(sqltype.PgNodeTree)
	var output [2]string
	list := owner.Rules.(stored.RuleList)
	for i := range list {
		rule := &list[i]
		side := 0
		if rule.Name == "new_row" {
			side = 1
		}
		qual, _, err := stored.RewriteTrees(t.Context(), read, owner, typ, rule, nil)
		if err != nil {
			t.Fatal(err)
		}
		tree, _ := qual.Tree()
		carrier := tree.(stored.RewriteQualCarrier)
		output[side], err = carrier.StructuralOutput(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if err := carrier.Preflight(t.Context(), 0); err == nil {
			t.Fatal("whole-row reference lost with zero context")
		}
		if side == 1 {
			if err := carrier.Preflight(t.Context(), 4294967295); err == nil {
				t.Fatal("NEW whole row collapsed into OLD")
			}
		} else {
			text, absent, err := stored.RewriteQualDisplay(t.Context(), carrier, stored.Deparser{View: read, Relation: owner})
			if err != nil || absent || text != "(src.* IS DISTINCT FROM NULL)" {
				t.Fatal(text, absent, err)
			}
		}
	}
	if output[0] == "" || output[1] == "" || output[0] == output[1] {
		t.Fatal("actual OLD/NEW whole-row structures collapsed", output)
	}
}
