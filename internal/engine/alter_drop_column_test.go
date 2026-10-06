package engine

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestAlterDropColumnIdentityAndSnapshots(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(a INT NOT NULL,b TEXT,c BIGINT)")
	run(t, e, "INSERT INTO t VALUES(1,'drop',9),(2,NULL,NULL)")
	ref := roleTableRef(t, e, "t")
	old := e.state.descriptors[ref.Address].(*relationDescriptor)
	original := e.storage[ref.Address]
	held := run(t, e, "SELECT * FROM t ORDER BY a")
	run(t, e, "ALTER TABLE t DROP COLUMN b RESTRICT")
	next := e.state.descriptors[ref.Address].(*relationDescriptor)
	if next == old || next.address != old.address || next.revision != old.revision+1 || !reflect.DeepEqual(next.attributes, []int32{1, 3}) || next.index["c"] != 1 || len(old.columns) != 3 || len(original.versions[0].values) != 3 {
		t.Fatal(old, next)
	}
	for i, row := range e.storage[ref.Address].versions {
		before := original.versions[i]
		if row.row != before.row || row.version != before.version || row.born != before.born {
			t.Fatal(row, before)
		}
	}
	if got := tuples(held); !reflect.DeepEqual(got, [][]driver.Value{{int64(1), "drop", int64(9)}, {int64(2), nil, nil}}) {
		t.Fatal(got)
	}
	result := run(t, e, "SELECT * FROM t ORDER BY a")
	if result.Rows.Schema()[1].OriginAttribute != 3 || result.Rows.Schema()[0].Nullable || !result.Rows.Schema()[1].Nullable {
		t.Fatal(result.Rows.Schema())
	}
	if got := tuples(result); !reflect.DeepEqual(got, [][]driver.Value{{int64(1), int64(9)}, {int64(2), nil}}) {
		t.Fatal(got)
	}
	dead, live := ref, ref
	dead.Address.SubID = 2
	live.Address.SubID = 3
	if e.ValidateObjectRef(e.CatalogRead(), dead) == nil {
		t.Fatal("dead attribute accepted")
	}
	if err := e.ValidateObjectRef(e.CatalogRead(), live); err != nil {
		t.Fatal(err)
	}
	run(t, e, "ALTER TABLE ONLY(t) DROP a CASCADE")
	run(t, e, "ALTER TABLE t* DROP c")
	zero := run(t, e, "SELECT * FROM t")
	if len(zero.Rows.Schema()) != 0 || zero.Count != 2 {
		t.Fatal(zero)
	}
	zero.Rows.Close()
}

func alterDropTestStage(t *testing.T, e *Engine) (*CatalogStage, ObjectRef, *relationDescriptor) {
	t.Helper()
	ref := roleTableRef(t, e, "t")
	old := e.state.descriptors[ref.Address].(*relationDescriptor)
	next := *old
	next.revision++
	next.columns = append([]ColumnDescriptor(nil), old.columns[0])
	next.attributes = []int32{1}
	next.index = map[string]int{"a": 0}
	next.columnACL = nil
	original := e.storage[ref.Address]
	replacement := &relationStorage{versions: make([]rowVersion, len(original.versions)), ends: original.ends}
	for i, row := range original.versions {
		row.values = append([]sqltype.Value(nil), row.values[0])
		replacement.versions[i] = row
	}
	stage := e.BeginCatalogStage(e.CatalogRead())
	if err := stage.alterRelation(context.Background(), ref, &next, original, replacement, len(original.versions), original.ends); err != nil {
		t.Fatal(err)
	}
	return stage, ref, old
}

func TestAlterDropColumnStageRejectsConcurrentInsert(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(a INT,b TEXT)")
	run(t, e, "INSERT INTO t VALUES(1,'one')")
	stage, ref, old := alterDropTestStage(t, e)
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	run(t, e, "INSERT INTO t VALUES(2,'two')")
	change, err := e.PublishCatalog(prepared, PublicationToken{Epoch: ref.Epoch})
	var conflict CatalogConflict
	if !errors.As(err, &conflict) || len(change.Definitions) != 0 || e.state.descriptors[ref.Address] != old {
		t.Fatal(change, err)
	}
	if got := tuples(run(t, e, "SELECT * FROM t ORDER BY a")); !reflect.DeepEqual(got, [][]driver.Value{{int64(1), "one"}, {int64(2), "two"}}) {
		t.Fatal(got)
	}
}

