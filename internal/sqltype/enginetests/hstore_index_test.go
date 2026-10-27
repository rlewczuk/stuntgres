package enginetests

import (
	"context"
	"testing"

	"stuntgres/internal/sqltype"
)

func hstoreIndexClass(d *sqltype.Descriptor, hash bool) sqltype.IndexClass {
	method := uint32(403)
	if hash {
		method = 405
	}
	c := sqltype.NewIndexClass(sqltype.IndexClassDefinition{OID: 65100, Method: method, Family: 65101, Input: uint32(d.ID()), Hash: hash, Name: "hstore_ops", FamilyName: "hstore_ops"})
	for slot := 1; slot <= 2; slot++ {
		c = c.WithSupport(slot, sqltype.IndexSupport{Provider: sqltype.HstoreIndexSupport, Family: 65101, Member: uint32(65200 + slot), Routine: uint32(65300 + slot), Left: uint32(d.ID()), Right: uint32(d.ID()), Revision: 1, FamilyRevision: 2, RoutineRevision: 3})
	}
	return c
}

func TestHstoreIndexSupport(t *testing.T) {
	ctx := context.Background()
	d := hstoreUnitType(t, 65000)
	a := hstoreComparisonValue(t, d, "a=>bc")
	b := hstoreComparisonValue(t, d, "ab=>c")
	c := hstoreIndexClass(d, false)
	order, err := sqltype.BindIndexOrdering(d.Usage(), c)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := order.Compare(ctx, a, b, sqltype.InputEnvironment{}); got != -1 || err != nil {
		t.Fatalf("typed support comparator %d/%v", got, err)
	}
	if order.Support() != c.Support(1) || !order.DatumComparison().OrderingBound() || order.DatumComparison().DefaultBtreeOpclass() != c.OID() {
		t.Fatal("ordering lost selected support/class authority")
	}
	if _, err := order.Compare(ctx, a, sqltype.Null(d), sqltype.InputEnvironment{}); err == nil {
		t.Fatal("NULL reached support comparator")
	}
	hashClass := hstoreIndexClass(d, true)
	hash, err := sqltype.BindIndexHash(d.Usage(), hashClass)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := hash.Hash(ctx, a); got != uint64(uint32(2609841294)) || err != nil {
		t.Fatalf("unseeded support hash %d/%v", got, err)
	}
	if equal, err := hash.Equal(ctx, a, b); equal || err != nil {
		t.Fatalf("boundary-distinct support equality %v/%v", equal, err)
	}
	alias := hstoreComparisonValue(t, d, `"a"=>"bc"`)
	if equal, err := hash.Equal(ctx, a, alias); !equal || err != nil {
		t.Fatalf("normalized support equality %v/%v", equal, err)
	}
	extended, err := sqltype.BindPartitionHash(d.Usage(), hashClass)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := extended.Hash(ctx, a, 4294967296); int64(got) != 123372787139268927 || err != nil {
		t.Fatalf("extended support hash %d/%v", int64(got), err)
	}
	for _, mutation := range []struct {
		name string
		apply func(*sqltype.IndexSupport)
	}{
		{"builtin", func(s *sqltype.IndexSupport) { s.Provider = sqltype.BuiltinIndexSupport; s.Routine = 351 }},
		{"cube", func(s *sqltype.IndexSupport) { s.Provider = sqltype.CubeIndexSupport }},
		{"unknown_provider", func(s *sqltype.IndexSupport) { s.Provider = 255 }},
		{"foreign_family", func(s *sqltype.IndexSupport) { s.Family++ }},
		{"missing_member", func(s *sqltype.IndexSupport) { s.Member = 0 }},
		{"missing_routine", func(s *sqltype.IndexSupport) { s.Routine = 0 }},
		{"wrong_left", func(s *sqltype.IndexSupport) { s.Left++ }},
		{"wrong_right", func(s *sqltype.IndexSupport) { s.Right++ }},
		{"missing_revision", func(s *sqltype.IndexSupport) { s.Revision = 0 }},
		{"missing_family_revision", func(s *sqltype.IndexSupport) { s.FamilyRevision = 0 }},
		{"missing_routine_revision", func(s *sqltype.IndexSupport) { s.RoutineRevision = 0 }},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			s := c.Support(1)
			mutation.apply(&s)
			if _, err := sqltype.BindIndexOrdering(d.Usage(), c.WithSupport(1, s)); err == nil {
				t.Fatal("invalid ordering capture admitted")
			}
			for slot := 1; slot <= 2; slot++ {
				s := hashClass.Support(slot)
				mutation.apply(&s)
				bad := hashClass.WithSupport(slot, s)
				if _, err := sqltype.BindIndexHash(d.Usage(), bad); err == nil {
					t.Fatalf("invalid hash slot %d admitted", slot)
				}
				if _, err := sqltype.BindPartitionHash(d.Usage(), bad); err == nil {
					t.Fatalf("invalid extended hash slot %d admitted", slot)
				}
			}
		})
	}
	for _, support := range []sqltype.IndexSupport{{}, hashClass.Support(1)} {
		bad := hashClass.WithSupport(2, support)
		if _, err := sqltype.BindIndexHash(d.Usage(), bad); err == nil {
			t.Fatal("missing or duplicate extended support admitted")
		}
		if _, err := sqltype.BindPartitionHash(d.Usage(), bad); err == nil {
			t.Fatal("missing or duplicate extended support admitted by partition hash")
		}
	}
	stale := hashClass.Support(2)
	stale.FamilyRevision++
	if _, err := sqltype.BindIndexHash(d.Usage(), hashClass.WithSupport(2, stale)); err == nil {
		t.Fatal("incoherent family captures admitted")
	}
	for _, definition := range []sqltype.IndexClassDefinition{
		{OID: 65100, Method: 405, Family: 65101, Input: uint32(d.ID())},
		{OID: 0, Method: 403, Family: 65101, Input: uint32(d.ID())},
		{OID: 65100, Method: 403, Family: 0, Input: uint32(d.ID())},
		{OID: 65100, Method: 403, Family: 65101, Input: uint32(d.ID()+1)},
	} {
		bad := sqltype.NewIndexClass(definition).WithSupport(1, c.Support(1))
		if _, err := sqltype.BindIndexOrdering(d.Usage(), bad); err == nil {
			t.Fatal("wrong method/class/type admitted")
		}
	}
	other := hstoreUnitType(t, 65001)
	v := hstoreComparisonValue(t, other, "a=>bc")
	if _, err := hash.Hash(ctx, v); err == nil {
		t.Fatal("foreign hash value admitted")
	}
	if _, err := extended.Hash(ctx, v, 0); err == nil {
		t.Fatal("foreign extended hash value admitted")
	}
	if _, err := hash.Hash(ctx, sqltype.Null(d)); err == nil {
		t.Fatal("NULL reached hash support")
	}
}
