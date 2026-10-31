package table

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestPGRewriteDeparsePreflight(t *testing.T) {
	f := newDeparseFixture(t)
	state := catalog.CloneState(f.in.Catalog.State())
	typ := builtinType(t, sqltype.PgNodeTree)
	state.SetDescriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 194}, &catalog.Type{SQLType: typ})
	owner := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 900}, ObjectName: "src", Rules: stored.RuleList{stored.NewRule(stored.RuleParts{OID: 901, Name: "r", Event: "INSERT", Mode: catalog.RuleMode('O')})}}
	state.SetDescriptor(owner.Address(), owner)
	f.in.Catalog = f.in.Catalog.WithState(state)
	list := owner.Rules.(stored.RuleList)
	qual, action, err := stored.RewriteTrees(t.Context(), f.in.Catalog, owner, typ, &list[0], nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []uint32{0, 900, 4294967295} {
		for _, pretty := range []bool{false, true} {
			oid, err := sqltype.OIDValue(builtinType(t, sqltype.Oid), id)
			if err != nil {
				t.Fatal(err)
			}
			c := &exec.Context{EvalStatement: exec.EvalStatement{Context: t.Context()}}
			signature := functions.DeparseExpr
			values := [3]sqltype.Value{action, oid}
			if pretty {
				signature = functions.DeparseExprPretty
				values[2] = sqltype.Boolean(builtinType(t, sqltype.Bool), true)
			}
			value, err := Deparse(c, f.in, signature, values, f.text)
			var d *sqlstate.Error
			if value.Type() != nil || !errors.As(err, &d) || d.Code != "22023" || d.Message != "input is a query, not an expression" {
				t.Fatal("action preflight was masked by context lookup", id, value, err)
			}
			values[0] = qual
			value, err = Deparse(c, f.in, signature, values, f.text)
			if id == 4294967295 {
				if err != nil || !value.IsNull() {
					t.Fatal("missing context qualification", value, err)
				}
			} else {
				if err != nil || value.IsNull() {
					t.Fatal("absent qualification is not empty TEXT", value, err)
				}
				if text, err := value.Text(); err != nil || text != "" {
					t.Fatal(text, err)
				}
			}
		}
	}
}