func TestAlterDropColumnFailuresAndSkip(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE TABLE t(a INT,b TEXT)")
	roleExecute(t, e, op, "INSERT INTO t VALUES(1,'keep')")
	ref := roleTableRef(t, e, "t")
	old := e.state.descriptors[ref.Address]
	root := e.storage[ref.Address]
	for _, tc := range []struct{ q, code, message string }{{"ALTER TABLE t DROP missing", "42703", `column "missing" of relation "t" does not exist`}, {"ALTER TABLE missing DROP b", "42P01", `relation "missing" does not exist`}, {"ALTER TABLE t DROP COLUMN ctid", "0A000", `cannot drop system column "ctid"`}, {"ALTER TABLE t DROP IF EXISTS xmin", "0A000", `cannot drop system column "xmin"`}} {
		_, err := e.Execute(op, statement(t, tc.q), 0, nil)
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message}
		if !errors.As(err, &got) || *got != want {
			t.Fatal(tc.q, got, err)
		}
		if e.state.descriptors[ref.Address] != old || e.storage[ref.Address] != root {
			t.Fatal("failure changed state")
		}
	}
	roleExecute(t, e, op, "ALTER TABLE t DROP COLUMN IF EXISTS missing")
	want := []sqlstate.Error{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `column "missing" of relation "t" does not exist, skipping`}}
	if got := op.DetachNotices(); !reflect.DeepEqual(got, want) {
		t.Fatal(got)
	}
	if e.state.descriptors[ref.Address] != old || e.storage[ref.Address] != root {
		t.Fatal("skip changed state")
	}
	var before bytes.Buffer
	if err := e.Dump(context.Background(), &before); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := testExecute(e, ctx, statement(t, "ALTER TABLE t DROP b"), nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	var after bytes.Buffer
	if err := e.Dump(context.Background(), &after); err != nil || before.String() != after.String() {
		t.Fatal(err, after.String())
	}
}

func TestAlterDropColumnStageCancellationAndReplacement(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		e := New()
		run(t, e, "CREATE TABLE t(a INT,b TEXT)")
		run(t, e, "INSERT INTO t VALUES(1,'keep')")
		stage, ref, old := alterDropTestStage(t, e)
		ctx, cancel := context.WithCancel(context.Background())
		prepared, err := e.ValidateCatalogStage(ctx, stage)
		if err != nil {
			t.Fatal(err)
		}
		if replacement {
			if err = e.Reset(context.Background()); err != nil {
				t.Fatal(err)
			}
		} else {
			cancel()
		}
		changes, err := e.PublishCatalog(prepared, PublicationToken{Epoch: ref.Epoch})
		cancel()
		if len(changes.Definitions) != 0 || err == nil {
			t.Fatal(changes, err)
		}
		if !replacement && (e.state.descriptors[ref.Address] != old || !errors.Is(err, context.Canceled)) {
			t.Fatal(err)
		}
		if replacement && !errors.Is(err, ErrCatalogReplaced) {
			t.Fatal(err)
		}
	}
}

func TestAlterDropColumnACLCleanupAndSurvivorAuthority(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE USER dropped_grantee")
	roleExecute(t, e, op, "CREATE USER survivor_grantee")
	roleExecute(t, e, op, "CREATE TABLE t(a INT,b TEXT,c BIGINT)")
	ref := roleTableRef(t, e, "t")
	owner := op.RoleState().CurrentUser
	for sub, name := range map[int32]string{2: "dropped_grantee", 3: "survivor_grantee"} {
		role, _ := e.Roles().Lookup(name)
		column := ref
		column.Address.SubID = sub
		acl := ACLValue{Explicit: true, Entries: []ACLEntry{{Grantor: owner, Grantee: ACLGrantee{Role: role.Ref}, Privileges: SelectPrivilege}}}
		if err := e.SetObjectACL(context.Background(), column, acl); err != nil {
			t.Fatal(err)
		}
	}
	old := e.state.descriptors[ref.Address].(*relationDescriptor)
	roleExecute(t, e, op, "ALTER TABLE t DROP b CASCADE")
	next := e.state.descriptors[ref.Address].(*relationDescriptor)
	if len(next.columnACL) != 1 || !reflect.DeepEqual(next.columnACL[3], old.columnACL[3]) || len(old.columnACL) != 2 {
		t.Fatal(old.columnACL, next.columnACL)
	}
	roleExecute(t, e, op, "DROP ROLE dropped_grantee")
	_, err := e.Execute(op, statement(t, "DROP ROLE survivor_grantee"), 0, nil)
	var dependency *sqlstate.Error
	wantDependency := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: `role "survivor_grantee" cannot be dropped because some objects depend on it`, Detail: "privileges for column c of table t"}
	if !errors.As(err, &dependency) || *dependency != wantDependency {
		t.Fatal(err, dependency)
	}
	dead, live := ref, ref
	dead.Address.SubID = 2
	live.Address.SubID = 3
	if e.SetObjectACL(context.Background(), dead, ACLValue{Explicit: true}) == nil {
		t.Fatal("dead ACL accepted")
	}
	if err = e.SetObjectACL(context.Background(), live, next.columnACL[3]); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION survivor_grantee")
	roleExecute(t, e, op, "SELECT c FROM t")
	_, err = e.Execute(op, statement(t, "ALTER TABLE t DROP IF EXISTS missing"), 0, nil)
	state(t, err, "42501")
}

type alterDropProvider struct {
	CatalogProvider
	callback func()
}

func (p alterDropProvider) Validate(v CatalogRead, d Descriptor) error {
	if err := p.CatalogProvider.Validate(v, d); err != nil {
		return err
	}
	p.callback()
	return nil
}

