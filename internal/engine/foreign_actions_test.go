package engine

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func foreignActionUnitScope(t *testing.T, e *Engine, session *Session, q string) *StatementScope {
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
	if _, err = scope.Execute(statement(t, q), 0, nil); err != nil {
		t.Fatal(err)
	}
	return scope
}

func foreignActionUnitJob(t *testing.T, jobs []foreignKeyJob, name string) *foreignKeyJob {
	t.Helper()
	for i := range jobs {
		if jobs[i].child.Name() == name {
			return &jobs[i]
		}
	}
	t.Fatal("missing captured job", name)
	return nil
}

func TestForeignActionsDefinition(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE p(a INT,b INT,PRIMARY KEY(a,b))", "CREATE TABLE c(id INT,a INT,dead INT,b INT,CONSTRAINT fk FOREIGN KEY(a,b) REFERENCES p ON UPDATE CASCADE ON DELETE SET NULL(b,b))"} {
		sessionSQL(t, s, q)
	}
	view := e.CatalogRead()
	ref := catalogRef(t, e, view, "c")
	child := view.MustRelation(ref.Address)
	original := child.ForeignKeys[0]
	if original.UpdateAction != parser.ForeignKeyCascade || original.DeleteAction != parser.ForeignKeySetNull || len(original.DeleteAttributes) != 1 || original.DeleteAttributes[0] != child.Attributes[3] {
		t.Fatal(original)
	}
	for _, change := range []func(*catalog.ForeignKeyConstraint){
		func(f *catalog.ForeignKeyConstraint) { f.UpdateAction = 255 }, func(f *catalog.ForeignKeyConstraint) { f.DeleteAction = 255 }, func(f *catalog.ForeignKeyConstraint) { f.DeleteAction = parser.ForeignKeyCascade }, func(f *catalog.ForeignKeyConstraint) { f.DeleteAttributes = []int32{child.Attributes[2]} }, func(f *catalog.ForeignKeyConstraint) { f.DeleteAttributes = []int32{99} }, func(f *catalog.ForeignKeyConstraint) {
			f.DeleteAttributes = []int32{child.Attributes[3], child.Attributes[3]}
		},
	} {
		candidate := *child
		candidate.ForeignKeys = slices.Clone(child.ForeignKeys)
		change(&candidate.ForeignKeys[0])
		if err := validateForeignKeys(view, &candidate, map[string]bool{}); err == nil {
			t.Fatal("invalid action attachment admitted", candidate.ForeignKeys[0])
		}
	}
	if !reflect.DeepEqual(original, child.ForeignKeys[0]) {
		t.Fatal("validation changed retained definition")
	}
	sessionSQL(t, s, "ALTER TABLE c DROP COLUMN dead")
	sessionSQL(t, s, "ALTER TABLE c RENAME b TO author")
	next := e.CatalogRead()
	current := next.MustRelation(ref.Address)
	if !slices.Equal(current.ForeignKeys[0].DeleteAttributes, original.DeleteAttributes) || !sameForeignKeyDefinition(original, current.ForeignKeys[0]) {
		t.Fatal("slot/name rewrite retargeted action", original, current.ForeignKeys[0])
	}
	text, err := (pgRenderer{view: next, relation: current, path: []ids.ObjectAddress{current.Namespace()}}).foreignKeyConstraintSQL(current, current.ForeignKeys[0])
	if err != nil || text != "FOREIGN KEY (a, author) REFERENCES p(a, b) ON UPDATE CASCADE ON DELETE SET NULL (author)" {
		t.Fatal(text, err)
	}
	oldText, err := (pgRenderer{view: view, relation: child, path: []ids.ObjectAddress{child.Namespace()}}).foreignKeyConstraintSQL(child, original)
	if err != nil || oldText != "FOREIGN KEY (a, b) REFERENCES p(a, b) ON UPDATE CASCADE ON DELETE SET NULL (b)" {
		t.Fatal("retained deparse changed", oldText, err)
	}
}

