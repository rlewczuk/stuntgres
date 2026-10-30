package core_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func foreignActionUnitScope(t *testing.T, e *core.Engine, session *session.Session, q string) *core.StatementScope {
	t.Helper()
	op, err := session.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	t.Cleanup(func() { scope.Finish(context.Canceled); op.Done() })
	if _, err = scope.Execute(enginetest.Statement(t, q), 0, nil); err != nil {
		t.Fatal(err)
	}
	return scope
}

func foreignActionUnitJob(t *testing.T, jobs []dml.ForeignKeyJob, name string) *dml.ForeignKeyJob {
	t.Helper()
	for i := range jobs {
		if jobs[i].Child.Name() == name {
			return &jobs[i]
		}
	}
	t.Fatal("missing captured job", name)
	return nil
}

func TestForeignActionsDefinition(t *testing.T) {
	e := engine.New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE p(a INT,b INT,PRIMARY KEY(a,b))", "CREATE TABLE c(id INT,a INT,dead INT,b INT,CONSTRAINT fk FOREIGN KEY(a,b) REFERENCES p ON UPDATE CASCADE ON DELETE SET NULL(b,b))"} {
		sessionSQL(t, s, q)
	}
	view := e.CatalogRead()
	ref := enginetest.CatalogRef(t, e, view, "c")
	child := view.MustRelation(ref.Address)
	original := child.Constraints.ForeignKeys[0]
	if original.UpdateAction != parser.ForeignKeyCascade || original.DeleteAction != parser.ForeignKeySetNull || len(original.DeleteAttributes) != 1 || original.DeleteAttributes[0] != child.Attributes[3] {
		t.Fatal(original)
	}
	for _, change := range []func(*catalog.ForeignKeyConstraint){
		func(f *catalog.ForeignKeyConstraint) { f.UpdateAction = 255 }, func(f *catalog.ForeignKeyConstraint) { f.DeleteAction = 255 }, func(f *catalog.ForeignKeyConstraint) { f.DeleteAction = parser.ForeignKeyCascade }, func(f *catalog.ForeignKeyConstraint) { f.DeleteAttributes = []int32{child.Attributes[2]} }, func(f *catalog.ForeignKeyConstraint) { f.DeleteAttributes = []int32{99} }, func(f *catalog.ForeignKeyConstraint) {
			f.DeleteAttributes = []int32{child.Attributes[3], child.Attributes[3]}
		},
	} {
		candidate := *child
		candidate.Constraints.ForeignKeys = slices.Clone(child.Constraints.ForeignKeys)
		change(&candidate.Constraints.ForeignKeys[0])
		if err := table.ValidateForeignKeys(view, &candidate, map[string]bool{}); err == nil {
			t.Fatal("invalid action attachment admitted", candidate.Constraints.ForeignKeys[0])
		}
	}
	if !reflect.DeepEqual(original, child.Constraints.ForeignKeys[0]) {
		t.Fatal("validation changed retained definition")
	}
	sessionSQL(t, s, "ALTER TABLE c DROP COLUMN dead")
	sessionSQL(t, s, "ALTER TABLE c RENAME b TO author")
	next := e.CatalogRead()
	current := next.MustRelation(ref.Address)
	if !slices.Equal(current.Constraints.ForeignKeys[0].DeleteAttributes, original.DeleteAttributes) || !dml.SameForeignKeyDefinition(original, current.Constraints.ForeignKeys[0]) {
		t.Fatal("slot/name rewrite retargeted action", original, current.Constraints.ForeignKeys[0])
	}
	text, err := (stored.Deparser{View: next, Relation: current, Path: []ids.ObjectAddress{current.Namespace()}}).ForeignKeyConstraintSQL(current, current.Constraints.ForeignKeys[0])
	if err != nil || text != "FOREIGN KEY (a, author) REFERENCES p(a, b) ON UPDATE CASCADE ON DELETE SET NULL (author)" {
		t.Fatal(text, err)
	}
	oldText, err := (stored.Deparser{View: view, Relation: child, Path: []ids.ObjectAddress{child.Namespace()}}).ForeignKeyConstraintSQL(child, original)
	if err != nil || oldText != "FOREIGN KEY (a, b) REFERENCES p(a, b) ON UPDATE CASCADE ON DELETE SET NULL (b)" {
		t.Fatal("retained deparse changed", oldText, err)
	}
}

