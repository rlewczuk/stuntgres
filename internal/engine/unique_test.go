package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestUniqueBackingRootsAndAtomicity(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(k INT CONSTRAINT u UNIQUE,v TEXT)")
	ref := roleTableRef(t, e, "t")
	parent := e.catalog.state.MustRelation(ref.Address)
	if len(parent.Constraints.Unique) != 1 {
		t.Fatal(parent.Constraints.Unique)
	}
	c := parent.Constraints.Unique[0]
	index, empty := btreeRoot(t, e, "u")
	if c.OID == 0 || c.Index != index.Address() || !index.SQLIndex.Unique || index.SQLIndex.Constraint == nil || index.SQLIndex.Constraint.OID != c.OID || index.SQLIndex.Parent != parent.Address() || index.Owner != parent.Owner || index.Namespace() != parent.Namespace() || len(empty.Entries) != 0 {
		t.Fatal("broken parent/index closure")
	}
	if index.ColumnMeta.HighWater != int32(len(index.Attributes)) {
		t.Fatal("backing column identities exceed their allocation high-water")
	}
	run(t, e, "INSERT INTO t VALUES(1,'a'),(NULL,'n'),(NULL,'n')")
	old := btreeWantKeys(t, e, "u", "1", "NULL", "NULL")
	for _, query := range []string{"INSERT INTO t VALUES(2,'b'),(1,'dup')", "UPDATE t SET k=1 WHERE k IS NULL"} {
		result, err := testExecute(e, context.Background(), statement(t, query), nil)
		if result.Count != 0 || result.Rows != nil {
			t.Fatal(result)
		}
		nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "23505", Message: `duplicate key value violates unique constraint "u"`, Detail: "Key (k)=(1) already exists.", Schema: "public", Table: "t", Constraint: "u"})
		if e.storage.IndexRoots[index.Address()] != old {
			t.Fatal("failed batch published root")
		}
		lockEmpty(t, e)
	}
	run(t, e, "DELETE FROM t WHERE k=1")
	run(t, e, "INSERT INTO t VALUES(1,'again')")
	if !reflect.DeepEqual(btreeKeys(t, old), []string{"1", "NULL", "NULL"}) {
		t.Fatal("retained root mutated")
	}
	run(t, e, "ALTER TABLE t ALTER k TYPE BIGINT")
	next := e.catalog.state.MustRelation(ref.Address)
	if next.Address() != parent.Address() || next.Constraints.Unique[0].OID == c.OID || next.Constraints.Unique[0].Index == c.Index {
		t.Fatal("TYPE identity contract")
	}
	if _, ok := e.catalog.state.Find(c.Index); ok {
		t.Fatal("retained old backing object")
	}
	btreeWantKeys(t, e, "u", "1", "NULL", "NULL")
	run(t, e, "ALTER TABLE t DROP CONSTRAINT u")
	if len(e.storage.IndexRoots) != 0 {
		t.Fatal("orphan backing root")
	}
	run(t, e, "INSERT INTO t VALUES(1,'duplicate allowed')")
}

