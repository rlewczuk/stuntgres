package table

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func TestPlanAlterScan(t *testing.T) {
	relation := func(notNull bool, generation catalog.GenerationKind) *catalog.Relation {
		d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000}, ObjectName: "t", Columns: []catalog.Column{{Name: "a", Nullable: true}, {Name: "v", Nullable: !notNull}}, Attributes: []int32{1, 2}}
		if notNull {
			d.Constraints.NotNull = []catalog.NotNullConstraint{{Name: "t_v_not_null", Attribute: 2}}
		}
		if generation != 0 {
			d.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{2: {Kind: generation}}
		}
		return d
	}
	typeCheck, addedCheck := catalog.CheckConstraint{Name: "typed"}, catalog.CheckConstraint{Name: "added"}
	plain, notNull := relation(false, 0), relation(true, 0)
	virtual := relation(true, catalog.GenerationVirtual)
	for _, tc := range []struct {
		name                  string
		old, next             *catalog.Relation
		rewrite, physicalRows bool
		typeChecks, added     []catalog.CheckConstraint
		replacements          []int32
		scan                  bool
		checks                []string
		notNull               int
	}{
		{name: "no rows", old: plain, next: notNull, typeChecks: []catalog.CheckConstraint{typeCheck}},
		{name: "nothing to validate", old: notNull, next: notNull, physicalRows: true},
		{name: "type checks without rewrite", old: plain, next: plain, physicalRows: true, typeChecks: []catalog.CheckConstraint{typeCheck}, added: []catalog.CheckConstraint{addedCheck}, scan: true, checks: []string{"typed", "added"}},
		{name: "rewrite validates added checks only", old: plain, next: notNull, rewrite: true, physicalRows: true, typeChecks: []catalog.CheckConstraint{typeCheck}, added: []catalog.CheckConstraint{addedCheck}, scan: true, checks: []string{"added"}},
		{name: "rewrite skips NOT NULL", old: plain, next: notNull, rewrite: true, physicalRows: true},
		{name: "new NOT NULL", old: plain, next: notNull, physicalRows: true, scan: true, notNull: 1},
		{name: "virtual NOT NULL replacement", old: virtual, next: virtual, physicalRows: true, replacements: []int32{2}, scan: true, notNull: 1},
		{name: "virtual replacement without NOT NULL", old: virtual, next: relation(false, catalog.GenerationVirtual), physicalRows: true, replacements: []int32{2}},
	} {
		v, scan := PlanAlterScan(tc.old, tc.next, "public", tc.rewrite, tc.physicalRows, tc.typeChecks, tc.added, nil, nil, tc.replacements, NewAlterRowValidator(tc.next, "public").NotNull)
		if scan != tc.scan {
			t.Fatalf("%s: scan = %v", tc.name, scan)
		}
		if !scan {
			continue
		}
		if len(v.NotNull) != tc.notNull {
			t.Fatalf("%s: NOT NULL slots = %v", tc.name, v.NotNull)
		}
		if tc.checks == nil {
			if v.Checks != nil {
				t.Fatalf("%s: unexpected checks %v", tc.name, v.Checks.Constraints.Check)
			}
			continue
		}
		if v.Checks == nil || v.Checks == tc.next || len(v.Checks.Constraints.Check) != len(tc.checks) {
			t.Fatalf("%s: checks = %+v", tc.name, v.Checks)
		}
		for i, name := range tc.checks {
			if v.Checks.Constraints.Check[i].Name != name {
				t.Fatalf("%s: checks = %v", tc.name, v.Checks.Constraints.Check)
			}
		}
	}
}