func TestForeignActionsEquality(t *testing.T) {
	for _, tc := range []struct{ typ, old, next string }{{"NUMERIC", "1.0", "1.00"}, {"DOUBLE PRECISION", "0", "-0"}, {"INTERVAL", "1 day", "24 hours"}} {
		t.Run(tc.typ, func(t *testing.T) {
			e := engine.New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE p(id "+tc.typ+" PRIMARY KEY)")
			sessionSQL(t, s, "CREATE TABLE c(pid "+tc.typ+" REFERENCES p ON UPDATE CASCADE)")
			view := e.CatalogRead()
			child := view.MustRelation(enginetest.CatalogRef(t, e, view, "c").Address)
			f := child.Constraints.ForeignKeys[0]
			parent := view.MustRelation(f.Target)
			index := view.MustRelation(f.Index)
			comparisons, err := table.BindForeignKeyComparison(view, child, parent, index, f)
			if err != nil {
				t.Fatal(err)
			}
			old, err := parent.Columns[0].Usage.Type.Input(context.Background(), tc.old, -1)
			if err != nil {
				t.Fatal(err)
			}
			next, err := parent.Columns[0].Usage.Type.Input(context.Background(), tc.next, -1)
			if err != nil {
				t.Fatal(err)
			}
			if dml.ForeignActionSameKey(comparisons, []sqltype.Value{old}, []sqltype.Value{next}) {
				t.Fatal("SQL equality suppressed representation transition")
			}
			equal, err := table.ForeignKeyRowsMatch(context.Background(), comparisons, []sqltype.Value{old}, []sqltype.Value{next}, sqltype.InputEnvironment{})
			if err != nil || !equal {
				t.Fatal("matching lost selected supporting equality", equal, err)
			}
			if !dml.ForeignActionSameKey(comparisons, []sqltype.Value{next}, []sqltype.Value{next}) {
				t.Fatal("identical datum created an event")
			}
		})
	}
}

