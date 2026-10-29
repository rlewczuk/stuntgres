package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Public fixtures prove SQL outcomes; this inspects the actual shared invocation
// owner's restoration, not a second runtime or a manufactured provider frame.
func TestRefintInvocationCleanup(t *testing.T) {
	for _, mode := range []string{"null", "error", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			e := engine.New()
			setup, release := enginetest.RoleOperation(t, e, "postgres")
			enginetest.RoleExecute(t, e, setup, "CREATE EXTENSION refint")
			enginetest.RoleExecute(t, e, setup, "CREATE TABLE refint_owner(id int)")
			release()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, done, err := enginetest.Operation(e, ctx)
			if err != nil { t.Fatal(err) }
			defer done()
			s, err := e.StartStatement(op)
			if err != nil { t.Fatal(err) }
			defer s.Finish(nil)
			read := e.CatalogRead()
			ns := read.NameAddress(ids.NameKey{ClassID:ids.NamespaceClassID,Name:"public"})
			relation := read.MustRelation(read.NameAddress(ids.NameKey{ClassID:ids.RelationClassID,Namespace:ns,Name:"refint_owner"}))
			ext := read.MustExtension(read.NameAddress(ids.NameKey{ClassID:ids.ExtensionClassID,Name:"refint"}))
			p := read.MustProcedure(ext.Installation.Members[0])
			parts := triggerUnitDefinition(t,read,relation,p,"refint_checked",stored.TriggerInsert).Parts()
			parts.Timing = stored.TriggerAfter
			parts.Arguments = []string{"id","refint_missing","id"}
			definition, err := stored.NewTrigger(parts)
			if err != nil { t.Fatal(err) }
			relation = triggerUnitInstall(t,e,relation,definition)
			triggerUnitAdmit(t,s,relation)
			integer,_ := read.LookupType(sqltype.Int4)
			value := triggerUnitInt(t,integer,1)
			if mode == "null" { value = sqltype.Null(integer) }
			ref := read.Owner().Domain.Ref(read.Epoch(),relation.Address())
			if err := s.WriteHost().AfterRow(dml.BeforeRow{Relation:ref,Event:dml.BeforeInsert,New:[]sqltype.Value{value}}); err != nil { t.Fatal(err) }
			depth := core.ScopeActiveSQLRoutineDepth(s)
			actor := s.Op.EffectiveUser
			demand := core.ScopeSQLRoutine(s)
			command := core.ScopeEffectCommand(s)
			// A reached nested command refreshes presentation and current
			// authority; those are not lexical restoration owners.
			scratch := core.ScopeSQLRoutineLexicalScratch(s)
			if mode == "cancel" { cancel() }
			err = core.ScopeDrainAfterTest(s)
			switch mode {
			case "null":
				if err != nil { t.Fatal(err) }
			case "error":
				var diagnostic *sqlstate.Error
				if !errors.As(err,&diagnostic) || diagnostic.Code != "42P01" { t.Fatal("expected real nested preparation failure",err) }
			case "cancel":
				if !errors.Is(err,context.Canceled) { t.Fatal("lost ordinary cancellation",err) }
			}
			if got := core.ScopeActiveSQLRoutineDepth(s); got != depth {
				t.Errorf("routine depth: got %d, want %d", got, depth)
			}
			if s.Op.EffectiveUser != actor {
				t.Errorf("effective actor: got %p, want %p", s.Op.EffectiveUser, actor)
			}
			if got := core.ScopeSQLRoutine(s); !core.SameSQLRoutineDemand(got, demand) {
				t.Errorf("routine demand: got %+v, want %+v", got, demand)
			}
			if got := core.ScopeEffectCommand(s); got != command {
				t.Errorf("effect command: got %d, want %d", got, command)
			}
			if got := core.ScopeSQLRoutineLexicalScratch(s); !reflect.DeepEqual(got, scratch) {
				t.Errorf("lexical scratch restoration mismatch:\n got: %+v\nwant: %+v", got, scratch)
			}
		})
	}
}
