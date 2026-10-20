package engine

import (
	"errors"
	"fmt"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestSerialSequenceSystemColumns(t *testing.T) {
	for _, kind := range []catalog.RelationKind{catalog.TableRelation, catalog.SequenceRelation, catalog.CatalogRelation, catalog.IndexRelation, catalog.ViewRelation} {
		for _, column := range []string{"ctid", "xmin", "cmin", "xmax", "cmax", "tableoid", "CTID", `"ctid"`, "ctid ", "oid", "absent"} {
			t.Run(fmt.Sprintf("kind=%d/%s", kind, column), func(t *testing.T) {
				f := serialSequenceUnitFixture(t)
				f.table.RelationKind = kind
				text := f.node.ResultUsage().Type
				f.node.Args[1] = plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: sqltype.String(text, column)}
				value, err := f.node.Eval(f.context)
				// Keep expectations independent of the production name helper.
				system := column == "ctid" || column == "xmin" || column == "cmin" || column == "xmax" || column == "cmax" || column == "tableoid"
				if system && kind != catalog.ViewRelation {
					if err != nil || !value.IsNull() || value.Type() != text {
						t.Fatal("existing unowned system attribute", value, err)
					}
					return
				}
				var diagnostic *sqlstate.Error
				want := fmt.Sprintf(`column "%s" of relation "Owned" does not exist`, column)
				if !errors.As(err, &diagnostic) || diagnostic.Code != "42703" || diagnostic.Message != want || diagnostic.Position != "" || diagnostic.Context != "" {
					t.Fatal("absent literal attribute", err)
				}
			})
		}
	}
	// A view can expose a declared column whose literal name happens to be a
	// system spelling. Declared lookup must precede the kind-aware fallback.
	f := serialSequenceUnitFixture(t)
	f.table.RelationKind = catalog.ViewRelation
	f.table.Columns[0].Name = "ctid"
	delete(f.table.Index, "ID")
	f.table.Index["ctid"] = 0
	f.sequence.Sequence.Owned = ids.ObjectAddress{}
	text := f.node.ResultUsage().Type
	f.node.Args[1] = plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: sqltype.String(text, "ctid")}
	value, err := f.node.Eval(f.context)
	if err != nil || !value.IsNull() || value.Type() != text {
		t.Fatal("declared view column skipped", value, err)
	}
}