func TestForeignActionsAdmission(t *testing.T) {
	e := engine.New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE p(id INT PRIMARY KEY)", "CREATE TABLE c(pid INT REFERENCES p ON UPDATE CASCADE)", "CREATE TABLE unrelated(id INT)", "INSERT INTO p VALUES(1)", "INSERT INTO c VALUES(1)"} {
		sessionSQL(t, s, q)
	}
	scope := foreignActionUnitScope(t, e, s, "UPDATE p SET id=2")
	named, stored, descriptor := scope.LockedRef, scope.LockedStorage, scope.LockedDescriptor
	jobs, auth, err := dml.PrepareForeignActions(scope.WriteHost())
	if err != nil {
		t.Fatal(err)
	}
	job := foreignActionUnitJob(t, jobs, "c")
	ref := e.Catalog.Domain.Ref(auth.Access().Catalog.Epoch(), job.Child.Address())
	children, err := scope.CaptureForeignKeyRows(ref, ids.ObjectAddress{})
	if err != nil {
		t.Fatal(err)
	}
	old := children.Committed[0]
	value, _ := sqltype.Integer(job.Child.Columns[0].Usage.Type, 2)
	replacement := [1][]sqltype.Value{{value}}
	predecessor := [1]storage.RowVersion{old}
	before := len(core.ScopeForeignActionsOrZero(scope).Transitions)
	if err = dml.ReplaceRows(scope.ActivateRuntime(scope.Op.Context()), scope.WriteHost(), ref, predecessor[:], index.WriteBatch{Values: replacement[:]}, dml.ReplaceMerge); err == nil {
		t.Fatal("FK address alone authorized a child replacement")
	}
	if len(core.ScopeForeignActionsOrZero(scope).Transitions) != before {
		t.Fatal("rejected child replacement recorded an accepted transition")
	}
	if err = core.ScopeAdmitForeignAction(scope, ref, job, false); err != nil {
		t.Fatal(err)
	}
	if err = scope.ForeignKeyGrant(ref, old.Row, lock.ForUpdate, children.Storage); err != nil {
		t.Fatal(err)
	}
	if err = core.WriteStageDeleteForeignAction(&scope.Stage, ref, old); err == nil {
		t.Fatal("read-only action authority admitted deletion")
	}
	if err = core.ScopeAdmitForeignAction(scope, ref, job, true); err != nil {
		t.Fatal(err)
	}
	scope.WriteHost().SwapForeignActionAuthority(auth.ActingAs(job.Child.Owner))
	core.ScopeForeignActions(scope).Settling = true
	if err = dml.ReplaceRows(scope.ActivateRuntime(scope.Op.Context()), scope.WriteHost(), ref, predecessor[:], index.WriteBatch{Values: replacement[:]}, dml.ReplaceMerge); err != nil {
		t.Fatal(err)
	}
	scope.ForeignActionAuth = nil
	core.ScopeForeignActions(scope).Settling = false
	next := scope.Stage.Rows[ref.Address][0]
	var childEvents []dml.ForeignActionTransition
	for _, event := range core.ScopeForeignActionsOrZero(scope).Transitions {
		if event.Relation == ref.Address {
			childEvents = append(childEvents, event)
		}
	}
	if next.Row != old.Row || next.Version == old.Version || next.Base != old.Version || len(childEvents) != 1 || childEvents[0].Old.Version != old.Version || childEvents[0].Next.Version != next.Version || childEvents[0].Owner != job.Child.Owner {
		t.Fatal("physical child lineage was not recorded", old, next, childEvents)
	}
	if scope.LockedRef != named || scope.LockedStorage != stored || scope.LockedDescriptor != descriptor {
		t.Fatal("child action spoofed initiating admission")
	}
	admission := core.ScopeForeignActionsOrZero(scope).Admissions[ref.Address]
	if !admission.Mutated || admission.Tx != scope.TxRef || admission.Storage != children.Storage {
		t.Fatal("missing captured physical child authority", admission)
	}
	foreign := ref
	foreign.Address = enginetest.CatalogRef(t, e, e.CatalogRead(), "unrelated").Address
	e.Mu.Lock()
	err = core.ScopeValidateForeignActionLocked(scope, e, foreign)
	e.Mu.Unlock()
	if err == nil {
		t.Fatal("unrelated child was authorized")
	}
	originalJob := admission.Jobs[0]
	admission.Jobs[0].Definition.DeleteAction = parser.ForeignKeyRestrict
	e.Mu.Lock()
	err = core.ScopeValidateForeignActionLocked(scope, e, ref)
	e.Mu.Unlock()
	admission.Jobs[0] = originalJob
	if !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("changed action definition authority survived", err)
	}
	originalStorage := admission.Storage
	admission.Storage = &storage.Relation{}
	e.Mu.Lock()
	err = core.ScopeValidateForeignActionLocked(scope, e, ref)
	e.Mu.Unlock()
	admission.Storage = originalStorage
	if !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("foreign child storage survived", err)
	}
}

