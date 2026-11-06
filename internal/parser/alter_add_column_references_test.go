package parser

import (
	"reflect"
	"strings"
	"testing"
)

func TestForeignKeyGrammarAlterAddColumn(t *testing.T) {
	for _, tc := range []struct {
		query                                 string
		ifNotExists, full, deferred, negative bool
		update, delete                        ForeignKeyAction
		referenced                            int
	}{
		{`ALTER TABLE c ADD pid INT REFERENCES p`, false, false, false, false, ForeignKeyNoAction, ForeignKeyNoAction, 0},
		{`ALTER TABLE c ADD COLUMN IF NOT EXISTS pid INT CONSTRAINT fk REFERENCES "s"."P"("ID") MATCH FULL ON DELETE SET NULL(pid) ON UPDATE CASCADE DEFERRABLE INITIALLY DEFERRED NOT ENFORCED`, true, true, true, true, ForeignKeyCascade, ForeignKeySetNull, 1},
		{`ALTER TABLE c ADD pid INT CONSTRAINT fk REFERENCES public.p(id) MATCH SIMPLE ON UPDATE SET DEFAULT ON DELETE RESTRICT NOT DEFERRABLE INITIALLY IMMEDIATE ENFORCED`, false, false, false, false, ForeignKeySetDefault, ForeignKeyRestrict, 1},
	} {
		statements, err := Parse(tc.query)
		if err != nil || len(statements) != 1 {
			t.Fatal(tc.query, statements, err)
		}
		action := statements[0].(*AlterTable).Actions[0]
		column := action.Definition
		if action.Action != AddColumn || action.IfNotExists != tc.ifNotExists || column == nil || len(column.Constraints) != 1 {
			t.Fatal(action)
		}
		fk := column.Constraints[0].ForeignKey
		if fk == nil || len(fk.Columns) != 0 || len(fk.Referenced) != tc.referenced || fk.Full != tc.full || fk.Deferrable != tc.deferred || fk.InitiallyDeferred != tc.deferred || fk.NotEnforced != tc.negative || fk.NotValid != tc.negative || fk.UpdateAction != tc.update || fk.DeleteAction != tc.delete {
			t.Fatal(tc.query, fk)
		}
		if action.Pos.Offset != strings.Index(tc.query, "ADD") || column.Pos.Offset != strings.Index(tc.query, "pid") || fk.Pos.Offset != strings.Index(tc.query, "REFERENCES") || fk.Pos.Character != fk.Pos.Offset+1 {
			t.Fatal("lost source positions", action.Pos, column.Pos, fk.Pos)
		}
		if tc.ifNotExists && (fk.Name.Text != "fk" || len(fk.Target.Parts) != 2 || fk.Target.Parts[0].Text != "s" || fk.Target.Parts[1].Text != "P" || fk.Referenced[0].Text != "ID" || len(fk.DeleteColumns) != 1 || fk.DeleteColumns[0].Text != "pid" || column.Constraints[0].EnforcementPos.Character == 0) {
			t.Fatal("lost names/options", fk)
		}
		before := *fk
		again, err := Parse(tc.query)
		if err != nil || !reflect.DeepEqual(fk, &before) {
			t.Fatal("later parse mutated retained syntax", err)
		}
		other := again[0].(*AlterTable).Actions[0].Definition.Constraints[0].ForeignKey
		other.Target.Parts[0].Text = "changed"
		if !reflect.DeepEqual(fk, &before) {
			t.Fatal("parse calls share FK backing state")
		}
	}
	for _, clauses := range []string{
		`DEFAULT 1 NULL CHECK(pid>0) UNIQUE REFERENCES p(id) NOT NULL REFERENCES q`,
		`REFERENCES p(id) DEFAULT 1 NULL CHECK(pid>0) UNIQUE NOT NULL REFERENCES q`,
	} {
		statements, err := Parse(`ALTER TABLE c ADD COLUMN pid INT ` + clauses)
		if err != nil {
			t.Fatal(clauses, err)
		}
		constraints := statements[0].(*AlterTable).Actions[0].Definition.Constraints
		if len(constraints) != 7 {
			t.Fatal("lost ordered local constraints", constraints)
		}
		previous, foreignKeys := -1, 0
		for _, constraint := range constraints {
			if constraint.Pos.Offset <= previous || constraint.Pos.Character == 0 {
				t.Fatal("constraint source order changed", constraints)
			}
			previous = constraint.Pos.Offset
			if constraint.ForeignKey != nil {
				foreignKeys++
			}
		}
		if foreignKeys != 2 {
			t.Fatal("lost repeated REFERENCES", constraints)
		}
	}
}
