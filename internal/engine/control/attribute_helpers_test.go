package control

import (
	"context"
	"reflect"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"testing"
)

func TestAccessRelationsDetachedSchedule(t *testing.T) {
	child := attributeUnitRelation(42000, "c", 1)
	attributeUnitHole(child, 1, false, 1)
	helper := attributeUnitRelation(42000, AttributeHelperStem+"abc", 1)
	helper.ObjectAddress.SubID = -1
	attributeUnitHole(helper, 1, true, 0)
	model := *child
	attributeUnitParent(&model, helper)
	result, err := scheduleAttributes(context.Background(), []*catalog.Relation{&model, helper}, map[ids.ObjectAddress]ids.ObjectAddress{helper.Address(): child.Address()})
	if err != nil {
		t.Fatal(err)
	}
	if child.Inheritance != nil || child.ColumnMeta.Dropped[1].Local {
		t.Fatal("model mutated source")
	}
	retired, detached, removed := -1, -1, -1
	events := make(map[dump.Event]bool)
	for i, op := range result.Operations {
		event := attributeOperationEvent(op, i)
		if events[event] {
			t.Fatal("operation identity collision", event)
		}
		events[event] = true
		switch op.Kind {
		case 'd':
			retired = i
		case 'n':
			detached = i
		case 'x':
			removed = i
		}
	}
	if retired < 0 || detached <= retired || removed <= detached || !result.Relations[helper.Address()].Removed {
		t.Fatal("helper lifetime", result.Operations)
	}
	if result.Groups[attributeSlot{child.Address(), 1}] != (attributeSlot{helper.Address(), 1}) {
		t.Fatal("synthetic provider", result.Groups)
	}
}

func TestAccessRelationsHelperAdmission(t *testing.T) {
	const helper = "public.__stuntgres_attribute_history_abc"
	const column = "__stuntgres_retired_abc_1"
	script := "CREATE TABLE " + helper + "(); CREATE TABLE public.c() INHERITS(" + helper + "); ALTER TABLE " + helper + " ADD COLUMN " + column + " pg_catalog.int4; ALTER TABLE " + helper + " DROP COLUMN " + column + "; ALTER TABLE public.c NO INHERIT " + helper + "; DROP TABLE " + helper + ";"
	statements, err := parser.Parse(script)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := parser.Parse(script)
	pairs, err := PairAttributeConstructors(statements)
	if err != nil || len(pairs.Adds) != 1 || len(pairs.Drops) != 1 || len(pairs.Detaches) != 1 || len(pairs.Teardowns) != 1 {
		t.Fatal("complete helper manifest", pairs, err)
	}
	if !reflect.DeepEqual(statements, before) {
		t.Fatal("helper pairing mutated AST")
	}
	for _, bad := range []string{
		strings.Replace(script, "ALTER TABLE public.c NO INHERIT "+helper+";", "", 1),
		strings.Replace(script, "DROP TABLE "+helper+";", "DROP TABLE "+helper+" CASCADE;", 1),
		strings.Replace(script, "ALTER TABLE "+helper+" DROP COLUMN", "ALTER TABLE ONLY "+helper+" DROP COLUMN", 1),
		strings.Replace(script, "ALTER TABLE "+helper+" DROP COLUMN", "INSERT INTO public.c DEFAULT VALUES; ALTER TABLE "+helper+" DROP COLUMN", 1),
		strings.Replace(script, "pg_catalog.int4", "INT", 1),
	} {
		statements, err := parser.Parse(bad)
		if err != nil {
			t.Fatal(err)
		}
		pairs, err := PairAttributeConstructors(statements)
		if err == nil && len(pairs.Teardowns) != 0 {
			t.Fatal("malformed helper teardown admitted", bad)
		}
	}
	statements, err = parser.Parse("DROP TABLE " + helper)
	if err != nil {
		t.Fatal(err)
	}
	pairs, err = PairAttributeConstructors(statements)
	if err != nil || len(pairs.Teardowns) != 0 {
		t.Fatal("prefix-only teardown", err)
	}
}
