package parser

import (
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestRelationLockSyntax(t *testing.T) {
	for _, q := range []string{"LOCK TABLE t IN ACCESS EXCLUSIVE MODE", `lock /*x*/ table public."T" in access exclusive mode; -- end`, "LOCK TABLE lock IN ACCESS EXCLUSIVE MODE", "LOCK TABLE access.exclusive IN ACCESS EXCLUSIVE MODE"} {
		statements, err := Parse(q)
		if err != nil {
			t.Fatal(q, err)
		}
		if len(statements) != 1 {
			t.Fatal(q, statements)
		}
		lock, ok := statements[0].(*LockTable)
		if !ok || len(lock.Name.Parts) == 0 || lock.Name.Parts[0].Pos.Character < 1 || lock.Pos.Character != 1 {
			t.Fatalf("%s %#v", q, statements)
		}
	}
	for _, q := range []string{"LOCK t", "LOCK TABLE t", "LOCK TABLE ONLY t IN ACCESS EXCLUSIVE MODE", "LOCK TABLE t * IN ACCESS EXCLUSIVE MODE", "LOCK TABLE t,u IN ACCESS EXCLUSIVE MODE", "LOCK TABLE t IN ACCESS SHARE MODE", "LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", "LOCK TABLE t IN ACCESS EXCLUSIVE MODE extra", "LOCK TABLE t IN ACCESS EXCLUSIVE MODE SELECT * FROM t"} {
		if statements, err := Parse(q); err == nil || statements != nil {
			t.Fatalf("excluded form accepted: %s %v %v", q, statements, err)
		}
	}
	for _, name := range []string{"lock", "access", "exclusive", "mode"} {
		if _, err := Parse("CREATE TABLE " + name + "(" + name + " INT); SELECT " + name + " FROM " + name); err != nil {
			t.Fatal("contextual keyword stolen", name, err)
		}
	}
}

func TestRelationLockCapabilityBoundaries(t *testing.T) {
	for _, q := range []string{"LOCK t", "LOCK ONLY t IN ACCESS EXCLUSIVE MODE", "LOCK TABLE t", "LOCK TABLE t NOWAIT", "LOCK TABLE ONLY t IN ACCESS EXCLUSIVE MODE", "LOCK TABLE t * IN ACCESS EXCLUSIVE MODE", "LOCK TABLE t,u IN ACCESS EXCLUSIVE MODE", "LOCK TABLE t IN ACCESS SHARE MODE", "LOCK TABLE t IN ROW SHARE MODE", "LOCK TABLE t IN ROW EXCLUSIVE MODE", "LOCK TABLE t IN SHARE UPDATE EXCLUSIVE MODE", "LOCK TABLE t IN SHARE MODE", "LOCK TABLE t IN SHARE ROW EXCLUSIVE MODE", "LOCK TABLE t IN EXCLUSIVE MODE", "LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT"} {
		_, err := Parse(q)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" {
			t.Fatal(q, err)
		}
	}
	for _, q := range []string{"LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT extra", "LOCK TABLE t IN ACCESS INVALID MODE", "LOCK TABLE t IN ACCESS EXCLUSIVE", "LOCK TABLE t IN ACCESS EXCLUSIVE MODE extra"} {
		_, err := Parse(q)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" {
			t.Fatal(q, err)
		}
	}
}
