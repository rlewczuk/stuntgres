package tablecmds_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestAlterDropColumnIdentityAndSnapshots(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE t(a INT NOT NULL,b TEXT,c BIGINT)")
	enginetest.Run(t, e, "INSERT INTO t VALUES(1,'drop',9),(2,NULL,NULL)")
	ref := enginetest.RoleTableRef(t, e, "t")
	old := e.Catalog.State.MustRelation(ref.Address)
	original := e.Storage.Relations[ref.Address]
	held := enginetest.Run(t, e, "SELECT * FROM t ORDER BY a")
	enginetest.Run(t, e, "ALTER TABLE t DROP COLUMN b RESTRICT")
	next := e.Catalog.State.MustRelation(ref.Address)
	if next == old || next.Address() != old.Address() || next.Revision() != old.Revision()+1 || !reflect.DeepEqual(next.Attributes, []int32{1, 3}) || next.Index["c"] != 1 || len(old.Columns) != 3 || len(original.Versions[0].Values) != 3 {
		t.Fatal(old, next)
	}
	for i, row := range e.Storage.Relations[ref.Address].Versions {
		before := original.Versions[i]
		if row.Row != before.Row || row.Version != before.Version || row.Born != before.Born {
			t.Fatal(row, before)
		}
	}
	if got := enginetest.Tuples(held); !reflect.DeepEqual(got, [][]driver.Value{{int64(1), "drop", int64(9)}, {int64(2), nil, nil}}) {
		t.Fatal(got)
	}
	result := enginetest.Run(t, e, "SELECT * FROM t ORDER BY a")
	if result.Rows.Schema()[1].OriginAttribute != 3 || result.Rows.Schema()[0].Nullable || !result.Rows.Schema()[1].Nullable {
		t.Fatal(result.Rows.Schema())
	}
	if got := enginetest.Tuples(result); !reflect.DeepEqual(got, [][]driver.Value{{int64(1), int64(9)}, {int64(2), nil}}) {
		t.Fatal(got)
	}
	dead, live := ref, ref
	dead.Address.SubID = 2
	live.Address.SubID = 3
	if enginetest.ValidateObjectRef(e, e.CatalogRead(), dead) == nil {
		t.Fatal("dead attribute accepted")
	}
	if err := enginetest.ValidateObjectRef(e, e.CatalogRead(), live); err != nil {
		t.Fatal(err)
	}
	enginetest.Run(t, e, "ALTER TABLE ONLY(t) DROP a CASCADE")
	enginetest.Run(t, e, "ALTER TABLE t* DROP c")
	zero := enginetest.Run(t, e, "SELECT * FROM t")
	if len(zero.Rows.Schema()) != 0 || zero.Count != 2 {
		t.Fatal(zero)
	}
	zero.Rows.Close()
}

func alterDropTestStage(t *testing.T, e *engine.Engine) (*engine.CatalogStage, ids.ObjectRef, *catalog.Relation) {
	t.Helper()
	ref := enginetest.RoleTableRef(t, e, "t")
	old := e.Catalog.State.MustRelation(ref.Address)
	next := *old
	next.ObjectRevision++
	next.Columns = append([]catalog.Column(nil), old.Columns[0])
	next.Attributes = []int32{1}
	next.Index = map[string]int{"a": 0}
	next.Security.ColumnACL = nil
	original := e.Storage.Relations[ref.Address]
	replacement := &storage.Relation{Versions: make([]storage.RowVersion, len(original.Versions)), Ends: original.Ends}
	for i, row := range original.Versions {
		row.Values = append([]sqltype.Value(nil), row.Values[0])
		replacement.Versions[i] = row
	}
	stage := e.BeginCatalogStage(e.CatalogRead())
	if err := stage.AlterRelation(context.Background(), ref, &next, original, replacement, len(original.Versions), original.Generation, original.Ends); err != nil {
		t.Fatal(err)
	}
	return stage, ref, old
}