func TestForeignActionsEquality(t *testing.T) {
	for _, tc := range []struct{ typ, old, next string }{{"NUMERIC", "1.0", "1.00"}, {"DOUBLE PRECISION", "0", "-0"}, {"INTERVAL", "1 day", "24 hours"}} {
		t.Run(tc.typ, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE p(id "+tc.typ+" PRIMARY KEY)")
			sessionSQL(t, s, "CREATE TABLE c(pid "+tc.typ+" REFERENCES p ON UPDATE CASCADE)")
			view := e.CatalogRead()
			child := view.MustRelation(catalogRef(t, e, view, "c").Address)
			f := child.ForeignKeys[0]
			parent := view.MustRelation(f.Target)
			index := view.MustRelation(f.Index)
			comparisons, err := bindForeignKeyComparison(view, child, parent, index, f)
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
			if foreignActionSameKey(comparisons, []sqltype.Value{old}, []sqltype.Value{next}) {
				t.Fatal("SQL equality suppressed representation transition")
			}
			equal, err := foreignKeyRowsMatch(context.Background(), comparisons, []sqltype.Value{old}, []sqltype.Value{next}, sqltype.InputEnvironment{})
			if err != nil || !equal {
				t.Fatal("matching lost selected supporting equality", equal, err)
			}
			if !foreignActionSameKey(comparisons, []sqltype.Value{next}, []sqltype.Value{next}) {
				t.Fatal("identical datum created an event")
			}
		})
	}
}

func TestForeignActionsAdmission(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE p(id INT PRIMARY KEY)", "CREATE TABLE c(pid INT REFERENCES p ON UPDATE CASCADE)", "CREATE TABLE unrelated(id INT)", "INSERT INTO p VALUES(1)", "INSERT INTO c VALUES(1)"} {
		sessionSQL(t, s, q)
	}
	scope := foreignActionUnitScope(t, e, s, "UPDATE p SET id=2")
	named, storage, descriptor := scope.lockedRef, scope.lockedStorage, scope.lockedDescriptor
	jobs, auth, err := scope.prepareForeignActions()
	if err != nil {
		t.Fatal(err)
	}
	job := foreignActionUnitJob(t, jobs, "c")
	ref := e.catalog.Domain.Ref(auth.catalog.Epoch(), job.child.Address())
	children, err := scope.captureForeignKeyRows(ref, ids.ObjectAddress{})
	if err != nil {
		t.Fatal(err)
	}
	old := children.committed[0]
	value, _ := sqltype.Integer(job.child.Columns[0].Usage.Type, 2)
	replacement := [1][]sqltype.Value{{value}}
	predecessor := [1]rowVersion{old}
	if err = scope.stage.replace(ref, predecessor[:], indexWriteBatch{values: replacement[:]}); err == nil {
		t.Fatal("FK address alone authorized a child replacement")
	}
	if err = scope.admitForeignAction(ref, job, false); err != nil {
		t.Fatal(err)
	}
	if err = scope.foreignKeyGrant(ref, old.row, lock.ForUpdate, children.storage); err != nil {
		t.Fatal(err)
	}
	if err = scope.stage.deleteForeignAction(ref, old); err == nil {
		t.Fatal("read-only action authority admitted deletion")
	}
	if err = scope.admitForeignAction(ref, job, true); err != nil {
		t.Fatal(err)
	}
	ownerAuth := foreignActionOwner(auth, job.child.Owner)
	scope.foreignActionAuth = &ownerAuth
	scope.foreignActionsSettling = true
	if err = scope.stage.replace(ref, predecessor[:], indexWriteBatch{values: replacement[:]}); err != nil {
		t.Fatal(err)
	}
	scope.foreignActionAuth = nil
	scope.foreignActionsSettling = false
	next := scope.stage.rows[ref.Address][0]
	if next.row != old.row || next.version == old.version || next.base != old.version || len(scope.foreignActionTransitions) != 1 || scope.foreignActionTransitions[0].old.version != old.version || scope.foreignActionTransitions[0].next.version != next.version {
		t.Fatal("physical lineage was not recorded", old, next, scope.foreignActionTransitions)
	}
	if scope.lockedRef != named || scope.lockedStorage != storage || scope.lockedDescriptor != descriptor {
		t.Fatal("child action spoofed initiating admission")
	}
	admission := scope.foreignActionAdmissions[ref.Address]
	if !admission.mutated || admission.tx != scope.txRef || admission.storage != children.storage {
		t.Fatal("missing captured physical child authority", admission)
	}
	foreign := ref
	foreign.Address = catalogRef(t, e, e.CatalogRead(), "unrelated").Address
	e.mu.Lock()
	err = scope.validateForeignActionLocked(e, foreign)
	e.mu.Unlock()
	if err == nil {
		t.Fatal("unrelated child was authorized")
	}
	originalJob := admission.jobs[0]
	admission.jobs[0].definition.DeleteAction = parser.ForeignKeyRestrict
	e.mu.Lock()
	err = scope.validateForeignActionLocked(e, ref)
	e.mu.Unlock()
	admission.jobs[0] = originalJob
	if !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("changed action definition authority survived", err)
	}
	originalStorage := admission.storage
	admission.storage = &relationStorage{}
	e.mu.Lock()
	err = scope.validateForeignActionLocked(e, ref)
	e.mu.Unlock()
	admission.storage = originalStorage
	if !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("foreign child storage survived", err)
	}
}

