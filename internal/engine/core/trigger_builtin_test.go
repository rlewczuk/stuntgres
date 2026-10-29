package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestTriggerBuiltinsCapability(t *testing.T) {
	e, s, read, relation, pl := triggerUnitFixture(t, "BEGIN RETURN NEW; END")
	if !catalog.SupportsDMLTrigger(pl) || catalog.BuiltinDMLTrigger(pl) != 0 {
		t.Fatal("PL capability changed")
	}
	for _, oid := range []ids.OID{1291, 3752, 3753} {
		d := read.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid})
		if catalog.BuiltinDMLTrigger(d) != oid || !catalog.SupportsDMLTrigger(d) {
			t.Fatal("builtin capability", oid)
		}
		if err := (stored.ProcedureProvider{}).Validate(read, d); err != nil {
			t.Fatal(err)
		}
		definition := triggerUnitDefinition(t, read, relation, d, "builtin", stored.TriggerInsert)
		installed := triggerUnitInstall(t, e, relation, definition)
		if err := installed.Triggers.Validate(e.CatalogRead(), installed); err != nil {
			t.Fatal("stored builtin rejected", err)
		}
		for _, change := range []func(*catalog.Procedure){
			func(d *catalog.Procedure) { d.Builtin = nil },
			func(d *catalog.Procedure) { d.Signature = "25" },
			func(d *catalog.Procedure) { d.RoutineKind = catalog.ProcedureRoutine },
			func(d *catalog.Procedure) { d.Result = 23 },
		} {
			bad := *d
			change(&bad)
			if catalog.SupportsDMLTrigger(&bad) {
				t.Fatal("invalid capability admitted", bad)
			}
		}
		forged := *d
		copyDeclaration := *d.Builtin
		forged.Builtin = &copyDeclaration
		if err := (stored.ProcedureProvider{}).Validate(read, &forged); err == nil {
			t.Fatal("copied declaration granted provider authority")
		}
		e.Mu.Lock()
		auth, err := e.AuthorizationLocked(s.Op)
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := exec.ValidateRoutineDescriptor(auth.Catalog, auth.Roles, &forged); err == nil {
			t.Fatal("forged live carrier admitted")
		}
		_, _, foreignRead, _, _ := triggerUnitFixture(t, "BEGIN RETURN NEW; END")
		foreignBuiltin := foreignRead.MustProcedure(d.Address())
		if _, err := exec.ValidateRoutineDescriptor(auth.Catalog, auth.Roles, foreignBuiltin); err == nil {
			t.Fatal("foreign principal carrier admitted")
		}
	}
}

func TestTriggerBuiltinsPayload(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		oldNull, newNull, changed bool
	}{
		{"same", false, false, false}, {"different", false, false, true},
		{"nulls", true, true, false}, {"to_null", false, true, true}, {"from_null", true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, s, read, relation, _ := triggerUnitFixture(t, "BEGIN RETURN NEW; END")
			d := read.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 1291})
			definition := triggerUnitDefinition(t, read, relation, d, "suppress", stored.TriggerUpdate)
			relation = triggerUnitInstall(t, e, relation, definition)
			triggerUnitAdmit(t, s, relation)
			integer, _ := read.LookupType(sqltype.Int4)
			old := []sqltype.Value{triggerUnitInt(t, integer, 1), triggerUnitInt(t, integer, 5)}
			next := []sqltype.Value{old[0], old[1]}
			if tc.oldNull {
				old[1] = sqltype.Null(integer)
			}
			if tc.newNull {
				next[1] = sqltype.Null(integer)
			} else if tc.changed && !tc.oldNull {
				next[1] = triggerUnitInt(t, integer, 6)
			}
			ref := read.Owner().Domain.Ref(read.Epoch(), relation.Address())
			result, err := s.WriteHost().BeforeRow(dml.BeforeRow{Relation: ref, Event: dml.BeforeUpdate, Old: old, New: next})
			if err != nil || result.Proceed != tc.changed {
				t.Fatal("payload decision", result.Proceed, err)
			}
			if &result.Row[0] != &next[0] {
				t.Fatal("builtin copied producer row")
			}
			if n, _ := old[0].Integer(); n != 1 {
				t.Fatal("OLD mutated")
			}
			if core.ScopeActiveSQLRoutineDepth(s) != 0 {
				t.Fatal("routine depth leaked")
			}
		})
	}
}