func TestForeignActionsClosure(t *testing.T) {
	e := engine.New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE p(id INT PRIMARY KEY)", "CREATE TABLE a(id INT PRIMARY KEY REFERENCES p ON UPDATE CASCADE)", "CREATE TABLE b(id INT PRIMARY KEY REFERENCES p ON UPDATE CASCADE)", "CREATE TABLE g(id INT,aid INT REFERENCES a ON UPDATE CASCADE,bid INT REFERENCES b ON UPDATE CASCADE)", "INSERT INTO p VALUES(1)", "INSERT INTO a VALUES(1)", "INSERT INTO b VALUES(1)", "INSERT INTO g VALUES(99,1,1)"} {
		sessionSQL(t, s, q)
	}
	scope := foreignActionUnitScope(t, e, s, "UPDATE p SET id=2")
	if err := dml.SettleForeignActions(scope.ActivateRuntime(scope.Op.Context()), scope.WriteHost()); err != nil {
		t.Fatal(err)
	}
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "g")
	rows := scope.Stage.Rows[ref.Address]
	if len(rows) != 1 {
		t.Fatal(rows)
	}
	var events []dml.ForeignActionTransition
	for _, event := range core.ScopeForeignActionsOrZero(scope).Transitions {
		if event.Relation == ref.Address {
			events = append(events, event)
		}
	}
	if len(events) != 2 || events[0].Old.Row != events[1].Old.Row || events[0].Next.Version != events[1].Old.Version || events[0].Next.Version == events[1].Next.Version || rows[0].Version != events[1].Next.Version || events[0].Next.Base != events[1].Next.Base {
		t.Fatal("diamond lost an actual row transition", events, rows)
	}
	expected, _ := sqltype.Integer(rows[0].Values[1].Type(), 2)
	if !sqltype.SameDatum(rows[0].Values[1], expected) || !sqltype.SameDatum(rows[0].Values[2], expected) {
		t.Fatal("second path overwrote first staged payload", rows[0])
	}
	if scope.LockedRef.Address == ref.Address {
		t.Fatal("closure retargeted named admission")
	}
}

func TestForeignActionsDefaults(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "per-row", true: "late-failure"}[fail], func(t *testing.T) {
			e := engine.New()
			s := sessionStart(t, e, nil)
			for _, q := range []string{"CREATE TABLE p(id INT PRIMARY KEY)", "CREATE TABLE d(id INT,pid INT DEFAULT 1 REFERENCES p ON DELETE SET DEFAULT)", "INSERT INTO p VALUES(1),(2),(10)", "INSERT INTO d VALUES(11,10),(12,10)"} {
				sessionSQL(t, s, q)
			}
			probe := &defaultOrdinal{}
			if fail {
				probe.failAt = 2
			}
			defaultProbe(t, e, probe)
			ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "d")
			before := e.Storage.Relations[ref.Address]
			versions := slices.Clone(before.Versions)
			scope := foreignActionUnitScope(t, e, s, "DELETE FROM p WHERE id=10")
			err := dml.SettleForeignActions(scope.ActivateRuntime(scope.Op.Context()), scope.WriteHost())
			if probe.ordinal != 2 {
				t.Fatal("default demand was not per physical row", probe.ordinal)
			}
			if fail {
				var d *sqlstate.Error
				if !errors.As(err, &d) || d.Code != "22003" || d.Context != `SQL statement "UPDATE ONLY "public"."d" SET "pid" = DEFAULT WHERE $1 OPERATOR(pg_catalog.=) "pid""` {
					t.Fatal(err, d)
				}
				if scope.Finish(err) == nil {
					t.Fatal("late action failure succeeded")
				}
			} else {
				rows := scope.Stage.Rows[ref.Address]
				if len(rows) != 2 {
					t.Fatal(rows)
				}
				for i, row := range rows {
					want, _ := sqltype.Integer(row.Values[1].Type(), int64(i+1))
					if !sqltype.SameDatum(row.Values[1], want) || row.Row != versions[i].Row || row.Base != versions[i].Version {
						t.Fatal("default action lineage/value", row, versions[i])
					}
				}
			}
			if e.Storage.Relations[ref.Address] != before || !reflect.DeepEqual(before.Versions, versions) {
				t.Fatal("private action changed committed storage")
			}
		})
	}
}
