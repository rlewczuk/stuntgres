package tablecmds_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestAlterValidateConstraintPublication(t *testing.T) {
	for _, terminal := range []string{"success", "late-error", "cancel"} {
		t.Run(terminal, func(t *testing.T) {
			e := engine.New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE t(a INT UNIQUE,b INT)")
			sessionSQL(t, s, "INSERT INTO t VALUES(1,-1)")
			sessionSQL(t, s, "ALTER TABLE t ADD CONSTRAINT good CHECK(a>0) NOT VALID,ADD CONSTRAINT bad CHECK(b>0) NOT VALID")
			old := alterConstraintDescriptor(t, e, "t")
			before, store := e.Catalog.State, e.Storage.Relations[old.Address()]
			root := e.Storage.IndexRoots[old.Constraints.Unique[0].Index]
			q := "ALTER TABLE t VALIDATE CONSTRAINT good"
			if terminal == "late-error" {
				q += ",VALIDATE CONSTRAINT bad,ADD COLUMN companion INT"
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, err := s.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			scope, err := e.StartStatement(op)
			if err != nil {
				op.Done()
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if !scope.Finished {
					_ = scope.Finish(context.Canceled)
				}
				op.Done()
			})
			raw, err := parser.Parse(q)
			if err == nil {
				_, err = scope.Execute(raw[0], 0, nil)
			}
			if terminal == "late-error" {
				if err == nil {
					t.Fatal("late historical scan accepted invalid row")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if e.Catalog.State != before || e.Storage.Relations[old.Address()] != store || e.Storage.IndexRoots[old.Constraints.Unique[0].Index] != root || !old.Constraints.Check[0].NotValid || !old.Constraints.Check[1].NotValid {
				t.Fatal("public prefix before Finish")
			}
			if terminal != "late-error" {
				candidate := scope.Catalog.Descriptors[old.Address()].(*catalog.Relation)
				selected, selectErr := table.SelectAlterConstraintValidation(candidate, "good")
				if selectErr != nil || selected.Invalid() || candidate.Revision() != old.Revision()+1 {
					t.Fatal("missing private promotion", selected, selectErr)
				}
			}
			if terminal == "cancel" {
				cancel()
			}
			finishErr := scope.Finish(err)
			if terminal == "cancel" && !errors.Is(finishErr, context.Canceled) {
				t.Fatal("missing final cancellation veto", finishErr)
			}
			if terminal == "success" {
				if finishErr != nil {
					t.Fatal(finishErr)
				}
				current := alterConstraintDescriptor(t, e, "t")
				v, selectErr := table.SelectAlterConstraintValidation(current, "good")
				if selectErr != nil || v.Invalid() || v.OID != old.Constraints.Check[1].OID {
					t.Fatal("publication identity", v, selectErr)
				}
			} else {
				if finishErr == nil {
					t.Fatal("failure swallowed")
				}
				if e.Catalog.State != before || e.Storage.Relations[old.Address()] != store || e.Storage.IndexRoots[old.Constraints.Unique[0].Index] != root {
					t.Fatal("failed Finish retained private roots")
				}
			}
			scope.Op.Done()
			lockEmpty(t, e)
			if terminal == "success" {
				current := alterConstraintDescriptor(t, e, "t")
				catalogRoot := e.Catalog.State
				noop, noErr := alterConstraintExecuteScope(t, e, s, "ALTER TABLE t VALIDATE CONSTRAINT good,VALIDATE CONSTRAINT good")
				if noErr != nil {
					t.Fatal(noErr)
				}
				if err := noop.Finish(nil); err != nil {
					t.Fatal(err)
				}
				noop.Op.Done()
				if e.Catalog.State != catalogRoot || alterConstraintDescriptor(t, e, "t") != current || current.Revision() != old.Revision()+1 {
					t.Fatal("real no-op changed descriptor/root/revision")
				}
			}
		})
	}
	t.Run("enforced-concurrent-history", func(t *testing.T) {
		e := engine.New()
		owner, peer := sessionStart(t, e, nil), sessionStart(t, e, nil)
		sessionSQL(t, owner, "CREATE TABLE t(a INT UNIQUE,b INT)")
		sessionSQL(t, owner, "INSERT INTO t VALUES(1,1)")
		sessionSQL(t, owner, "ALTER TABLE t ADD CONSTRAINT ck CHECK(b>0) NOT VALID")
		old := alterConstraintDescriptor(t, e, "t")
		scope, err := alterConstraintExecuteScope(t, e, owner, "ALTER TABLE t VALIDATE CONSTRAINT ck")
		if err != nil {
			t.Fatal(err)
		}
		if len(scope.Catalog.Storage) != 0 {
			t.Fatal("validity-only scan installed heap shape")
		}
		beforeStore := e.Storage.Relations[old.Address()]
		beforeGeneration := beforeStore.Generation
		sessionSQL(t, peer, "INSERT INTO t VALUES(2,2)")
		sessionSQL(t, peer, "UPDATE t SET b=3 WHERE a=1")
		sessionSQL(t, peer, "DELETE FROM t WHERE a=2")
		store, root := e.Storage.Relations[old.Address()], e.Storage.IndexRoots[old.Constraints.Unique[0].Index]
		if store != beforeStore || store.Generation <= beforeGeneration {
			t.Fatal("fixture did not distinguish heap identity from real DML generation advancement")
		}
		e.Mu.Lock()
		for _, fence := range scope.Catalog.Fences {
			if committed, ok := fence.(session.CommittedFence); ok {
				if err := committed.Current(e.Catalog.State, e.Storage.Relations); err != nil {
					e.Mu.Unlock()
					t.Fatal("validity-only journal retained a tuple-revision freeze", fence.Kind(), err)
				}
			}
		}
		e.Mu.Unlock()
		if err := scope.Finish(nil); err != nil {
			t.Fatal("enforced append/Ends evolution rejected", err)
		}
		scope.Op.Done()
		if e.Storage.Relations[old.Address()] != store || e.Storage.IndexRoots[old.Constraints.Unique[0].Index] != root {
			t.Fatal("validation republished DML heap/index roots")
		}
		lockEmpty(t, e)
	})
	t.Run("prior-shape-authority", func(t *testing.T) {
		e := engine.New()
		owner := sessionStart(t, e, nil)
		sessionSQL(t, owner, "CREATE TABLE t(a INT)")
		sessionSQL(t, owner, "INSERT INTO t VALUES(1)")
		sessionSQL(t, owner, "ALTER TABLE t ADD CONSTRAINT ck CHECK(a>0) NOT VALID")
		tx := transactionBegin(t, owner)
		sessionSQL(t, owner, "ALTER TABLE t ADD COLUMN b INT DEFAULT 7")
		prior := owner.Transactions.Catalog
		shapes := prior.Clone().Storage
		fences := append([]session.Fence(nil), prior.Fences...)
		scope, err := alterConstraintExecuteScope(t, e, owner, "ALTER TABLE t VALIDATE CONSTRAINT ck")
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(scope.Catalog.Storage, shapes) || len(scope.Catalog.Fences) < len(fences) || !reflect.DeepEqual(scope.Catalog.Fences[:len(fences)], fences) {
			t.Fatal("validation erased/replaced prior shape or strict fences")
		}
		if err := scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
		scope.Op.Done()
		transactionEnd(t, owner, tx, false)
		lockEmpty(t, e)
	})
}

func TestAlterValidateConstraintAdmission(t *testing.T) {
	e := engine.New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE p(id INT PRIMARY KEY)")
	sessionSQL(t, s, "CREATE TABLE c(id INT)")
	sessionSQL(t, s, "ALTER TABLE c ADD CONSTRAINT fk FOREIGN KEY(id) REFERENCES p NOT VALID")
	child, parent := alterConstraintDescriptor(t, e, "c"), alterConstraintDescriptor(t, e, "p")
	scope, err := alterConstraintExecuteScope(t, e, s, "ALTER TABLE c VALIDATE CONSTRAINT fk")
	if err != nil {
		t.Fatal(err)
	}
	captures := tablecmds.ForeignKeysOrZero(scope)
	if len(captures.AlterKeys) != 0 || len(captures.ValidationKeys) != 1 {
		t.Fatal("existing validation confused with ADD capture", captures)
	}
	capture := captures.ValidationKeys[0]
	if capture.Constraint != child.Constraints.ForeignKeys[0].OID || capture.Target.Address != parent.Address() || capture.Index != child.Constraints.ForeignKeys[0].Index || capture.Storage != e.Storage.Relations[parent.Address()] {
		t.Fatal("inauthentic validation provider", capture)
	}
	foreignKeyHeldMode(t, e, catalogRef(t, e, e.CatalogRead(), "c"), 0, scope.LockTicket, lock.ShareUpdateExclusive)
	foreignKeyHeldMode(t, e, catalogRef(t, e, e.CatalogRead(), "p"), 0, scope.LockTicket, lock.RowShare)
	mark, err := scope.BeginPLException()
	if err != nil {
		t.Fatal(err)
	}
	captures.ValidationKeys[0].Constraint = 0
	captures.ValidationKeys = append(captures.ValidationKeys, capture)
	if err := scope.EndPLException(mark, true, nil); err != nil {
		t.Fatal(err)
	}
	restored := tablecmds.ForeignKeysOrZero(scope)
	if len(restored.ValidationKeys) != 1 || restored.ValidationKeys[0] != capture {
		t.Fatal("checkpoint retained borrowed validation slice", restored.ValidationKeys)
	}
	if err := scope.Finish(context.Canceled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	scope.Op.Done()
	lockEmpty(t, e)
	for _, actions := range [][]parser.AlterTableAction{
		{{Action: parser.ValidateConstraint}},
		{{Action: parser.AddForeignKey}, {Action: parser.ValidateConstraint}},
		{{Action: parser.ValidateConstraint}, {Action: parser.AddForeignKey}},
		{{Action: parser.ValidateConstraint}, {Action: parser.AddColumn}},
	} {
		want := lock.ShareUpdateExclusive
		for _, a := range actions {
			if a.Action == parser.AddForeignKey {
				want = lock.ShareRowExclusive
			}
			if a.Action == parser.AddColumn {
				want = lock.AccessExclusive
			}
		}
		if got := table.AlterTableLockMode(&parser.AlterTable{Actions: actions}); got != want {
			t.Fatal("weakened companion mode", got, want)
		}
	}
	sessionSQL(t, s, "CREATE TABLE self(id INT PRIMARY KEY)")
	sessionSQL(t, s, "ALTER TABLE self ADD CONSTRAINT self_fk FOREIGN KEY(id) REFERENCES self NOT VALID")
	self, err := alterConstraintExecuteScope(t, e, s, "ALTER TABLE self VALIDATE CONSTRAINT self_fk")
	if err != nil {
		t.Fatal(err)
	}
	foreignKeyHeldMode(t, e, catalogRef(t, e, e.CatalogRead(), "self"), 0, self.LockTicket, lock.ShareUpdateExclusive)
	if err := self.Finish(nil); err != nil {
		t.Fatal(err)
	}
	self.Op.Done()
	lockEmpty(t, e)
	sessionSQL(t, s, "CREATE TABLE root(a INT)")
	sessionSQL(t, s, "CREATE TABLE middle() INHERITS(root)")
	sessionSQL(t, s, "CREATE TABLE leaf() INHERITS(middle)")
	sessionSQL(t, s, "ALTER TABLE root ADD CONSTRAINT ck CHECK(a>0) NOT VALID")
	hierarchy, err := alterConstraintExecuteScope(t, e, s, "ALTER TABLE root VALIDATE CONSTRAINT ck")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"root", "middle", "leaf"} {
		d := alterConstraintDescriptor(t, e, name)
		foreignKeyHeldMode(t, e, catalogRef(t, e, e.CatalogRead(), name), 0, hierarchy.LockTicket, lock.ShareUpdateExclusive)
		if !d.Constraints.Check[0].NotValid {
			t.Fatal("hierarchy published before Finish", name)
		}
	}
	if err := hierarchy.Finish(context.Canceled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	hierarchy.Op.Done()
	lockEmpty(t, e)
	for _, replacement := range []string{"parent", "provider", "storage", "epoch"} {
		t.Run(replacement+"-fence", func(t *testing.T) {
			target := engine.New()
			owner := sessionStart(t, target, nil)
			sessionSQL(t, owner, "CREATE TABLE p(id INT PRIMARY KEY)")
			sessionSQL(t, owner, "CREATE TABLE c(id INT)")
			sessionSQL(t, owner, "ALTER TABLE c ADD CONSTRAINT fk FOREIGN KEY(id) REFERENCES p NOT VALID")
			oldChild := alterConstraintDescriptor(t, target, "c")
			parent := alterConstraintDescriptor(t, target, "p")
			pending, err := alterConstraintExecuteScope(t, target, owner, "ALTER TABLE c VALIDATE CONSTRAINT fk")
			if err != nil {
				t.Fatal(err)
			}
			if replacement == "storage" {
				// Inject a replaced incarnation through the actual owner map, not
				// through SQL that the held parent grant correctly excludes.
				store := *target.Storage.Relations[parent.Address()]
				target.Storage.Relations[parent.Address()] = &store
			} else if replacement == "epoch" {
				next := *target.Catalog.State
				next.SetEpoch(next.Epoch() + 1)
				target.Catalog.State = &next
			} else {
				address := parent.Address()
				if replacement == "provider" {
					address = oldChild.Constraints.ForeignKeys[0].Index
				}
				next := *target.CatalogRead().MustRelation(address)
				next.ObjectRevision++
				stage := target.BeginCatalogStage(target.CatalogRead())
				if err := stage.Replace(&next); err != nil {
					t.Fatal(err)
				}
				ownershipUnitPublish(t, target, stage)
				target.DiscardCatalogStage(stage)
			}
			observed := target.Catalog.State
			if err := pending.Finish(nil); err == nil {
				t.Fatal("replaced validation source admitted", replacement)
			}
			pending.Op.Done()
			if target.Catalog.State != observed || alterConstraintDescriptor(t, target, "c") != oldChild || !oldChild.Constraints.ForeignKeys[0].NotValid {
				t.Fatal("rejected source published validity")
			}
			lockEmpty(t, target)
		})
	}
	t.Run("recursive-authority", func(t *testing.T) {
		target := engine.New()
		owner := sessionStart(t, target, nil)
		for _, q := range []string{
			"CREATE ROLE validate_owner", "CREATE ROLE validate_other",
			"GRANT USAGE ON SCHEMA public TO validate_owner",
			"CREATE TABLE root(a INT)", "CREATE TABLE child() INHERITS(root)",
			"ALTER TABLE root ADD CONSTRAINT ck CHECK(a>0) NOT VALID",
			"ALTER TABLE child OWNER TO validate_other", "ALTER TABLE root OWNER TO validate_owner",
			"SET ROLE validate_owner",
		} {
			sessionSQL(t, owner, q)
		}
		root, child := alterConstraintDescriptor(t, target, "root"), alterConstraintDescriptor(t, target, "child")
		validation, err := alterConstraintExecuteScope(t, target, owner, "ALTER TABLE root VALIDATE CONSTRAINT ck")
		if err != nil {
			t.Fatal("propagated validation demanded child ownership", err)
		}
		if err := validation.Finish(context.Canceled); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		validation.Op.Done()
		mixed, err := alterConstraintExecuteScope(t, target, owner, "ALTER TABLE root VALIDATE CONSTRAINT ck,ADD CONSTRAINT companion CHECK(a<100) NOT VALID")
		if err == nil {
			t.Fatal("nonvalidation companion bypassed child ownership")
		}
		var denied *sqlstate.Error
		if !errors.As(err, &denied) || denied.Code != "42501" || denied.Message != "must be owner of table child" {
			t.Fatal("wrong companion authority diagnostic", err)
		}
		if err := mixed.Finish(err); err == nil {
			t.Fatal("owner failure swallowed")
		}
		mixed.Op.Done()
		if alterConstraintDescriptor(t, target, "root") != root || alterConstraintDescriptor(t, target, "child") != child {
			t.Fatal("authority failure published prefix")
		}
		lockEmpty(t, target)
	})
}