func TestAlterDropColumnStageRejectsConcurrentInsert(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE t(a INT,b TEXT)")
	enginetest.Run(t, e, "INSERT INTO t VALUES(1,'one')")
	stage, ref, old := alterDropTestStage(t, e)
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	enginetest.Run(t, e, "INSERT INTO t VALUES(2,'two')")
	change, err := enginetest.PublishCatalog(e, prepared, engine.PublicationToken{Epoch: ref.Epoch})
	var conflict catalog.Conflict
	if !errors.As(err, &conflict) || len(change.Definitions) != 0 || e.Catalog.State.Get(ref.Address) != old {
		t.Fatal(change, err)
	}
	if got := enginetest.Tuples(enginetest.Run(t, e, "SELECT * FROM t ORDER BY a")); !reflect.DeepEqual(got, [][]driver.Value{{int64(1), "one"}, {int64(2), "two"}}) {
		t.Fatal(got)
	}
}

func TestAlterDropColumnFailuresAndSkip(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE TABLE t(a INT,b TEXT)")
	enginetest.RoleExecute(t, e, op, "INSERT INTO t VALUES(1,'keep')")
	ref := enginetest.RoleTableRef(t, e, "t")
	old := e.Catalog.State.Get(ref.Address)
	root := e.Storage.Relations[ref.Address]
	for _, tc := range []struct{ q, code, message string }{{"ALTER TABLE t DROP missing", "42703", `column "missing" of relation "t" does not exist`}, {"ALTER TABLE missing DROP b", "42P01", `relation "missing" does not exist`}, {"ALTER TABLE t DROP COLUMN ctid", "0A000", `cannot drop system column "ctid"`}, {"ALTER TABLE t DROP IF EXISTS xmin", "0A000", `cannot drop system column "xmin"`}} {
		_, err := e.Execute(op, enginetest.Statement(t, tc.q), 0, nil)
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message}
		if !errors.As(err, &got) || *got != want {
			t.Fatal(tc.q, got, err)
		}
		if e.Catalog.State.Get(ref.Address) != old || e.Storage.Relations[ref.Address] != root {
			t.Fatal("failure changed state")
		}
	}
	enginetest.RoleExecute(t, e, op, "ALTER TABLE t DROP COLUMN IF EXISTS missing")
	want := []sqlstate.Error{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `column "missing" of relation "t" does not exist, skipping`}}
	if got := op.DetachNotices(); !reflect.DeepEqual(got, want) {
		t.Fatal(got)
	}
	if e.Catalog.State.Get(ref.Address) != old || e.Storage.Relations[ref.Address] != root {
		t.Fatal("skip changed state")
	}
	var before bytes.Buffer
	if err := engine.Dump(context.Background(), e, &before); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := enginetest.Execute(e, ctx, enginetest.Statement(t, "ALTER TABLE t DROP b"), nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	var after bytes.Buffer
	if err := engine.Dump(context.Background(), e, &after); err != nil || before.String() != after.String() {
		t.Fatal(err, after.String())
	}
}

func TestAlterDropColumnStageCancellationAndReplacement(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		e := engine.New()
		enginetest.Run(t, e, "CREATE TABLE t(a INT,b TEXT)")
		enginetest.Run(t, e, "INSERT INTO t VALUES(1,'keep')")
		stage, ref, old := alterDropTestStage(t, e)
		ctx, cancel := context.WithCancel(context.Background())
		prepared, err := e.ValidateCatalogStage(ctx, stage)
		if err != nil {
			t.Fatal(err)
		}
		if replacement {
			if err = engine.Reset(context.Background(), e); err != nil {
				t.Fatal(err)
			}
		} else {
			cancel()
		}
		changes, err := enginetest.PublishCatalog(e, prepared, engine.PublicationToken{Epoch: ref.Epoch})
		cancel()
		if len(changes.Definitions) != 0 || err == nil {
			t.Fatal(changes, err)
		}
		if !replacement && (e.Catalog.State.Get(ref.Address) != old || !errors.Is(err, context.Canceled)) {
			t.Fatal(err)
		}
		if replacement && !errors.Is(err, ids.ErrCatalogReplaced) {
			t.Fatal(err)
		}
	}
}

