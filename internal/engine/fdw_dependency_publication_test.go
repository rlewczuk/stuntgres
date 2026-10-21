package engine

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func fdwLateUnitSetup(t *testing.T, edge string) (*Engine, *session.Session, *session.Session, string, string, ids.ObjectAddress) {
	t.Helper()
	e := New()
	if edge == "procedure_wrapper" || edge == "validator_wrapper" || edge == "transitive_procedure_mapping" {
		definition, err := fdw.ValidateWrapper(fdw.WrapperDefinition{HandlerName: catalog.ForeignFunctionName{Schema: "fdw_cap", Name: "handler"}, Wrapper: &fdwUnitWrapper{}})
		if err != nil {
			t.Fatal(err)
		}
		validator, err := fdw.ValidateValidator(fdw.ValidatorDefinition{
			Name: catalog.ForeignFunctionName{Schema: "fdw_cap", Name: "validator"},
			Validate: func(_ context.Context, _ catalog.ForeignObjectKind, options []catalog.ForeignOption) error {
				for _, option := range options {
					if option.Name != "tag" {
						return fmt.Errorf("unknown test provider option %q", option.Name)
					}
				}
				return nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := e.InstallForeignDefinitions([]fdw.WrapperDefinition{definition}, []fdw.ValidatorDefinition{validator}, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
	if edge != "procedure_wrapper" && edge != "validator_wrapper" {
		q := "CREATE FOREIGN DATA WRAPPER fdw_retire"
		if edge == "transitive_procedure_mapping" {
			q += " HANDLER fdw_cap.handler VALIDATOR fdw_cap.validator"
		}
		sessionSQL(t, a, q)
	}
	if edge == "server_mapping" || edge == "server_table" || edge == "transitive_procedure_mapping" {
		sessionSQL(t, a, "CREATE SERVER fdw_retire_server FOREIGN DATA WRAPPER fdw_retire")
	}
	drop, create := "DROP FOREIGN DATA WRAPPER fdw_retire CASCADE", "CREATE SERVER fdw_late FOREIGN DATA WRAPPER fdw_retire"
	target := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.ForeignWrapperClassID, Name: "fdw_retire"})
	switch edge {
	case "server_mapping", "server_table":
		drop = "DROP SERVER fdw_retire_server CASCADE"
		create = "CREATE USER MAPPING FOR PUBLIC SERVER fdw_retire_server"
		if edge == "server_table" {
			create = "CREATE FOREIGN TABLE fdw_late(id integer) SERVER fdw_retire_server"
		}
		target = e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.ForeignServerClassID, Name: "fdw_retire_server"})
	case "procedure_wrapper", "validator_wrapper", "transitive_procedure_mapping":
		drop = "DROP SCHEMA fdw_cap CASCADE"
		create = "CREATE FOREIGN DATA WRAPPER fdw_late HANDLER fdw_cap.handler"
		if edge == "validator_wrapper" {
			create = "CREATE FOREIGN DATA WRAPPER fdw_late VALIDATOR fdw_cap.validator"
		}
		if edge == "transitive_procedure_mapping" {
			create = "CREATE USER MAPPING FOR PUBLIC SERVER fdw_retire_server"
		}
		target = e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "fdw_cap"})
	}
	if target == (ids.ObjectAddress{}) {
		t.Fatal("missing actual retirement root", edge)
	}
	return e, a, b, drop, create, target
}

func fdwPrepareLateUnitDrop(t *testing.T, e *Engine, scope *StatementScope, query string) *PreparedCatalogPublication {
	t.Helper()
	stmt := statement(t, query)
	var stage *CatalogStage
	var roots []ids.ObjectAddress
	var err error
	e.mu.Lock()
	if foreign, ok := stmt.(*parser.ForeignDDL); ok {
		var auth AuthorizationView
		auth, err = e.authorizationLocked(scope.op)
		if err == nil {
			stage = e.BeginCatalogStage(auth.Catalog)
			stage.authorizeLocked(auth, scope.op.Session)
			_, roots, err = e.stageForeignLocked(scope, auth, stage, foreign)
		}
	} else {
		stage, err = e.stageSchemaLocked(scope.op, stmt, sqltype.InputEnvironment{}, scope.output)
	}
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.DiscardCatalogStage(stage) })
	if len(roots) != 0 {
		err = scope.stageExpressionDependents(stage, roots, parser.DropCascade, nil)
	} else {
		err = scope.stageDomainDependents(stage, parser.DropCascade)
	}
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := e.ValidateCatalogStage(scope.op.Context(), stage)
	if err != nil {
		t.Fatal(err)
	}
	return prepared
}