func TestUniqueClaimInterningAndRetirement(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(k NUMERIC CONSTRAINT u UNIQUE)")
	transactionBegin(t, s)
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(nil)
	if err := scope.gate(statement(t, "INSERT INTO t VALUES(1)")); err != nil {
		t.Fatal(err)
	}
	idx, _ := btreeRoot(t, e, "u")
	ref := e.catalog.Domain.Ref(e.catalog.state.Epoch(), idx.Address())
	parent := e.catalog.state.MustRelation(idx.SQLIndex.Parent)
	ordering, err := index.BindIndexOrdering(operatorFamilyTestRead(), parent, idx.SQLIndex)
	if err != nil {
		t.Fatal(err)
	}
	typ := idx.Columns[0].Usage.Type
	group, err := sqltype.BindGrouping(idx.Columns[0].Usage)
	if err != nil {
		t.Fatal(err)
	}
	grouping := []sqltype.Grouping{group}
	one, err := typ.Input(context.Background(), "1", -1)
	if err != nil {
		t.Fatal(err)
	}
	scaled, err := typ.Input(context.Background(), "1.00", -1)
	if err != nil {
		t.Fatal(err)
	}
	two, err := typ.Input(context.Background(), "2", -1)
	if err != nil {
		t.Fatal(err)
	}
	// A legitimate hash-collision candidate interned first sits ahead of the
	// real key in its bucket. Hash alone must not grant equality, and provider
	// comparison must pass over the collision to find the real key.
	hash, err := index.UniqueKeyHash(context.Background(), ordering, grouping, []sqltype.Value{one})
	if err != nil {
		t.Fatal(err)
	}
	collision, releaseCollision, err := e.locks.PinUniqueKey(context.Background(), scope.ticket, ref, hash, &index.UniqueKeyValues{Ordering: ordering, Values: []sqltype.Value{two}})
	if err != nil {
		t.Fatal(err)
	}
	first, releaseFirst, err := e.pinUniqueKey(context.Background(), scope.ticket, ref, ordering, grouping, []sqltype.Value{one})
	if err != nil {
		t.Fatal(err)
	}
	if first.Key() == collision.Key() {
		t.Fatal("hash collision established equality")
	}
	equal, releaseEqual, err := e.pinUniqueKey(context.Background(), scope.ticket, ref, ordering, grouping, []sqltype.Value{scaled})
	if err != nil {
		t.Fatal(err)
	}
	if equal.Key() != first.Key() {
		t.Fatal("normalized numeric equality split claims")
	}
	again, releaseAgain, err := e.pinUniqueKey(context.Background(), scope.ticket, ref, ordering, grouping, []sqltype.Value{one})
	if err != nil {
		t.Fatal(err)
	}
	if again.Key() != first.Key() || again.Key() == collision.Key() {
		t.Fatal("hash collision established equality")
	}
	releaseAgain()
	releaseEqual()
	releaseFirst()
	releaseCollision()
	lockState := e.locks.Inspect()
	left := lockState.KeyBuckets
	if left != 0 {
		t.Fatal("empty interner buckets retained", left)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, release, err := e.pinUniqueKey(canceled, scope.ticket, ref, ordering, grouping, []sqltype.Value{one}); !errors.Is(err, context.Canceled) || release != nil {
		t.Fatal(err)
	}
}

func TestUniqueInsertPrivilegeDisclosure(t *testing.T) {
	for _, mode := range []string{"none", "partial", "keys", "table"} {
		for _, prepared := range []bool{false, true} {
			t.Run(mode+map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
				e := New()
				admin, done := roleOperation(t, e, "postgres")
				defer done()
				roleExecute(t, e, admin, "CREATE ROLE unique_actor LOGIN")
				roleExecute(t, e, admin, "CREATE TABLE t(a INT,b TEXT,secret TEXT DEFAULT 'private',CONSTRAINT u UNIQUE(a,b))")
				roleExecute(t, e, admin, "INSERT INTO t(a,b) VALUES(1,'x')")
				ref := roleTableRef(t, e, "t")
				actor, _ := e.Roles().Lookup("unique_actor")
				grant := func(attribute int32, privileges authorization.Privileges) {
					target := ref
					target.Address.SubID = attribute
					if err := e.SetObjectACL(context.Background(), target, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Grantee: authorization.ACLGrantee{Role: actor.Ref}, Privileges: privileges}}}); err != nil {
						t.Fatal(err)
					}
				}
				grant(1, authorization.InsertPrivilege)
				grant(2, authorization.InsertPrivilege)
				switch mode {
				case "partial":
					grant(1, authorization.InsertPrivilege|authorization.SelectPrivilege)
				case "keys":
					grant(1, authorization.InsertPrivilege|authorization.SelectPrivilege)
					grant(2, authorization.InsertPrivilege|authorization.SelectPrivilege)
				case "table":
					grant(0, authorization.SelectPrivilege)
				}
				op, finish := roleOperation(t, e, "unique_actor")
				defer finish()
				query := "INSERT INTO t(a,b) VALUES(1,'x')"
				count := 0
				if prepared {
					query = "INSERT INTO t(a,b) VALUES($1,'x')"
					count = 1
				}
				ast := statement(t, query)
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				if prepared {
					description, err := scope.Describe(ast)
					if err != nil {
						scope.Finish(err)
						t.Fatal(err)
					}
					scope.Expected = &description
				}
				result, err := scope.Execute(ast, count, func(ctx context.Context, parameters analyze.ParameterInfo, frame []sqltype.Value, input sqltype.InputEnvironment) error {
					value, err := sqltype.Integer(parameters.Types[0].Type, 1)
					frame[0] = value
					return err
				})
				scope.Finish(err)
				if result.Count != 0 || result.Rows != nil {
					t.Fatal(result)
				}
				detail := ""
				if mode == "keys" || mode == "table" {
					detail = "Key (a, b)=(1, x) already exists."
				}
				nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "23505", Message: `duplicate key value violates unique constraint "u"`, Detail: detail, Schema: "public", Table: "t", Constraint: "u"})
				lockEmpty(t, e)
			})
		}
	}
}