func TestAlterDropColumnACLCleanupAndSurvivorAuthority(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE USER dropped_grantee")
	enginetest.RoleExecute(t, e, op, "CREATE USER survivor_grantee")
	enginetest.RoleExecute(t, e, op, "CREATE TABLE t(a INT,b TEXT,c BIGINT)")
	ref := enginetest.RoleTableRef(t, e, "t")
	owner := op.RoleState().CurrentUser
	for sub, name := range map[int32]string{2: "dropped_grantee", 3: "survivor_grantee"} {
		role, _ := e.Roles().Lookup(name)
		column := ref
		column.Address.SubID = sub
		acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: role.Ref}, Privileges: authorization.SelectPrivilege}}}
		if err := enginetest.SetObjectACL(e, context.Background(), column, acl); err != nil {
			t.Fatal(err)
		}
	}
	old := e.Catalog.State.MustRelation(ref.Address)
	enginetest.RoleExecute(t, e, op, "ALTER TABLE t DROP b CASCADE")
	next := e.Catalog.State.MustRelation(ref.Address)
	if len(next.Security.ColumnACL) != 1 || !reflect.DeepEqual(next.Security.ColumnACL[3], old.Security.ColumnACL[3]) || len(old.Security.ColumnACL) != 2 {
		t.Fatal(old.Security.ColumnACL, next.Security.ColumnACL)
	}
	enginetest.RoleExecute(t, e, op, "DROP ROLE dropped_grantee")
	_, err := e.Execute(op, enginetest.Statement(t, "DROP ROLE survivor_grantee"), 0, nil)
	var dependency *sqlstate.Error
	wantDependency := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: `role "survivor_grantee" cannot be dropped because some objects depend on it`, Detail: "privileges for column c of table t"}
	if !errors.As(err, &dependency) || *dependency != wantDependency {
		t.Fatal(err, dependency)
	}
	dead, live := ref, ref
	dead.Address.SubID = 2
	live.Address.SubID = 3
	if enginetest.SetObjectACL(e, context.Background(), dead, authorization.ACLValue{Explicit: true}) == nil {
		t.Fatal("dead ACL accepted")
	}
	if err = enginetest.SetObjectACL(e, context.Background(), live, next.Security.ColumnACL[3]); err != nil {
		t.Fatal(err)
	}
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION survivor_grantee")
	enginetest.RoleExecute(t, e, op, "SELECT c FROM t")
	_, err = e.Execute(op, enginetest.Statement(t, "ALTER TABLE t DROP IF EXISTS missing"), 0, nil)
	enginetest.State(t, err, "42501")
}

type alterDropProvider struct {
	catalog.Provider
	callback func()
}

func (p alterDropProvider) Validate(v catalog.Read, d catalog.Descriptor) error {
	if err := p.Provider.Validate(v, d); err != nil {
		return err
	}
	p.callback()
	return nil
}

func TestAlterDropColumnProviderCancellationAndReset(t *testing.T) {
	for _, reset := range []bool{false, true} {
		e := engine.New()
		enginetest.Run(t, e, "CREATE TABLE t(a INT,b TEXT)")
		enginetest.Run(t, e, "INSERT INTO t VALUES(1,'keep')")
		ref := enginetest.RoleTableRef(t, e, "t")
		old := e.Catalog.State.Get(ref.Address)
		original := e.Storage.Relations[ref.Address]
		ctx, cancel := context.WithCancel(context.Background())
		var triggered atomic.Bool
		e.Catalog.Providers[ids.RelationClassID] = alterDropProvider{e.Catalog.Providers[ids.RelationClassID], func() {
			if !triggered.CompareAndSwap(false, true) {
				return
			}
			if reset {
				if err := engine.Reset(context.Background(), e); err != nil {
					t.Fatal(err)
				}
			} else {
				cancel()
			}
		}}
		_, err := enginetest.Execute(e, ctx, enginetest.Statement(t, "ALTER TABLE t DROP b"), nil)
		cancel()
		if reset {
			if !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatal(err)
			}
		} else {
			if !errors.Is(err, context.Canceled) || e.Catalog.State.Get(ref.Address) != old || e.Storage.Relations[ref.Address] != original {
				t.Fatal(err)
			}
		}
	}
}