func fdwLateUnitConflict(t *testing.T, err error, e *Engine, before *catalog.State) {
	t.Helper()
	var conflict catalog.Conflict
	if !errors.As(err, &conflict) || err.Error() != "stuntgres: catalog publication conflict" {
		t.Fatal("late incoming dependency did not reject retirement before publication", err)
	}
	if e.catalog.state != before {
		t.Fatal("failed retirement changed the complete committed metadata graph")
	}
}

func TestFDWLateDependencyPublication(t *testing.T) {
	for _, edge := range []string{"wrapper_server", "server_mapping", "server_table", "procedure_wrapper", "validator_wrapper", "transitive_procedure_mapping"} {
		for _, route := range []string{"validated_direct", "active", "retained"} {
			t.Run(edge+"/"+route, func(t *testing.T) {
				e, a, b, drop, create, _ := fdwLateUnitSetup(t, edge)
				switch route {
				case "validated_direct":
					op, err := a.Acquire(context.Background())
					if err != nil {
						t.Fatal(err)
					}
					defer op.Done()
					scope, err := e.StartStatement(op)
					if err != nil {
						t.Fatal(err)
					}
					defer scope.Finish(context.Canceled)
					prepared := fdwPrepareLateUnitDrop(t, e, scope, drop)
					// The incoming edge is published after provider validation,
					// not merely before preparation of the retirement candidate.
					sessionSQL(t, b, create)
					before := e.catalog.state
					_, err = e.publishCatalogOperation(op, nil, prepared, PublicationToken{Epoch: prepared.epoch})
					fdwLateUnitConflict(t, err, e, before)
				case "active":
					sessionSQL(t, a, "BEGIN")
					if edge == "procedure_wrapper" || edge == "validator_wrapper" || edge == "transitive_procedure_mapping" {
						// Public explicit DROP SCHEMA is deliberately excluded.
						// Exercise the existing eligible private catalog publisher
						// with a genuine prepared schema retirement stage instead.
						if err := fdwApplyLateUnitStage(t, e, a, func(scope *StatementScope) (*PreparedCatalogPublication, error) {
							return fdwPrepareLateUnitDrop(t, e, scope, drop), nil
						}); err != nil {
							t.Fatal(err)
						}
					} else {
						sessionSQL(t, a, drop)
					}
					sessionSQL(t, b, create)
					before := e.catalog.state
					op, err := a.Acquire(context.Background())
					if err != nil {
						t.Fatal(err)
					}
					_, err = e.Execute(op, statement(t, "COMMIT"), 0, nil)
					op.Done()
					fdwLateUnitConflict(t, err, e, before)
				case "retained":
					message := batchUnitStart(t, a, context.Background())
					defer message.end(context.Canceled)
					if edge == "procedure_wrapper" || edge == "validator_wrapper" || edge == "transitive_procedure_mapping" {
						// Admit an ordinary statement to create the retained
						// implicit transaction, then use the real stage publisher.
						// This does not add public transactional schema support.
						err := message.scope.gate(statement(t, "SELECT 1"))
						if err == nil {
							prepared := fdwPrepareLateUnitDrop(t, e, message.scope, drop)
							_, err = e.publishCatalogOperation(message.op, message.scope, prepared, PublicationToken{Epoch: prepared.epoch})
						}
						if finish := message.scope.Finish(err); err == nil {
							err = finish
						}
						if err != nil {
							t.Fatal(err)
						}
					} else if _, err := message.step(statement(t, drop)); err != nil {
						t.Fatal(err)
					}
					sessionSQL(t, b, create)
					before := e.catalog.state
					fdwLateUnitConflict(t, message.end(nil), e, before)
				}
			})
		}
	}
	for _, route := range []string{"validated_direct", "active", "retained"} {
		t.Run("no_late_dependency/"+route, func(t *testing.T) {
			e, a, _, drop, _, target := fdwLateUnitSetup(t, "wrapper_server")
			switch route {
			case "validated_direct":
				op, err := a.Acquire(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				defer op.Done()
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				defer scope.Finish(context.Canceled)
				prepared := fdwPrepareLateUnitDrop(t, e, scope, drop)
				if _, err := e.publishCatalogOperation(op, nil, prepared, PublicationToken{Epoch: prepared.epoch}); err != nil {
					t.Fatal(err)
				}
			case "active":
				sessionSQL(t, a, "BEGIN")
				sessionSQL(t, a, drop)
				sessionSQL(t, a, "COMMIT")
			case "retained":
				message := batchUnitStart(t, a, context.Background())
				defer message.end(context.Canceled)
				if _, err := message.step(statement(t, drop)); err != nil {
					t.Fatal(err)
				}
				if err := message.end(nil); err != nil {
					t.Fatal(err)
				}
			}
			if e.catalog.state.Get(target) != nil || e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.ForeignWrapperClassID, Name: "fdw_retire"}) != (ids.ObjectAddress{}) {
				t.Fatal("uncontended retirement retained descriptor or name")
			}
		})
	}
	t.Run("same_transaction_create_drop", func(t *testing.T) {
		e := New()
		a := sessionStart(t, e, nil)
		sessionSQL(t, a, "BEGIN")
		sessionSQL(t, a, "CREATE FOREIGN DATA WRAPPER fdw_private")
		sessionSQL(t, a, "CREATE SERVER fdw_private_server FOREIGN DATA WRAPPER fdw_private")
		sessionSQL(t, a, "DROP FOREIGN DATA WRAPPER fdw_private CASCADE")
		sessionSQL(t, a, "COMMIT")
		if e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.ForeignWrapperClassID, Name: "fdw_private"}) != (ids.ObjectAddress{}) || e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.ForeignServerClassID, Name: "fdw_private_server"}) != (ids.ObjectAddress{}) {
			t.Fatal("private creation/retirement published ghost names")
		}
	})
	t.Run("retargeted_committed_owner", func(t *testing.T) {
		e, a, b, drop, _, _ := fdwLateUnitSetup(t, "wrapper_server")
		target := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.ForeignWrapperClassID, Name: "fdw_retire"})
		sessionSQL(t, b, "CREATE FOREIGN DATA WRAPPER fdw_other")
		sessionSQL(t, b, "CREATE SERVER fdw_retarget FOREIGN DATA WRAPPER fdw_other")
		server := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.ForeignServerClassID, Name: "fdw_retarget"})
		sessionSQL(t, a, "BEGIN")
		sessionSQL(t, a, drop)
		sessionSQL(t, b, "BEGIN")
		if err := fdwEditLateUnitServer(t, e, b, server, func(stage *CatalogStage, old *catalog.ForeignObject) error {
			next := *old
			next.Wrapper = target
			var err error
			next.ObjectRevision, err = e.catalog.revisionLocked()
			if err != nil {
				return err
			}
			return stage.Replace(&next)
		}); err != nil {
			t.Fatal(err)
		}
		sessionSQL(t, b, "COMMIT")
		before := e.catalog.state
		op, err := a.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		_, err = e.Execute(op, statement(t, "COMMIT"), 0, nil)
		op.Done()
		fdwLateUnitConflict(t, err, e, before)
	})
	t.Run("own_removed_late_owner", func(t *testing.T) {
		e, a, b, drop, create, target := fdwLateUnitSetup(t, "wrapper_server")
		sessionSQL(t, a, "BEGIN")
		sessionSQL(t, a, drop)
		sessionSQL(t, b, create)
		late := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.ForeignServerClassID, Name: "fdw_late"})
		if err := fdwEditLateUnitServer(t, e, a, late, func(stage *CatalogStage, old *catalog.ForeignObject) error {
			return stage.removeObjectLocked(e.catalog.Domain.Ref(stage.Base.Epoch(), old.Address()), false)
		}); err != nil {
			t.Fatal(err)
		}
		sessionSQL(t, a, "COMMIT")
		if e.catalog.state.Get(target) != nil || e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.ForeignServerClassID, Name: "fdw_late"}) != (ids.ObjectAddress{}) {
			t.Fatal("own removal did not publish a coherent empty graph")
		}
	})
	for _, restoreRetirement := range []bool{false, true} {
		t.Run(map[bool]string{false: "savepoint_discards_retirement", true: "savepoint_preserves_retirement"}[restoreRetirement], func(t *testing.T) {
			e, a, b, drop, create, target := fdwLateUnitSetup(t, "wrapper_server")
			sessionSQL(t, a, "BEGIN")
			if restoreRetirement {
				sessionSQL(t, a, drop)
				sessionSQL(t, a, "SAVEPOINT before_late")
			} else {
				sessionSQL(t, a, "SAVEPOINT before_late")
				sessionSQL(t, a, drop)
			}
			sessionSQL(t, b, create)
			sessionSQL(t, a, "ROLLBACK TO SAVEPOINT before_late")
			before := e.catalog.state
			op, err := a.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			_, err = e.Execute(op, statement(t, "COMMIT"), 0, nil)
			op.Done()
			if restoreRetirement {
				fdwLateUnitConflict(t, err, e, before)
			} else if err != nil || e.catalog.state.Get(target) == nil {
				t.Fatal("rolled-back retirement retained a false dependency fence", err)
			}
		})
	}
	for _, route := range []string{"validated_direct", "active", "retained"} {
		t.Run("unrelated_change/"+route, func(t *testing.T) {
			e, a, b, drop, _, target := fdwLateUnitSetup(t, "wrapper_server")
			sessionSQL(t, a, "CREATE FOREIGN DATA WRAPPER fdw_unrelated")
			sessionSQL(t, a, "CREATE SERVER fdw_unrelated_server FOREIGN DATA WRAPPER fdw_unrelated")
			var err error
			switch route {
			case "validated_direct":
				op, acquireErr := a.Acquire(context.Background())
				if acquireErr != nil {
					t.Fatal(acquireErr)
				}
				defer op.Done()
				scope, startErr := e.StartStatement(op)
				if startErr != nil {
					t.Fatal(startErr)
				}
				defer scope.Finish(context.Canceled)
				prepared := fdwPrepareLateUnitDrop(t, e, scope, drop)
				sessionSQL(t, b, "ALTER SERVER fdw_unrelated_server OPTIONS(tag 'changed')")
				_, err = e.publishCatalogOperation(op, nil, prepared, PublicationToken{Epoch: prepared.epoch})
			case "active":
				sessionSQL(t, a, "BEGIN")
				sessionSQL(t, a, drop)
				sessionSQL(t, b, "ALTER SERVER fdw_unrelated_server OPTIONS(tag 'changed')")
				op, acquireErr := a.Acquire(context.Background())
				if acquireErr != nil {
					t.Fatal(acquireErr)
				}
				_, err = e.Execute(op, statement(t, "COMMIT"), 0, nil)
				op.Done()
			case "retained":
				message := batchUnitStart(t, a, context.Background())
				defer message.end(context.Canceled)
				if _, stepErr := message.step(statement(t, drop)); stepErr != nil {
					t.Fatal(stepErr)
				}
				sessionSQL(t, b, "ALTER SERVER fdw_unrelated_server OPTIONS(tag 'changed')")
				err = message.end(nil)
			}
			if err != nil || e.catalog.state.Get(target) != nil || e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.ForeignWrapperClassID, Name: "fdw_unrelated"}) == (ids.ObjectAddress{}) {
				t.Fatal("unrelated metadata change blocked or lost independent retirement", err)
			}
			server := e.catalog.state.MustForeign(e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.ForeignServerClassID, Name: "fdw_unrelated_server"}))
			if len(server.Options) != 1 || server.Options[0] != (catalog.ForeignOption{Name: "tag", Value: "changed"}) {
				t.Fatal("unrelated committed server revision was lost", server)
			}
		})
	}
}

