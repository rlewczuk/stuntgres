package core_test

import (
	"bytes"
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestQueryWindowReviewDescriptorRemap(t *testing.T) {
	e := engine.New()
	for _, q := range []string{
		"CREATE TYPE window_enum AS ENUM('z','a')",
		"CREATE DOMAIN window_domain AS window_enum",
		"CREATE TABLE enum_source(id INT,e window_domain)",
		"INSERT INTO enum_source VALUES(1,'a'),(2,'z'),(3,'a'),(4,NULL)",
		"CREATE VIEW enum_window AS SELECT id,row_number() OVER(PARTITION BY e ORDER BY id) AS rn,count(*) OVER(ORDER BY e) AS n FROM enum_source",
	} {
		enginetest.Run(t, e, q)
	}
	ref := enginetest.RoleTableRef(t, e, "enum_window")
	definition := stored.ViewOf(e.CatalogRead().MustRelation(ref.Address))
	original := definition.Query().Root.(*plan.ProjectNode).Child.(*plan.WindowNode)
	oldEnum := domainUnitType(t, e.CatalogRead(), "window_enum").SQLType
	oldDomain := domainUnitType(t, e.CatalogRead(), "window_domain").SQLType
	var dump bytes.Buffer
	if err := engine.Dump(context.Background(), e, &dump); err != nil {
		t.Fatal(err)
	}
	fresh := engine.New()
	if err := engine.Restore(context.Background(), fresh, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	newEnum := domainUnitType(t, fresh.CatalogRead(), "window_enum").SQLType
	newDomain := domainUnitType(t, fresh.CatalogRead(), "window_domain").SQLType
	if oldEnum == newEnum || oldDomain == newDomain || newDomain.BaseType() != newEnum {
		t.Fatal("descriptors not replaced")
	}
	mapped, err := stored.RemapViewQuery(engine.New().CatalogRead(), definition.Query().Root, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old == oldEnum {
			return newEnum
		}
		if old == oldDomain {
			return newDomain
		}
		next, ok := fresh.CatalogRead().LookupType(old.ID())
		if !ok {
			t.Fatalf("missing replacement type %v", old.ID())
		}
		return next
	})
	if err != nil || mapped == definition.Query().Root {
		t.Fatal(mapped, err)
	}
	next := mapped.(*plan.ProjectNode).Child.(*plan.WindowNode)
	value, err := newDomain.Input(context.Background(), "a", -1)
	if err != nil {
		t.Fatal(err)
	}
	grouped, ordered := false, false
	for i, spec := range original.Specs {
		for j, key := range spec.Partition {
			if key.ResultUsage().Type != oldDomain {
				continue
			}
			grouped = true
			if next.Specs[i].Partition[j].ResultUsage().Type != newDomain {
				t.Fatal("partition usage not replaced")
			}
			if _, err := next.Specs[i].Grouping[j].Hash(context.Background(), value); err != nil {
				t.Fatal("remapped grouping retained old owner", err)
			}
			if _, err := spec.Grouping[j].Hash(context.Background(), value); err == nil {
				t.Fatal("original grouping accepted replacement owner")
			}
			if key.ResultUsage().Type != oldDomain {
				t.Fatal("original partition mutated")
			}
		}
		for j, key := range spec.Order {
			if key.Expression.ResultUsage().Type != oldDomain {
				continue
			}
			ordered = true
			if next.Specs[i].Order[j].Expression.ResultUsage().Type != newDomain {
				t.Fatal("ordering usage not replaced")
			}
			if sign, err := next.Specs[i].Order[j].Comparison.Compare(context.Background(), value, value, sqltype.InputEnvironment{}); err != nil || sign != 0 {
				t.Fatal("remapped ordering retained old owner", sign, err)
			}
			if _, err := key.Comparison.Compare(context.Background(), value, value, sqltype.InputEnvironment{}); err == nil {
				t.Fatal("original ordering accepted replacement owner")
			}
			if key.Expression.ResultUsage().Type != oldDomain {
				t.Fatal("original ordering mutated")
			}
		}
	}
	if !grouped || !ordered {
		t.Fatal("descriptor-changing partition/order witnesses absent", grouped, ordered)
	}
	for i, call := range original.Calls {
		if next.Calls[i].Expression.Slot != call.Expression.Slot || next.Calls[i].Expression == call.Expression {
			t.Fatal("remap lost slot or retained original occurrence")
		}
	}
}
