package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestConcurrentIndexU5AbsentRootPrivateTypeAndOwnPrefix(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(n INT)")
	sessionSQL(t, s, "INSERT INTO t VALUES(1),(1)")
	concurrentUnitError(t, s, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t((n+1))", "23505")
	concurrentUnitIndex(t, e, "ui", false, false)
	sessionSQL(t, s, "DELETE FROM t")
	sessionSQL(t, s, "BEGIN ISOLATION LEVEL READ COMMITTED")
	sessionSQL(t, s, "INSERT INTO t VALUES(2)")
	sessionSQL(t, s, "ALTER TABLE t ALTER n TYPE BIGINT")
	// Public committed observation cannot see private reconstruction; same-TX
	// producers must nevertheless use the real rebuilt private tuple baseline.
	concurrentUnitIndex(t, e, "ui", false, false)
	sessionSQL(t, s, "SAVEPOINT typed")
	concurrentUnitError(t, s, "INSERT INTO t VALUES(2)", "23505")
	sessionSQL(t, s, "ROLLBACK TO typed")
	sessionSQL(t, s, "INSERT INTO t VALUES(3)")
	sessionSQL(t, s, "COMMIT")
	index, root := concurrentUnitIndex(t, e, "ui", true, true)
	if !reflect.DeepEqual(btreeKeys(t, root), []string{"3", "4"}) {
		t.Fatal("sanctioned reconstruction lost pre-TYPE accepted prefix", btreeKeys(t, root))
	}
	concurrentUnitRootMatchesStorage(t, e, index)
	concurrentUnitError(t, s, "INSERT INTO t VALUES(2)", "23505")
	lockEmpty(t, e)
}

func TestConcurrentIndexU5TypeChecksWholePrivatePrefixAtExecute(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(n INT)")
	sessionSQL(t, s, "INSERT INTO t VALUES(1),(1)")
	concurrentUnitError(t, s, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(n)", "23505")
	sessionSQL(t, s, "DELETE FROM t")
	sessionSQL(t, s, "BEGIN ISOLATION LEVEL READ COMMITTED")
	sessionSQL(t, s, "INSERT INTO t VALUES(2),(2)")
	sessionSQL(t, s, "SAVEPOINT before_type")
	err := concurrentUnitError(t, s, "ALTER TABLE t ALTER n TYPE BIGINT", "23505")
	nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "23505", Message: `could not create unique index "ui"`, Detail: "Key (n)=(2) is duplicated.", Schema: "public", Table: "t", Constraint: "ui"})
	concurrentUnitIndex(t, e, "ui", false, false)
	sessionSQL(t, s, "ROLLBACK TO before_type")
	// Failed TYPE did not retroactively turn the original admission into UNIQUE.
	sessionSQL(t, s, "INSERT INTO t VALUES(2)")
	sessionSQL(t, s, "ROLLBACK")
	concurrentUnitIndex(t, e, "ui", false, false)
	lockEmpty(t, e)
}