func TestFDWLateDependencyOwnRewire(t *testing.T) {
	e, a, b, drop, create, target := fdwLateUnitSetup(t, "wrapper_server")
	sessionSQL(t, a, "CREATE FOREIGN DATA WRAPPER fdw_survivor")
	survivor := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.ForeignWrapperClassID, Name: "fdw_survivor"})
	sessionSQL(t, a, "BEGIN")
	sessionSQL(t, a, drop)
	sessionSQL(t, b, create)
	late := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.ForeignServerClassID, Name: "fdw_late"})
	// Exercise an actual provider-valid private catalog replacement. This is
	// not a new SQL ALTER SERVER ... WRAPPER spelling: it tests the common
	// publisher's final candidate override when the old incoming edge is gone.
	err := fdwEditLateUnitServer(t, e, a, late, func(stage *CatalogStage, old *catalog.ForeignObject) error {
		next := *old
		next.Wrapper = survivor
		var err error
		next.ObjectRevision, err = e.catalog.revisionLocked()
		if err != nil {
			return err
		}
		return stage.Replace(&next)
	})
	if err != nil {
		t.Fatal(err)
	}
	sessionSQL(t, a, "COMMIT")
	server, ok := e.catalog.state.Foreign(late)
	if !ok || server.Wrapper != survivor || e.catalog.state.Get(target) != nil || e.catalog.state.Get(survivor) == nil {
		t.Fatal("final valid rewire falsely conflicted or published a dangling edge", server)
	}
}

