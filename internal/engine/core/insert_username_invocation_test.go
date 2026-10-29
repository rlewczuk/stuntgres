package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestInsertUsernameInvocation(t *testing.T) {
	for _, tc := range []struct { name, arg string; cancel, update, domain, noArgs, twoArgs, definer bool; code, message string }{
		{name: "insert", arg: "actor"},
		{name: "update", arg: "actor", update: true},
		{name: "missing", arg: "missing", code: "09000", message: `"trigger_unit_table" has no attribute "missing"`},
		{name: "system", arg: "ctid", code: "09000", message: `"trigger_unit_table" has no attribute "ctid"`},
		{name: "integer", arg: "id", code: "09000", message: `attribute "id" of "trigger_unit_table" must be type TEXT`},
		{name: "text_domain", arg: "actor", domain: true, code: "09000", message: `attribute "actor" of "trigger_unit_table" must be type TEXT`},
		{name: "no_arguments", noArgs: true, code: "XX000", message: "insert_username (trigger_unit_table): one argument was expected"},
		{name: "two_arguments", arg: "missing", twoArgs: true, code: "XX000", message: "insert_username (trigger_unit_table): one argument was expected"},
		{name: "cancel", arg: "actor", cancel: true},
		{name: "definer_success", arg: "actor", definer: true},
		{name: "definer_error", arg: "missing", definer: true, code: "09000", message: `"trigger_unit_table" has no attribute "missing"`},
		{name: "definer_cancellation", arg: "actor", definer: true, cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			e, s, _, relation, _ := triggerUnitFixtureContext(t, ctx, "BEGIN RETURN NEW; END")
			op, done := enginetest.RoleOperation(t, e, "postgres")
			enginetest.RoleExecute(t, e, op, "CREATE EXTENSION insert_username")
			if tc.definer {
				for _, q := range []string{"CREATE ROLE iu_native_owner", "ALTER FUNCTION insert_username() OWNER TO iu_native_owner", "ALTER FUNCTION insert_username() SECURITY DEFINER", "ALTER FUNCTION insert_username() SET search_path=pg_catalog"} {
					enginetest.RoleExecute(t, e, op, q)
				}
			}
			// This low-level fixture bypasses normal query identity capture.
			// Capture the real caller before invoking an invoker native body.
			e.Mu.Lock()
			caller, captureErr := s.RoutineAuthorizationLocked()
			if captureErr == nil {
				captureErr = s.CaptureIdentityLocked(caller)
			}
			e.Mu.Unlock()
			if captureErr != nil { t.Fatal(captureErr) }
			previousActor := s.Op.EffectiveUser
			previousPath, _ := s.Op.Setting("search_path")
			done()
			read := e.CatalogRead()
			ext := read.MustExtension(read.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "insert_username"}))
			d := read.MustProcedure(ext.Members[0])
			integer, _ := read.LookupType(sqltype.Int4)
			text, _ := read.LookupType(sqltype.Text)
			if tc.domain {
				domain, err := sqltype.Domain(9500020, "iu_domain", "public.iu_domain", text.Usage(), true)
				if err != nil { t.Fatal(err) }
				text = domain
			}
			shape, err := sqltype.Composite(relation.RowType, relation.Name(), "public."+relation.Name(), 0, []sqltype.CompositeMember{{Attribute: 1, Name: "id", Usage: integer.Usage()}, {Attribute: 2, Name: "actor", Usage: text.Usage()}})
			if err != nil { t.Fatal(err) }
			changed := *relation
			changed.Columns = []catalog.Column{{Name: "id", Usage: integer.Usage(), Nullable: true}, {Name: "actor", Usage: text.Usage(), Nullable: true}}
			changed.Index = map[string]int{"id": 0, "actor": 1}
			rowtype := &catalog.Type{SQLType: shape, ObjectNamespace: relation.Namespace(), ObjectRevision: 1, Owner: relation.Owner, OwningRelation: relation.Address()}
			e.Mu.Lock()
			state := catalog.CloneState(e.Catalog.State)
			if tc.domain {
				domainType := &catalog.Type{SQLType: text, ObjectNamespace: relation.Namespace(), ObjectRevision: 1, Owner: relation.Owner}
				state.SetDescriptor(domainType.Address(), domainType)
				state.BindName(catalog.DescriptorNameKey(domainType), domainType.Address())
			}
			state.SetDescriptor(rowtype.Address(), rowtype)
			e.Catalog.PublishLocked(state)
			e.Mu.Unlock()
			parts := triggerUnitDefinition(t, read, &changed, d, "username", stored.TriggerInsert|stored.TriggerUpdate).Parts()
			parts.Arguments = []string{tc.arg}
			if tc.noArgs { parts.Arguments = nil }
			if tc.twoArgs { parts.Arguments = []string{tc.arg, "actor"} }
			definition, err := stored.NewTrigger(parts)
			if err != nil { t.Fatal(err) }
			relation = triggerUnitInstall(t, e, &changed, definition)
			triggerUnitAdmit(t, s, relation)
			oldActor := sqltype.Null(text)
			if !tc.domain { oldActor = sqltype.String(text, "old") }
			old := []sqltype.Value{triggerUnitInt(t, integer, 1), oldActor}
			next := []sqltype.Value{old[0], sqltype.Null(text)}
			input := dml.BeforeRow{Relation: read.Owner().Domain.Ref(read.Epoch(), relation.Address()), Event: dml.BeforeInsert, New: next}
			if tc.update { input.Event = dml.BeforeUpdate; input.Old = old }
			if tc.cancel { cancel() }
			result, err := s.WriteHost().BeforeRow(input)
			switch {
			case tc.cancel:
				if !errors.Is(err, context.Canceled) { t.Fatal("canceled admission", err) }
			case tc.code != "":
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Message != tc.message { t.Fatal("native diagnostic", err) }
			default:
				if err != nil { t.Fatal(err) }
				if !result.Proceed || len(result.Row) != 2 || &result.Row[0] != &next[0] || result.Row[1].IsNull() || result.Row[1].Type() != text || !result.Row[1].ValidResult(text.Usage()) { t.Fatal("replacement lost producer backing or text authority") }
				want := "postgres"
				if tc.definer { want = "iu_native_owner" }
				if result.Row[1] != sqltype.String(text, want) { t.Fatal("effective identity", result.Row[1]) }
			}
			if old[1] != oldActor || next[0] != old[0] { t.Fatal("unrelated/OLD values changed") }
			if (tc.cancel || tc.code != "") && !next[1].IsNull() { t.Fatal("failed body changed producer row") }
			depth, err := s.WriteHost().TriggerDepthValue(integer)
			if n, _ := depth.Integer(); err != nil || n != 0 || core.ScopeActiveSQLRoutineDepth(s) != 0 { t.Fatal("invocation leaked depth", n, err) }
			path, _ := s.Op.Setting("search_path")
			if s.Op.EffectiveUser != previousActor || path != previousPath { t.Fatal("native body leaked actor/configuration", path, previousPath) }
		})
	}
}