func TestAlterDropColumnProviderCancellationAndReset(t *testing.T) {
	for _, reset := range []bool{false, true} {
		e := New()
		run(t, e, "CREATE TABLE t(a INT,b TEXT)")
		run(t, e, "INSERT INTO t VALUES(1,'keep')")
		ref := roleTableRef(t, e, "t")
		old := e.state.descriptors[ref.Address]
		original := e.storage[ref.Address]
		ctx, cancel := context.WithCancel(context.Background())
		e.providers[RelationClassID] = alterDropProvider{e.providers[RelationClassID], func() {
			if reset {
				if err := e.Reset(context.Background()); err != nil {
					t.Fatal(err)
				}
			} else {
				cancel()
			}
		}}
		_, err := testExecute(e, ctx, statement(t, "ALTER TABLE t DROP b"), nil)
		cancel()
		if reset {
			if !errors.Is(err, ErrCatalogReplaced) {
				t.Fatal(err)
			}
		} else {
			if !errors.Is(err, context.Canceled) || e.state.descriptors[ref.Address] != old || e.storage[ref.Address] != original {
				t.Fatal(err)
			}
		}
	}
}

func TestAlterDropColumnTransformCancellationAndRevisionExhaustion(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(a INT,b TEXT)")
	run(t, e, "INSERT INTO t VALUES(1,'keep'),(2,'keep'),(3,'keep')")
	ref := roleTableRef(t, e, "t")
	old := e.state.descriptors[ref.Address]
	original := e.storage[ref.Address]
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Stage transformation checkpoints use the real cancelable parent context.
	op, cleanup, err := testOperation(e, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	counting := &cancelAtErrCheck{Context: op.Context(), cancel: cancel, at: 12}
	op.ctx = counting
	_, err = e.Execute(op, statement(t, "ALTER TABLE t DROP b"), 0, nil)
	if !errors.Is(err, context.Canceled) || e.state.descriptors[ref.Address] != old || e.storage[ref.Address] != original {
		t.Fatal(err)
	}
	maximum := *old.(*relationDescriptor)
	maximum.revision = DefinitionRevision(^uint64(0))
	stage := e.BeginCatalogStage(e.CatalogRead())
	if err = stage.Replace(&maximum); err != nil {
		t.Fatal(err)
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: ref.Epoch}); err != nil {
		t.Fatal(err)
	}
	_, err = testExecute(e, context.Background(), statement(t, "ALTER TABLE t DROP b"), nil)
	if err == nil || err.Error() != "stuntgres: definition revision exhausted" || e.state.descriptors[ref.Address] != &maximum || e.storage[ref.Address] != original {
		t.Fatal(err)
	}
}

func TestAlterDropColumnSchemaOwnerIsNotTableOwner(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE USER schema_owner")
	roleExecute(t, e, op, "CREATE SCHEMA s AUTHORIZATION schema_owner")
	roleExecute(t, e, op, "CREATE TABLE s.t(a INT,b TEXT)")
	ref := roleTableRef(t, e, "s.t")
	old := e.state.descriptors[ref.Address]
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION schema_owner")
	for _, q := range []string{"ALTER TABLE s.t DROP b", "ALTER TABLE s.t DROP IF EXISTS missing"} {
		_, err := e.Execute(op, statement(t, q), 0, nil)
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "must be owner of table t"}
		if !errors.As(err, &got) || *got != want || e.state.descriptors[ref.Address] != old {
			t.Fatal(got, err)
		}
	}
}

func TestAlterDropColumnStageProviderFailureIsAtomic(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(a INT,b TEXT)")
	run(t, e, "INSERT INTO t VALUES(1,'keep')")
	stage, ref, old := alterDropTestStage(t, e)
	original := e.storage[ref.Address]
	// Exercise the real provider's validation, not a fabricated callback error.
	stage.descriptors[ref.Address].(*relationDescriptor).index["a"] = 9
	_, err := e.ValidateCatalogStage(context.Background(), stage)
	if err == nil || err.Error() != "stuntgres: invalid relation column identity" || e.state.descriptors[ref.Address] != old || e.storage[ref.Address] != original {
		t.Fatal(err)
	}
	if got := tuples(run(t, e, "SELECT * FROM t")); !reflect.DeepEqual(got, [][]driver.Value{{int64(1), "keep"}}) {
		t.Fatal(got)
	}
}

func TestAlterDropColumnStageSingleChangeSet(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(a INT,b TEXT)")
	run(t, e, "INSERT INTO t VALUES(1,'gone')")
	stage, ref, old := alterDropTestStage(t, e)
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := e.PublishCatalog(prepared, PublicationToken{Epoch: ref.Epoch})
	want := ChangeSet{Definitions: []ObjectAddress{ref.Address}}
	if err != nil || !reflect.DeepEqual(changes, want) || e.state.descriptors[ref.Address] == old {
		t.Fatal(changes, err)
	}
	if got := tuples(run(t, e, "SELECT * FROM t")); !reflect.DeepEqual(got, [][]driver.Value{{int64(1)}}) {
		t.Fatal(got)
	}
}