func fdwApplyLateUnitStage(t *testing.T, e *Engine, session *session.Session, prepare func(*StatementScope) (*PreparedCatalogPublication, error)) error {
	t.Helper()
	op, err := session.Acquire(context.Background())
	if err != nil {
		return err
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		return err
	}
	defer scope.Finish(context.Canceled)
	// Enter the existing eligible statement/transaction lifecycle; the
	// retirement itself is prepared and published through the catalog API.
	if err := scope.gate(statement(t, "SELECT 1")); err != nil {
		return err
	}
	prepared, err := prepare(scope)
	if prepared != nil {
		defer e.DiscardCatalogStage(prepared.stage)
	}
	if err == nil {
		_, err = e.publishCatalogOperation(op, scope, prepared, PublicationToken{Epoch: prepared.epoch})
	}
	if finish := scope.Finish(err); err == nil {
		err = finish
	}
	return err
}

func fdwEditLateUnitServer(t *testing.T, e *Engine, session *session.Session, address ids.ObjectAddress, edit func(*CatalogStage, *catalog.ForeignObject) error) error {
	t.Helper()
	return fdwApplyLateUnitStage(t, e, session, func(scope *StatementScope) (*PreparedCatalogPublication, error) {
		e.mu.Lock()
		auth, err := e.authorizationLocked(scope.op)
		var stage *CatalogStage
		if err == nil {
			// Capture the authentic committed predecessor, where the late
			// server's wrapper still exists. The common overlay publisher then
			// merges this valid removal/rewire with our earlier retirement.
			stage = e.BeginCatalogStage(e.catalog.read(e.catalog.state))
			stage.authorizeLocked(auth, scope.op.Session)
			err = edit(stage, stage.Base.MustForeign(address))
		}
		e.mu.Unlock()
		if err != nil {
			if stage != nil {
				e.DiscardCatalogStage(stage)
			}
			return nil, err
		}
		prepared, err := e.ValidateCatalogStage(scope.op.Context(), stage)
		if err != nil {
			e.DiscardCatalogStage(stage)
		}
		return prepared, err
	})
}
