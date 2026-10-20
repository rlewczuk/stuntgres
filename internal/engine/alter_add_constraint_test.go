package engine

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func alterConstraintOperation(t *testing.T, s *Session) *Operation {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(op.Done)
	return op
}
func alterConstraintExecuteScope(t *testing.T, e *Engine, s *Session, q string) (*StatementScope, error) {
	t.Helper()
	op := alterConstraintOperation(t, s)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !scope.finished {
			_ = scope.Finish(context.Canceled)
		}
	})
	raw, err := parser.Parse(q)
	if err == nil {
		_, err = scope.Execute(raw[0], 0, nil)
	}
	return scope, err
}
func alterConstraintDescriptor(t *testing.T, e *Engine, name string) *catalog.Relation {
	t.Helper()
	view := e.CatalogRead()
	return view.MustRelation(catalogRef(t, e, view, name).Address)
}

func TestAlterAddConstraintStableKeys(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(a INT UNIQUE,z INT,b INT)")
	sessionSQL(t, s, "ALTER TABLE t DROP z")
	original := alterConstraintDescriptor(t, e, "t")
	before, store := e.catalog.state, e.storage.Relations[original.Address()]
	op := alterConstraintOperation(t, s)
	auth, err := op.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	defer e.DiscardCatalogStage(stage)
	raw, err := parser.Parse("ALTER TABLE t ADD CONSTRAINT added UNIQUE(b,a)")
	if err != nil {
		t.Fatal(err)
	}
	next, err := e.stageAddedUnique(op, auth, stage, original, raw[0].(*parser.AlterTable).Actions[0].Unique)
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Constraints.Unique) != 2 || !slices.Equal(next.Constraints.Unique[1].Attributes, []int32{3, 1}) || next.Constraints.Unique[0].OID != original.Constraints.Unique[0].OID {
		t.Fatal("lost stable keys or incumbent group", next.Constraints.Unique)
	}
	added := next.Constraints.Unique[1]
	index := stage.Get(added.Index).(*catalog.Relation)
	if index.SQLIndex.Parent != next.Address() || index.SQLIndex.Constraint == nil || index.SQLIndex.Constraint.OID != added.OID {
		t.Fatal("missing reciprocal real index", added, index)
	}
	if _, err := e.ValidateCatalogStage(context.Background(), stage); err != nil {
		t.Fatal(err)
	}
	if e.catalog.state != before || e.storage.Relations[original.Address()] != store || len(original.Constraints.Unique) != 1 {
		t.Fatal("candidate changed borrowed catalog/storage")
	}
}