func TestAlterDropColumnTransformCancellationAndRevisionExhaustion(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE t(a INT,b TEXT)")
	enginetest.Run(t, e, "INSERT INTO t VALUES(1,'keep'),(2,'keep'),(3,'keep')")
	ref := enginetest.RoleTableRef(t, e, "t")
	old := e.Catalog.State.Get(ref.Address)
	original := e.Storage.Relations[ref.Address]
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Stage transformation checkpoints use the real cancelable parent context.
	op, cleanup, err := enginetest.Operation(e, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	counting := &cancelAtErrCheck{Context: op.Context(), cancel: cancel, at: 12}
	op.Ctx = counting
	_, err = e.Execute(op, enginetest.Statement(t, "ALTER TABLE t DROP b"), 0, nil)
	if !errors.Is(err, context.Canceled) || e.Catalog.State.Get(ref.Address) != old || e.Storage.Relations[ref.Address] != original {
		t.Fatal(err)
	}
	maximum := *old.(*catalog.Relation)
	maximum.ObjectRevision = ids.DefinitionRevision(^uint64(0))
	stage := e.BeginCatalogStage(e.CatalogRead())
	if err = stage.Replace(&maximum); err != nil {
		t.Fatal(err)
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = enginetest.PublishCatalog(e, prepared, engine.PublicationToken{Epoch: ref.Epoch}); err != nil {
		t.Fatal(err)
	}
	_, err = enginetest.Execute(e, context.Background(), enginetest.Statement(t, "ALTER TABLE t DROP b"), nil)
	if err == nil || err.Error() != "stuntgres: definition revision exhausted" || e.Catalog.State.Get(ref.Address) != &maximum || e.Storage.Relations[ref.Address] != original {
		t.Fatal(err)
	}
}

func TestAlterDropColumnSchemaOwnerIsNotTableOwner(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE USER schema_owner")
	enginetest.RoleExecute(t, e, op, "CREATE SCHEMA s AUTHORIZATION schema_owner")
	enginetest.RoleExecute(t, e, op, "CREATE TABLE s.t(a INT,b TEXT)")
	ref := enginetest.RoleTableRef(t, e, "s.t")
	old := e.Catalog.State.Get(ref.Address)
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION schema_owner")
	for _, q := range []string{"ALTER TABLE s.t DROP b", "ALTER TABLE s.t DROP IF EXISTS missing"} {
		_, err := e.Execute(op, enginetest.Statement(t, q), 0, nil)
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "must be owner of table t"}
		if !errors.As(err, &got) || *got != want || e.Catalog.State.Get(ref.Address) != old {
			t.Fatal(got, err)
		}
	}
}

func TestAlterDropColumnStageProviderFailureIsAtomic(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE t(a INT,b TEXT)")
	enginetest.Run(t, e, "INSERT INTO t VALUES(1,'keep')")
	stage, ref, old := alterDropTestStage(t, e)
	original := e.Storage.Relations[ref.Address]
	// Exercise the real provider's validation, not a fabricated callback error.
	stage.Get(ref.Address).(*catalog.Relation).Index["a"] = 9
	_, err := e.ValidateCatalogStage(context.Background(), stage)
	if err == nil || err.Error() != "stuntgres: invalid relation column identity" || e.Catalog.State.Get(ref.Address) != old || e.Storage.Relations[ref.Address] != original {
		t.Fatal(err)
	}
	if got := enginetest.Tuples(enginetest.Run(t, e, "SELECT * FROM t")); !reflect.DeepEqual(got, [][]driver.Value{{int64(1), "keep"}}) {
		t.Fatal(got)
	}
}

// cancelAtErrCheck drives existing cooperative cancellation boundaries without
// sleeps or production hooks. Its embedded real context owns Done and Err state.
type cancelAtErrCheck struct {
	context.Context
	cancel context.CancelFunc
	at     int32
	checks atomic.Int32
}

func (c *cancelAtErrCheck) Err() error {
	if c.checks.Add(1) == c.at {
		c.cancel()
	}
	return c.Context.Err()
}
