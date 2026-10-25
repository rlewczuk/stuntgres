package engine

import (
	"errors"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
	"testing"
)

func plExceptionUnitScope(t *testing.T) (*Engine, *StatementScope) {
	t.Helper()
	e := New()
	sess := sessionStart(t, e, nil)
	op := transactionOperation(t, sess)
	ref, err := e.Begin(op, session.TxOptions{})
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	s, err := e.StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	s.TxRef = ref
	SetScopeWorked(s, true)
	t.Cleanup(func() { _ = s.Finish(errors.New("unit cleanup")); op.Done() })
	return e, s
}

func TestPLExceptionCheckpoint(t *testing.T) {
	for _, capacity := range []int{1, 4, 128} {
		t.Run(string(rune('a'+capacity)), func(t *testing.T) {
			e, s := plExceptionUnitScope(t)
			a := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90001}
			rows := make([]storage.RowVersion, 1, capacity)
			rows[0] = storage.RowVersion{Row: 1, Version: 1}
			s.Stage.Rows = map[ids.ObjectAddress][]storage.RowVersion{a: rows}
			s.Stage.Deletes = map[ids.ObjectAddress]*visibility.PrivateDeletes{a: {Rows: map[ids.RowID]visibility.DeleteCandidate{7: {Row: 7, Version: 7}}}}
			s.OwnWrites = map[ids.ObjectAddress][]storage.RowVersion{a: rows}
			s.OwnDeletes = s.Stage.Deletes
			text, _ := e.CatalogRead().LookupType(sqltype.Text)
			outerValue := sqltype.String(text, "outer")
			SetScopeForeignActionsSlot(s, &dml.ForeignActionScope{Admissions: map[ids.ObjectAddress]*dml.ForeignActionAdmission{a: {Jobs: []dml.ForeignKeyJob{{ChildKey: []sqltype.Value{outerValue}}}}}, Transitions: []dml.ForeignActionTransition{{Relation: a, Next: rows[0], Command: 1}}})
			SetScopeIndexesSlot(s, &index.Scope{ExistingOutputs: map[ids.ObjectAddress]map[index.RowIdentity]storage.IndexOutput{a: {{Row: 1, Version: 1}: {}}}, ExclusionModes: index.ExclusionModes{ByConstraint: map[ids.OID]bool{1: false}, Events: []index.ExclusionEvent{{Row: 1, Version: 1}}}})
			SetScopeNotificationEvents(s, []session.Notification{{}})
			s.Op.Session.Settings.Values["application_name"] = "outer"
			ref, command, frontier := s.TxRef, s.Owner.Command, e.Visibility.CommitSeq()
			rowFrontier, versionFrontier := e.Storage.RowFrontier(), e.Storage.VersionFrontier()
			mark, err := ScopeBeginPLException(s)
			if err != nil {
				t.Fatal(err)
			}
			if len(s.Owner.Savepoints) != 1 || !s.Owner.Savepoints[0].Internal {
				t.Fatal("internal mark not registered")
			}
			s.Stage.Rows[a] = append(s.Stage.Rows[a], storage.RowVersion{Row: 2, Version: 2})
			failedRows := s.Stage.Rows[a]
			s.Stage.Deletes[a].Rows[8] = visibility.DeleteCandidate{Row: 8, Version: 8}
			ScopeForeignActionsSlot(s).Admissions[a].Mutated = true
			ScopeForeignActionsSlot(s).Admissions[a].Jobs[0].ChildKey[0] = sqltype.String(text, "inner")
			ScopeForeignActionsSlot(s).Transitions[0].Checked = true
			ScopeForeignActionsSlot(s).Transitions = append(ScopeForeignActionsSlot(s).Transitions, dml.ForeignActionTransition{Command: 2})
			ScopeForeignActionsSlot(s).ExclusionAdded = true
			ScopeIndexesSlot(s).ExistingOutputs[a][index.RowIdentity{Row: 2, Version: 2}] = storage.IndexOutput{}
			ScopeIndexesSlot(s).ExclusionModes.ByConstraint[1] = true
			ScopeIndexesSlot(s).ExclusionModes.Events = append(ScopeIndexesSlot(s).ExclusionModes.Events, index.ExclusionEvent{Row: 2})
			SetScopeMultiEvents(s, append(ScopeMultiEvents(s), lock.MultiEvent{}))
			SetScopeNotificationEvents(s, append(ScopeNotificationEvents(s), session.Notification{}))
			s.Op.Session.Settings.Values["application_name"] = "inner"
			SetScopeEffectSerial(s, 9)
			inner, err := ScopeBeginPLException(s)
			if err != nil {
				t.Fatal(err)
			}
			s.Stage.Rows[a] = []storage.RowVersion{{Row: 1, Version: 11}, {Row: 3, Version: 3}}
			SetWriteStageReplacing(&s.Stage, true)
			if err := ScopeEndPLException(s, inner, false, nil); err != nil {
				t.Fatal(err)
			}
			if !WriteStageReplacing(&s.Stage) || s.Stage.Rows[a][0].Version != 11 {
				t.Fatal("successful inner release discarded effects")
			}
			if err := ScopeEndPLException(s, mark, true, errors.New("body")); err != nil {
				t.Fatal(err)
			}
			if len(s.Stage.Rows[a]) != 1 || s.Stage.Rows[a][0].Version != 1 || WriteStageReplacing(&s.Stage) || len(s.Stage.Deletes[a].Rows) != 1 || s.Stage.Deletes[a].Rows[7].Version != 7 {
				t.Fatal("stage prefix/deletion root not restored")
			}
			s.Stage.Rows[a] = append(s.Stage.Rows[a], storage.RowVersion{Row: 9, Version: 9})
			if len(failedRows) != 2 || failedRows[1].Version != 2 || rows[0].Version != 1 {
				t.Fatal("rollback/reappend overwrote retained roots")
			}
			if ScopeForeignActionsSlot(s).Admissions[a].Mutated || len(ScopeForeignActionsSlot(s).Transitions) != 1 || ScopeForeignActionsSlot(s).Transitions[0].Checked || !sqltype.SameDatum(ScopeForeignActionsSlot(s).Admissions[a].Jobs[0].ChildKey[0], outerValue) || ScopeForeignActionsSlot(s).ExclusionAdded {
				t.Fatal("mutable FK roots/progress not restored")
			}
			if len(ScopeIndexesSlot(s).ExistingOutputs[a]) != 1 || ScopeIndexesSlot(s).ExclusionModes.ByConstraint[1] || len(ScopeIndexesSlot(s).ExclusionModes.Events) != 1 || len(ScopeMultiEvents(s)) != 0 || len(ScopeNotificationEvents(s)) != 1 {
				t.Fatal("index/exclusion/notification suffix leaked")
			}
			if s.Op.Session.Settings.Values["application_name"] != "outer" || len(s.OwnWrites[a]) != 1 || len(s.OwnDeletes[a].Rows) != 1 {
				t.Fatal("settings/own read roots not restored")
			}
			if s.TxRef != ref || s.Owner.Command != command || e.Visibility.CommitSeq() != frontier || ScopeEffectSerial(s) != 9 || e.Storage.RowFrontier() != rowFrontier || e.Storage.VersionFrontier() != versionFrontier {
				t.Fatal("checkpoint rewound/advanced snapshot or identity")
			}
			if err := ScopeEndPLException(s, mark, true, nil); err == nil {
				t.Fatal("double disposition accepted")
			}
			next, err := ScopeBeginPLException(s)
			if err != nil {
				t.Fatal(err)
			}
			foreign := &StatementScope{Owner: s.Owner}
			if err := ScopeEndPLException(foreign, next, true, nil); err == nil {
				t.Fatal("foreign statement checkpoint accepted")
			}
			nested, err := ScopeBeginPLException(s)
			if err != nil {
				t.Fatal(err)
			}
			if err := ScopeEndPLException(s, next, true, nil); err == nil {
				t.Fatal("out-of-order checkpoint accepted")
			}
			if err := ScopeEndPLException(s, nested, true, nil); err != nil {
				t.Fatal(err)
			}
			if err := ScopeEndPLException(s, next, false, nil); err != nil {
				t.Fatal(err)
			}
			if len(s.Owner.Savepoints) != 0 {
				t.Fatal("internal mark leaked")
			}
		})
	}
}
