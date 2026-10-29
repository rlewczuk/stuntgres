package schemacmds_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/parser"
)

// Invoke the genuine producer with a live statement collector, before the
// lexical dispatcher retires it. The journal and Finish remain the composed
// owners; no command receipt or candidate descriptor is installed by the test.
func TestExtensionCommandPublication(t *testing.T) {
	for _, action := range []string{"add", "drop", "move", "same_schema", "duplicate"} {
		for _, finish := range []string{"success", "cancel", "veto"} {
			t.Run(action+"/"+finish, func(t *testing.T) {
				e := engine.New()
				actor := enginetest.StartSession(t, e, nil)
				for _, q := range []string{
					"CREATE SCHEMA ec_old",
					"CREATE SCHEMA ec_new",
					"CREATE EXTENSION unaccent WITH SCHEMA ec_old",
					"CREATE TYPE ec_old.ec_member AS ENUM ('a')",
				} {
					enginetest.SessionSQL(t, actor, q)
				}
				if action == "drop" || action == "duplicate" {
					enginetest.SessionSQL(t, actor, "ALTER EXTENSION unaccent ADD TYPE ec_old.ec_member")
				}
				before := e.CatalogRead()
				extAddress := before.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"})
				old, ok := before.Extension(extAddress)
				if !ok {
					t.Fatal("missing genuine installation")
				}
				memberAddress := before.NameAddress(ids.NameKey{ClassID: ids.TypeClassID, Namespace: old.TargetNamespace, Name: "ec_member"})
				member := before.Get(memberAddress)
				if member == nil {
					t.Fatal("missing genuine member")
				}
				frontier := e.Visibility.CommitSeq()
				q := "ALTER EXTENSION unaccent ADD TYPE ec_old.ec_member"
				switch action {
				case "drop":
					q = "ALTER EXTENSION unaccent DROP TYPE ec_old.ec_member"
				case "move":
					q = "ALTER EXTENSION unaccent SET SCHEMA ec_new"
				case "same_schema":
					q = "ALTER EXTENSION unaccent SET SCHEMA ec_old"
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				op, err := actor.Acquire(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer op.Done()
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				defer scope.Finish(errors.New("unfinished extension command proof"))
				stmt := enginetest.Statement(t, q).(*parser.AlterExtension)
				// Direct producer inspection still needs the real statement gate:
				// it admits the transaction and makes Finish own pending effects.
				if err := scope.Gate(stmt); err != nil {
					t.Fatal(err)
				}
				command := &core.EventCommand{Tag: "ALTER EXTENSION", CollectDDL: true}
				scope.EventCommand = command
				_, executeErr := schemacmds.ExecuteExtensionAlter(e, scope, stmt, 0)
				if executeErr == nil {
					scope.CompleteUtility()
				}
				if action == "duplicate" {
					if executeErr == nil || len(command.Commands) != 0 || scope.Catalog != nil {
						t.Fatal("failed membership admitted command or journal", executeErr, command.Commands)
					}
				} else {
					if executeErr != nil || scope.Catalog == nil || scope.CatalogAuthority == nil {
						t.Fatal("successful command lost publication admission", executeErr)
					}
					if action == "same_schema" {
						if len(command.Commands) != 0 || e.PrivateCatalogLocked(scope.PendingCatalog()).Get(extAddress) != old {
							t.Fatal("same-schema admission minted descriptor or receipt", command.Commands)
						}
					} else {
						if len(command.Commands) != 1 {
							t.Fatal("expected one outer receipt, not member commands", command.Commands)
						}
						got := command.Commands[0]
						if got.Tag != "ALTER EXTENSION" || got.ClassID != uint32(extAddress.ClassID) || got.ObjectID != uint32(extAddress.ObjectID) || got.SubID != 0 || got.SpecialKind != "" {
							t.Fatal("wrong outer extension receipt", got)
						}
						if e.PrivateCatalogLocked(scope.PendingCatalog()).Get(extAddress) == old {
							t.Fatal("receipt has no genuine private mutation")
						}
					}
				}
				if e.CatalogRead().State() != before.State() || before.Get(extAddress) != old || before.Get(memberAddress) != member || e.Visibility.CommitSeq() != frontier {
					t.Fatal("producer mutated committed or retained roots before Finish")
				}
				if err := scope.FinishEventCommand(nil, executeErr); err != executeErr {
					t.Fatal("lexical completion changed producer outcome", err)
				}
				primary := executeErr
				if executeErr == nil {
					switch finish {
					case "cancel":
						cancel()
					case "veto":
						primary = errors.New("extension command Finish veto")
					}
				}
				finishErr := scope.Finish(primary)
				rejected := executeErr != nil || finish != "success"
				if rejected {
					if finishErr == nil || e.CatalogRead().State() != before.State() || e.Visibility.CommitSeq() != frontier {
						t.Fatal("rejected command published catalog or visibility", finishErr)
					}
					if executeErr == nil && finish == "cancel" && !errors.Is(finishErr, context.Canceled) {
						t.Fatal("lost cancellation", finishErr)
					}
				} else if finishErr != nil {
					t.Fatal(finishErr)
				} else {
					after := e.CatalogRead()
					next, _ := after.Extension(extAddress)
					switch action {
					case "add":
						if catalog.ExtensionMembership(after.Get(memberAddress)) != extAddress {
							t.Fatal("membership did not publish")
						}
					case "drop":
						if after.Get(memberAddress) == nil || catalog.ExtensionMembership(after.Get(memberAddress)) != (ids.ObjectAddress{}) {
							t.Fatal("detach deleted member or retained membership")
						}
					case "move":
						if next == nil || next.TargetNamespace == old.TargetNamespace {
							t.Fatal("real relocation did not publish")
						}
					case "same_schema":
						if next != old || after.Get(memberAddress) != member {
							t.Fatal("same-target command replaced immutable descriptors")
						}
					}
				}
				if scope.EventCommand != nil || core.ActiveScopeOfLease(op.Lease) != nil {
					t.Fatal("Finish retained lexical command authority")
				}
				if before.Get(extAddress) != old || before.Get(memberAddress) != member {
					t.Fatal("publication mutated retained predecessors")
				}
			})
		}
	}
}