func TestForeignActionsClosure(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE p(id INT PRIMARY KEY)", "CREATE TABLE a(id INT PRIMARY KEY REFERENCES p ON UPDATE CASCADE)", "CREATE TABLE b(id INT PRIMARY KEY REFERENCES p ON UPDATE CASCADE)", "CREATE TABLE g(id INT,aid INT REFERENCES a ON UPDATE CASCADE,bid INT REFERENCES b ON UPDATE CASCADE)", "INSERT INTO p VALUES(1)", "INSERT INTO a VALUES(1)", "INSERT INTO b VALUES(1)", "INSERT INTO g VALUES(99,1,1)"} {
		sessionSQL(t, s, q)
	}
	scope := foreignActionUnitScope(t, e, s, "UPDATE p SET id=2")
	if err := scope.settleForeignActions(); err != nil {
		t.Fatal(err)
	}
	ref := catalogRef(t, e, e.CatalogRead(), "g")
	rows := scope.stage.rows[ref.Address]
	if len(rows) != 1 {
		t.Fatal(rows)
	}
	var events []foreignActionTransition
	for _, event := range scope.foreignActionTransitions {
		if event.relation == ref.Address {
			events = append(events, event)
		}
	}
	if len(events) != 2 || events[0].old.row != events[1].old.row || events[0].next.version != events[1].old.version || events[0].next.version == events[1].next.version || rows[0].version != events[1].next.version || events[0].next.base != events[1].next.base {
		t.Fatal("diamond lost an actual row transition", events, rows)
	}
	expected, _ := sqltype.Integer(rows[0].values[1].Type(), 2)
	if !sqltype.SameDatum(rows[0].values[1], expected) || !sqltype.SameDatum(rows[0].values[2], expected) {
		t.Fatal("second path overwrote first staged payload", rows[0])
	}
	if scope.lockedRef.Address == ref.Address {
		t.Fatal("closure retargeted named admission")
	}
}

func TestForeignActionsDefaults(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "per-row", true: "late-failure"}[fail], func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			for _, q := range []string{"CREATE TABLE p(id INT PRIMARY KEY)", "CREATE TABLE d(id INT,pid INT DEFAULT 1 REFERENCES p ON DELETE SET DEFAULT)", "INSERT INTO p VALUES(1),(2),(10)", "INSERT INTO d VALUES(11,10),(12,10)"} {
				sessionSQL(t, s, q)
			}
			probe := &defaultOrdinal{}
			if fail {
				probe.failAt = 2
			}
			defaultProbe(t, e, probe)
			ref := catalogRef(t, e, e.CatalogRead(), "d")
			before := e.storage.relations[ref.Address]
			versions := slices.Clone(before.versions)
			scope := foreignActionUnitScope(t, e, s, "DELETE FROM p WHERE id=10")
			err := scope.settleForeignActions()
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
				rows := scope.stage.rows[ref.Address]
				if len(rows) != 2 {
					t.Fatal(rows)
				}
				for i, row := range rows {
					want, _ := sqltype.Integer(row.values[1].Type(), int64(i+1))
					if !sqltype.SameDatum(row.values[1], want) || row.row != versions[i].row || row.base != versions[i].version {
						t.Fatal("default action lineage/value", row, versions[i])
					}
				}
			}
			if e.storage.relations[ref.Address] != before || !reflect.DeepEqual(before.versions, versions) {
				t.Fatal("private action changed committed storage")
			}
		})
	}
}