func TestTriggerBuiltinsInvocation(t *testing.T) {
	t.Run("tuple_ownership", triggerBuiltinTupleOwnership)
	for _, tc := range []struct {
		name             string
		oid              ids.OID
		after, statement bool
		code, message    string
	}{
		{"suppress_insert", 1291, false, false, "39P01", "suppress_redundant_updates_trigger: must be called on update"},
		{"vector_args", 3752, false, false, "XX000", "tsvector_update_trigger: arguments must be tsvector_field, ts_config, text_field1, ...)"},
		{"column_args", 3753, false, false, "XX000", "tsvector_update_trigger: arguments must be tsvector_field, ts_config, text_field1, ...)"},
		{"vector_after", 3752, true, false, "XX000", "tsvector_update_trigger: must be fired BEFORE event"},
		{"vector_statement", 3753, true, true, "XX000", "tsvector_update_trigger: must be fired for row"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, s, read, relation, _ := triggerUnitFixture(t, "BEGIN RETURN NEW; END")
			d := read.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: tc.oid})
			parts := triggerUnitDefinition(t, read, relation, d, "builtin", stored.TriggerInsert).Parts()
			if tc.after {
				parts.Timing = stored.TriggerAfter
			}
			if tc.statement {
				parts.Level = stored.TriggerStatement
			}
			definition, err := stored.NewTrigger(parts)
			if err != nil {
				t.Fatal(err)
			}
			relation = triggerUnitInstall(t, e, relation, definition)
			triggerUnitAdmit(t, s, relation)
			integer, _ := read.LookupType(sqltype.Int4)
			ref := read.Owner().Domain.Ref(read.Epoch(), relation.Address())
			row := []sqltype.Value{triggerUnitInt(t, integer, 1), triggerUnitInt(t, integer, 5)}
			h := s.WriteHost()
			h.EnterForeignKeyTrigger()
			defer h.LeaveForeignKeyTrigger()
			if tc.statement {
				err = h.AfterStatement(ref, stored.TriggerInsert, nil)
				if err == nil {
					err = h.CompleteAfterStatement(ref, stored.TriggerInsert)
				}
			} else if tc.after {
				err = h.AfterRow(dml.BeforeRow{Relation: ref, Event: dml.BeforeInsert, New: row})
			} else {
				_, err = h.BeforeRow(dml.BeforeRow{Relation: ref, Event: dml.BeforeInsert, New: row})
			}
			if tc.after && err == nil {
				err = core.ScopeDrainAfterTest(s)
			}
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Message != tc.message {
				t.Fatal("protocol diagnostic", err)
			}
			depth, err := h.TriggerDepthValue(integer)
			if n, _ := depth.Integer(); err != nil || n != 1 {
				t.Fatal("body did not unwind to surrounding RI", n, err)
			}
			if core.ScopeActiveSQLRoutineDepth(s) != 0 {
				t.Fatal("routine depth leaked")
			}
		})
	}
}

func TestTriggerBuiltinsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e, s, read, relation, _ := triggerUnitFixtureContext(t, ctx, "BEGIN RETURN NEW; END")
	d := read.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 1291})
	relation = triggerUnitInstall(t, e, relation, triggerUnitDefinition(t, read, relation, d, "suppress", stored.TriggerUpdate))
	triggerUnitAdmit(t, s, relation)
	integer, _ := read.LookupType(sqltype.Int4)
	row := []sqltype.Value{triggerUnitInt(t, integer, 1), triggerUnitInt(t, integer, 5)}
	ref := read.Owner().Domain.Ref(read.Epoch(), relation.Address())
	cancel()
	_, err := s.WriteHost().BeforeRow(dml.BeforeRow{Relation: ref, Event: dml.BeforeUpdate, Old: row, New: row})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation admitted suppression", err)
	}
	depth, err := s.WriteHost().TriggerDepthValue(integer)
	if n, _ := depth.Integer(); err != nil || n != 0 || core.ScopeActiveSQLRoutineDepth(s) != 0 {
		t.Fatal("canceled entry leaked depth", n, err)
	}
	if n, _ := row[1].Integer(); n != 5 {
		t.Fatal("canceled invocation changed row")
	}
}

func triggerBuiltinTupleOwnership(t *testing.T) {
	e, s, read, relation, _ := triggerUnitFixture(t, "BEGIN RETURN NEW; END")
	text, _ := read.LookupType(sqltype.Text)
	vector, _ := read.LookupType(sqltype.TSVector)
	domain, err := sqltype.Domain(9500020, "trigger_vector", "public.trigger_vector", vector.Usage(), true)
	if err != nil {
		t.Fatal(err)
	}
	domainType := &catalog.Type{SQLType: domain, ObjectNamespace: relation.Namespace(), ObjectRevision: 1, Owner: relation.Owner}
	columns := []catalog.Column{{Name: "title", Usage: text.Usage(), Nullable: true}, {Name: "v", Usage: domain.Usage(), Nullable: true}}
	shape, err := sqltype.Composite(relation.RowType, relation.Name(), "public."+relation.Name(), 0, []sqltype.CompositeMember{
		{Attribute: 1, Name: "title", Usage: text.Usage()},
		{Attribute: 2, Name: "v", Usage: domain.Usage()},
	})
	if err != nil {
		t.Fatal(err)
	}
	copy := *relation
	copy.Columns, copy.Index = columns, map[string]int{"title": 0, "v": 1}
	rowtype := &catalog.Type{SQLType: shape, ObjectNamespace: relation.Namespace(), ObjectRevision: 1, Owner: relation.Owner, OwningRelation: relation.Address()}
	e.Mu.Lock()
	state := catalog.CloneState(e.Catalog.State)
	state.SetDescriptor(domainType.Address(), domainType)
	state.BindName(catalog.DescriptorNameKey(domainType), domainType.Address())
	state.SetDescriptor(rowtype.Address(), rowtype)
	e.Catalog.PublishLocked(state)
	e.Mu.Unlock()
	read = e.CatalogRead()
	d := read.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3752})
	parts := triggerUnitDefinition(t, read, &copy, d, "vector", stored.TriggerInsert).Parts()
	parts.Arguments = []string{"v", "pg_catalog.simple", "title"}
	definition, err := stored.NewTrigger(parts)
	if err != nil {
		t.Fatal(err)
	}
	relation = triggerUnitInstall(t, e, &copy, definition)
	triggerUnitAdmit(t, s, relation)
	ref := read.Owner().Domain.Ref(read.Epoch(), relation.Address())
	row := []sqltype.Value{sqltype.String(text, "Rats"), sqltype.Null(domain)}
	result, err := s.WriteHost().BeforeRow(dml.BeforeRow{Relation: ref, Event: dml.BeforeInsert, New: row})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Proceed || &result.Row[0] != &row[0] || result.Row[1].Type() != domain || !result.Row[1].ValidResult(domain.Usage()) || result.Row[1].IsNull() {
		t.Fatal("binary replacement lost row or destination authority")
	}
	base, err := result.Row[1].DomainValue(vector)
	if err != nil || !base.ValidResult(vector.Usage()) || row[0].Type() != text {
		t.Fatal("replacement lost canonical vector carrier", err)
	}
}