func TestAlterAddConstraintValidationSelection(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(a INT,b INT)")
	sessionSQL(t, s, "INSERT INTO t VALUES(NULL,1),(-1,1)")
	sessionSQL(t, s, "ALTER TABLE t ADD CONSTRAINT nn NOT NULL a NOT VALID,ADD CONSTRAINT ck CHECK(a>0) NOT VALID")
	d := alterConstraintDescriptor(t, e, "t")
	if d.Columns[0].Nullable || !d.Constraints.NotNull[0].NotValid || !d.Constraints.Check[0].NotValid {
		t.Fatal("invalid means unenforced or flags lost", d)
	}
	v := newAlterRowValidator(d, "public")
	v.checks = validCheckTraversal(d)
	if len(v.notNull) != 0 || len(v.checks.Constraints.Check) != 0 {
		t.Fatal("historical selection contains invalid jobs", v)
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	for _, row := range e.storage.Relations[d.Address()].Versions {
		if err := v.validate(row.Values, &evaluation); err != nil {
			t.Fatal(err)
		}
	}
	row := e.storage.Relations[d.Address()].Versions[1]
	failed, err := validateCheckRow(d, row.Values, &evaluation)
	if err != nil || failed == nil || failed.Name != "ck" {
		t.Fatal("ordinary DML lost invalid CHECK enforcement", failed, err)
	}
	oldNN, oldCK := d.Constraints.NotNull[0].OID, d.Constraints.Check[0].OID
	sessionSQL(t, s, "ALTER TABLE t ALTER b TYPE BIGINT")
	unrelated := alterConstraintDescriptor(t, e, "t")
	if unrelated.Constraints.NotNull[0].OID != oldNN || unrelated.Constraints.Check[0].OID != oldCK {
		t.Fatal("unrelated TYPE recreated identity")
	}
	sessionSQL(t, s, "ALTER TABLE t ALTER a TYPE BIGINT")
	related := alterConstraintDescriptor(t, e, "t")
	if related.Constraints.NotNull[0].OID == oldNN || related.Constraints.Check[0].OID == oldCK || !related.Constraints.NotNull[0].NotValid || !related.Constraints.Check[0].NotValid {
		t.Fatal("affected rebuild lost identity/validity", related)
	}
	if d.Constraints.NotNull[0].OID != oldNN || d.Constraints.Check[0].OID != oldCK {
		t.Fatal("retained descriptor mutated")
	}
}

func TestAlterAddConstraintForeignKeyAdmission(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(a INT)")
	sessionSQL(t, s, "INSERT INTO t VALUES(1)")
	original := alterConstraintDescriptor(t, e, "t")
	scope, err := alterConstraintExecuteScope(t, e, s, "ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES t(a),ADD CONSTRAINT key UNIQUE(a)")
	if err != nil {
		_ = scope.Finish(err)
		t.Fatal(err)
	}
	if len(scope.foreignActionsOrZero().alterKeys) != 1 || scope.foreignActionsOrZero().alterKeys[0].target.Address != original.Address() || scope.foreignActionsOrZero().alterKeys[0].index != (ids.ObjectAddress{}) {
		t.Fatal("invented preexisting self provider", scope.foreignActionsOrZero().alterKeys)
	}
	candidate := scope.catalog.Descriptors[original.Address()].(*catalog.Relation)
	if len(candidate.Constraints.ForeignKeys) != 1 || candidate.Constraints.ForeignKeys[0].Index != candidate.Constraints.Unique[0].Index {
		t.Fatal("FK did not bind completed real key", candidate)
	}
	foreignKeyHeldMode(t, e, e.catalog.Domain.Ref(e.catalog.state.Epoch(), original.Address()), 0, scope.ticket, lock.AccessExclusive)
	if len(original.Constraints.ForeignKeys) != 0 || len(original.Constraints.Unique) != 0 {
		t.Fatal("published before Finish")
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	scope.op.Done()
	lockEmpty(t, e)
}

func TestAlterAddConstraintForeignKeySuccessors(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE p(id INT PRIMARY KEY)")
	sessionSQL(t, s, "INSERT INTO p VALUES(1)")
	sessionSQL(t, s, "CREATE TABLE t(a BIGINT,b INT)")
	sessionSQL(t, s, "INSERT INTO t VALUES(9223372036854775807,1)")
	sessionSQL(t, s, "ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p NOT VALID")
	child, parent := alterConstraintDescriptor(t, e, "t"), alterConstraintDescriptor(t, e, "p")
	f := child.Constraints.ForeignKeys[0]
	index := e.catalog.state.MustRelation(f.Index)
	comparisons, err := bindForeignKeyComparison(e.CatalogRead(), child, parent, index, f)
	if err != nil {
		t.Fatal(err)
	}
	value := e.storage.Relations[child.Address()].Versions[0].Values[0]
	equal, err := comparisons[0].comparison.EqualChildren(context.Background(), value, value, sqltype.InputEnvironment{})
	if err != nil || !equal {
		t.Fatal("narrowed committed unchanged child key", equal, err)
	}
	tx := transactionBegin(t, s)
	sessionSQL(t, s, "UPDATE t SET b=2")
	scope, err := alterConstraintExecuteScope(t, e, s, "UPDATE t SET b=3")
	if err != nil {
		_ = scope.Finish(err)
		t.Fatal(err)
	}
	if scope.projectedRoots {
		t.Fatal("ordinary UPDATE acquired historical provenance")
	}
	current := scope.stage.rows[child.Address()][0]
	rows, err := scope.captureForeignKeyRows(e.catalog.Domain.Ref(e.catalog.state.Epoch(), child.Address()), ids.ObjectAddress{})
	if err != nil {
		t.Fatal(err)
	}
	previous, ok := rows.previous(current.Row)
	if !ok || current.Base == 0 || previous.Born != 0 {
		t.Fatal("real prior-own predecessor missing", current, previous)
	}
	err = scope.Finish(nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "23503" {
		t.Fatal("prior own key skipped", err)
	}
	scope.op.Done()
	transactionEnd(t, s, tx, false)
}

func TestAlterAddConstraintProjectionProvenance(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE p(id INT PRIMARY KEY)", "INSERT INTO p VALUES(1)", "CREATE TABLE t(a INT,b INT)", "CREATE TABLE u(a INT,b INT)"} {
		sessionSQL(t, s, q)
	}
	tx := transactionBegin(t, s)
	for _, q := range []string{"INSERT INTO t VALUES(9,1)", "INSERT INTO u VALUES(9,1)", "ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p NOT VALID", "ALTER TABLE u ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p NOT VALID"} {
		sessionSQL(t, s, q)
	}
	tDesc, uDesc := alterConstraintDescriptor(t, e, "t"), alterConstraintDescriptor(t, e, "u")
	// Explicit metadata is private; relation addresses remain the committed refs.
	scope, err := alterConstraintExecuteScope(t, e, s, "ALTER TABLE t ALTER b TYPE BIGINT")
	if err != nil {
		_ = scope.Finish(err)
		t.Fatal(err)
	}
	if !scope.projectedRoots || !scope.stage.replacing || len(scope.stage.rows[tDesc.Address()]) != 1 || len(scope.stage.rows[uDesc.Address()]) != 1 {
		t.Fatal("projection not complete historical overlay", scope.stage.rows)
	}
	if !reflect.DeepEqual(scope.stage.rows[uDesc.Address()], scope.ownWrites[uDesc.Address()]) {
		t.Fatal("unrelated historical own rows changed")
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	scope.op.Done()
	write, err := alterConstraintExecuteScope(t, e, s, "UPDATE u SET b=2")
	if err != nil {
		_ = write.Finish(err)
		t.Fatal(err)
	}
	if write.projectedRoots {
		t.Fatal("fresh DML got projection exemption")
	}
	err = write.Finish(nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "23503" {
		t.Fatal("following born-current DML lost enforcement", err)
	}
	write.op.Done()
	transactionEnd(t, s, tx, false)
}

func TestAlterAddConstraintAtomicity(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(a INT UNIQUE,b INT)")
	sessionSQL(t, s, "INSERT INTO t VALUES(1,1),(2,1)")
	original := alterConstraintDescriptor(t, e, "t")
	before, store := e.catalog.state, e.storage.Relations[original.Address()]
	root := e.storage.IndexRoots[original.Constraints.Unique[0].Index]
	op := alterConstraintOperation(t, s)
	auth, err := op.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	defer e.DiscardCatalogStage(stage)
	raw, err := parser.Parse("ALTER TABLE t ADD CONSTRAINT bad UNIQUE(b)")
	if err != nil {
		t.Fatal(err)
	}
	next, err := e.stageAddedUnique(op, auth, stage, original, raw[0].(*parser.AlterTable).Actions[0].Unique)
	if err != nil {
		t.Fatal(err)
	}
	_, err = validateAlterIndexBuilds(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: e.stageRead(stage)}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, auth, e.stageRead(stage), next, store, nil, nil, "public", sqltype.OutputEnvironment{}, nil, nil, []ids.ObjectAddress{next.Constraints.Unique[1].Index}, false)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "23505" {
		t.Fatal("no real private root validation", err)
	}
	if e.catalog.state != before || e.storage.Relations[original.Address()] != store || e.storage.IndexRoots[original.Constraints.Unique[0].Index] != root || len(original.Constraints.Unique) != 1 {
		t.Fatal("failed candidate changed catalog/roots")
	}
}

func TestAlterAddConstraintLocks(t *testing.T) {
	e := New()
	a, b, c := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, c, "CREATE TABLE p(id INT PRIMARY KEY)")
	sessionSQL(t, c, "CREATE TABLE t(a INT)")
	ref := catalogRef(t, e, e.CatalogRead(), "t")
	before := e.catalog.state
	tx := transactionBegin(t, a)
	sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := lockAsync(e, b, ctx, "ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p NOT VALID", false)
	joined := false
	defer func() {
		cancel()
		if !joined {
			lockAwait(t, done, context.Canceled)
		}
	}()
	lockPending(t, e, 1)
	lockState := e.locks.Inspect()
	entry := lockState.Resources[lock.Resource{ObjectRef: ref}]
	queued := false
	if entry != nil {
		for _, request := range entry.Pending {
			queued = queued || !request.Terminal && request.Mode == lock.ShareRowExclusive && request.Ticket != a.transactions.ticket
		}
	}
	if !queued {
		t.Fatal("ADD FK did not queue its exact child table SRE request")
	}
	if e.catalog.state != before {
		t.Fatal("queued candidate published")
	}
	cancel()
	lockAwait(t, done, context.Canceled)
	joined = true
	transactionEnd(t, a, tx, false)
	lockEmpty(t, e)
	tx = transactionBegin(t, b)
	sessionSQL(t, b, "ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p NOT VALID")
	foreignKeyHeldMode(t, e, ref, 0, b.transactions.ticket, lock.ShareRowExclusive)
	foreignKeyHeldMode(t, e, catalogRef(t, e, e.CatalogRead(), "p"), 0, b.transactions.ticket, lock.ShareRowExclusive)
	transactionEnd(t, b, tx, false)
	lockEmpty(t, e)
}

func TestAlterAddConstraintControls(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE p(id INT PRIMARY KEY)", "INSERT INTO p VALUES(1)", "CREATE TABLE t(a INT,b INT,pid INT)", "INSERT INTO t VALUES(0,NULL,9)", "ALTER TABLE t ADD CONSTRAINT ck CHECK(a>0) NO INHERIT NOT VALID,ADD CONSTRAINT nn NOT NULL b NOT VALID,ADD CONSTRAINT fk FOREIGN KEY(pid) REFERENCES p NOT VALID"} {
		sessionSQL(t, s, q)
	}
	var dump bytes.Buffer
	if err := e.Dump(context.Background(), &dump); err != nil {
		t.Fatal(err)
	}
	text := dump.String()
	rowAt := strings.Index(text, `INSERT INTO "public"."t"`)
	for _, name := range []string{"ck", "nn", "fk"} {
		at := strings.Index(text, `ALTER TABLE "public"."t" ADD CONSTRAINT "`+name+`"`)
		if at <= rowAt {
			t.Fatal("invalid constraint precedes actual ordinary row", name, text)
		}
	}
	sourceState := e.catalog.state
	source := alterConstraintDescriptor(t, e, "t")
	sourceStore := e.storage.Relations[source.Address()]
	sourceNN, sourceCK, sourceFK := source.Constraints.NotNull[0].OID, source.Constraints.Check[0].OID, source.Constraints.ForeignKeys[0].OID
	op := alterConstraintOperation(t, s)
	auth, err := op.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	malformed := e.BeginCatalogStage(auth.Catalog)
	invalid := *source
	invalid.Constraints.ForeignKeys = slices.Clone(source.Constraints.ForeignKeys)
	invalid.Constraints.ForeignKeys[0].Index = source.Address()
	if err := malformed.Replace(&invalid); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ValidateCatalogStage(context.Background(), malformed); err == nil {
		t.Fatal("NOT VALID bypassed actual supporting-index manifest validation")
	}
	e.DiscardCatalogStage(malformed)
	op.Done()
	if source.Constraints.ForeignKeys[0].Index == source.Address() || e.catalog.state != sourceState || e.storage.Relations[source.Address()] != sourceStore {
		t.Fatal("malformed stage changed borrowed source")
	}
	target := New()
	owner := sessionStart(t, target, nil)
	sessionSQL(t, owner, "CREATE TABLE sentinel(a INT)")
	sessionSQL(t, owner, "INSERT INTO sentinel VALUES(91)")
	sentinel := alterConstraintDescriptor(t, target, "sentinel")
	before, store, epoch := target.catalog.state, target.storage.Relations[sentinel.Address()], target.catalog.state.Epoch()
	if err := target.Restore(context.Background(), strings.NewReader(text+"ALTER TABLE public.t ADD CONSTRAINT late FOREIGN KEY(pid) REFERENCES public.absent NOT VALID;")); err == nil {
		t.Fatal("late missing provider silently accepted")
	}
	if target.catalog.state != before || target.storage.Relations[sentinel.Address()] != store || target.catalog.state.Epoch() != epoch {
		t.Fatal("failed import changed sentinel/incarnation")
	}
	if err := target.Restore(context.Background(), strings.NewReader(text)); err != nil {
		t.Fatal(err)
	}
	restored := alterConstraintDescriptor(t, target, "t")
	if !restored.Constraints.NotNull[0].NotValid || !restored.Constraints.Check[0].NotValid || !restored.Constraints.ForeignKeys[0].NotValid || restored.Constraints.ForeignKeys[0].Target != alterConstraintDescriptor(t, target, "p").Address() {
		t.Fatal("remap lost validity/relationships", restored)
	}
	var again bytes.Buffer
	if err := target.Dump(context.Background(), &again); err != nil {
		t.Fatal(err)
	}
	if again.String() != text {
		t.Fatal("noncanonical invalid replay", text, again.String())
	}
	if e.catalog.state != sourceState || e.storage.Relations[source.Address()] != sourceStore || alterConstraintDescriptor(t, e, "t") != source || source.Constraints.NotNull[0].OID != sourceNN || source.Constraints.Check[0].OID != sourceCK || source.Constraints.ForeignKeys[0].OID != sourceFK {
		t.Fatal("target import mutated borrowed source state/storage/identities")
	}
}