func TestConcurrentIndexU5BuildRepairAndTypeDisclosure(t *testing.T) {
	for _, kind := range []string{"simple", "parenthesized", "collated", "computed"} {
		computed := kind == "computed"
		for _, access := range []string{"hidden", "key_columns", "table"} {
			t.Run(kind+"/"+access, func(t *testing.T) {
				e := New()
				admin, adminDone := roleOperation(t, e, "postgres")
				defer adminDone()
				roleExecute(t, e, admin, "CREATE ROLE index_owner LOGIN")
				owner, ownerDone := roleOperation(t, e, "index_owner")
				defer ownerDone()
				roleExecute(t, e, owner, "CREATE TABLE t(v TEXT,n INT,payload TEXT)")
				seed := "INSERT INTO t VALUES('A',1,'protected'),('a',1,'different')"
				if kind == "collated" {
					seed = "INSERT INTO t VALUES('A',1,'protected'),('A',1,'different')"
				}
				roleExecute(t, e, owner, seed)
				ref := roleTableRef(t, e, "t")
				actor, ok := e.Roles().Lookup("index_owner")
				if !ok || actor.Superuser {
					t.Fatal("nonsuperuser owner fixture", actor, ok)
				}
				grant := func(attribute int32, privileges authorization.Privileges) {
					target := ref
					target.Address.SubID = attribute
					acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: actor.Ref, Grantee: authorization.ACLGrantee{Role: actor.Ref}, Privileges: privileges}}}
					if err := e.SetObjectACL(context.Background(), target, acl); err != nil {
						t.Fatal(err)
					}
				}
				// REINDEX separately requires MAINTAIN; this fixture revokes
				// SELECT, not the authority needed to reach duplicate checking.
				grant(0, authorization.InsertPrivilege|authorization.MaintainPrivilege)
				if access == "table" {
					grant(0, authorization.InsertPrivilege|authorization.MaintainPrivilege|authorization.SelectPrivilege)
				}
				if access == "key_columns" {
					grant(1, authorization.SelectPrivilege)
					grant(2, authorization.SelectPrivilege)
				}
				definition := "n,n"
				detail := "Key (n, n)=(1, 1) is duplicated."
				typeDetail := "Key (n, n)=(0, 0) is duplicated."
				writeDetail := "Key (n, n)=(1, 1) already exists."
				if kind == "parenthesized" {
					definition = "(n),(n)"
				}
				if kind == "collated" {
					definition = `(v) COLLATE "C" text_ops,n`
					detail = "Key (v, n)=(A, 1) is duplicated."
					typeDetail = "Key (v, n)=(A, 0) is duplicated."
					writeDetail = "Key (v, n)=(A, 1) already exists."
				}
				if computed {
					definition = `lower(v) COLLATE "C" text_ops,(n+1)`
					detail = "Key (lower(v), (n + 1))=(a, 2) is duplicated."
					typeDetail = "Key (lower(v), (n + 1))=(a, 1) is duplicated."
					writeDetail = "Key (lower(v), (n + 1))=(a, 2) already exists."
				}
				if access == "hidden" || (computed && access != "table") {
					detail, typeDetail, writeDetail = "Duplicate keys exist.", "Duplicate keys exist.", ""
				}
				_, err := e.Execute(owner, statement(t, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t("+definition+") INCLUDE(payload)"), 0, nil)
				nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "23505", Message: `could not create unique index "ui"`, Detail: detail, Schema: "public", Table: "t", Constraint: "ui"})
				initial, _ := concurrentUnitIndex(t, e, "ui", false, false)
				_, err = e.Execute(owner, statement(t, "REINDEX INDEX ui"), 0, nil)
				nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "23505", Message: `could not create unique index "ui"`, Detail: detail, Schema: "public", Table: "t", Constraint: "ui"})
				before := e.CatalogRead().state
				_, err = e.Execute(owner, statement(t, "ALTER TABLE t ALTER n TYPE BIGINT USING 0"), 0, nil)
				nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "23505", Message: `could not create unique index "ui"`, Detail: typeDetail, Schema: "public", Table: "t", Constraint: "ui"})
				unchanged, _ := concurrentUnitIndex(t, e, "ui", false, false)
				if e.CatalogRead().state != before || unchanged != initial {
					t.Fatal("failed standalone TYPE changed committed authority")
				}
				roleExecute(t, e, admin, "DELETE FROM t")
				roleExecute(t, e, admin, "INSERT INTO t VALUES('A',1,'protected')")
				roleExecute(t, e, owner, "REINDEX INDEX ui")
				healthy, _ := concurrentUnitIndex(t, e, "ui", true, true)
				concurrentUnitRootMatchesStorage(t, e, healthy)
				write := "INSERT INTO t VALUES('a',1,'different')"
				if kind == "collated" {
					write = "INSERT INTO t VALUES('A',1,'different')"
				}
				_, err = e.Execute(owner, statement(t, write), 0, nil)
				nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "23505", Message: `duplicate key value violates unique constraint "ui"`, Detail: writeDetail, Schema: "public", Table: "t", Constraint: "ui"})
				lockEmpty(t, e)
			})
		}
	}
}
