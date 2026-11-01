package control_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

func TestTriggerDeferredReplacementIdentities(t *testing.T) {
	e := engine.New()
	seed := e.CatalogRead()
	var builtin *catalog.Procedure
	for _, descriptor := range seed.Descriptors() {
		if p, ok := descriptor.(*catalog.Procedure); ok && p.Name() == "suppress_redundant_updates_trigger" {
			builtin = p
			break
		}
	}
	if builtin == nil {
		t.Fatal("missing genuine builtin trigger capability")
	}
	state := catalog.CloneState(seed.State())
	address := func(class, oid ids.OID) ids.ObjectAddress { return ids.ObjectAddress{ClassID: class, ObjectID: oid} }
	temporary := &catalog.Namespace{ObjectAddress: address(ids.NamespaceClassID, 9100001), ObjectName: "pg_temp_fixture", Temporary: seed.Owner().Domain.Session(1)}
	state.SetDescriptor(temporary.Address(), temporary)
	persistent := &catalog.Namespace{ObjectAddress: address(ids.NamespaceClassID, 9100002), ObjectName: "persistent_fixture"}
	state.SetDescriptor(persistent.Address(), persistent)
	view := seed.WithState(state)
	makeOwner := func(oid ids.OID, ns *catalog.Namespace) *catalog.Relation {
		d := &catalog.Relation{ObjectAddress: address(ids.RelationClassID, oid), ObjectNamespace: ns.Address(), ObjectName: "t", ObjectRevision: 1, RelationKind: catalog.TableRelation}
		function := builtin.Address()
		p := stored.TriggerParts{OID: 9100010, Name: "tr", Relation: view.Owner().Domain.Ref(view.Epoch(), d.Address()), Procedure: view.Owner().Domain.Ref(view.Epoch(), function), Timing: stored.TriggerAfter, Level: stored.TriggerRow, Events: stored.TriggerInsert, References: []ids.ObjectAddress{function}, Constraint: catalog.TriggerConstraint{OID: 9100011, Name: "c", Deferrable: true, InitiallyDeferred: true}}
		definition, err := stored.NewTrigger(p)
		if err != nil {
			t.Fatal(err)
		}
		d.Triggers = stored.TriggerList{definition}
		state.SetDescriptor(d.Address(), d)
		return d
	}
	temp, imported := makeOwner(9100003, temporary), makeOwner(9100004, persistent)
	oldTemp, oldImported := stored.TriggersOf(temp)[0], stored.TriggersOf(imported)[0]
	nextOID := ids.OID(9200000)
	if err := control.RemapDefinitionOIDs(view, func() (ids.OID, error) { nextOID++; return nextOID, nil }); err != nil {
		t.Fatal(err)
	}
	tempNow := stored.TriggersOf(view.MustRelation(temp.Address()))[0]
	importedNow := stored.TriggersOf(view.MustRelation(imported.Address()))[0]
	if !oldTemp.SameExecutable(tempNow) {
		t.Fatal("replacement reminted retained TEMP definition")
	}
	before, after := oldImported.Parts(), importedNow.Parts()
	if after.OID == before.OID || after.Constraint.OID == before.Constraint.OID || after.OID == after.Constraint.OID || after.Constraint.Name != before.Constraint.Name || !after.Constraint.Deferrable || !after.Constraint.InitiallyDeferred {
		t.Fatal("replacement lost distinct reminted identities or declarations", before, after)
	}
	if oldImported.Parts().OID != before.OID || oldImported.Parts().Constraint != before.Constraint {
		t.Fatal("remint mutated source snapshot")
	}
	if !catalog.DefinitionOIDUsed(view.MustRelation(imported.Address()), after.Constraint.OID) {
		t.Fatal("replacement constraint missing identity inventory")
	}
}

func TestTriggerDeferredRestoreAdmission(t *testing.T) {
	target := func(schema string) parser.ObjectName {
		return parser.ObjectName{Parts: []parser.Identifier{{Text: schema}, {Text: "t"}}}
	}
	constraint := parser.AlterTableRenameConstraint{
		Name: target("public"), Constraint: parser.Identifier{Text: "temporary"},
		NewName: parser.Identifier{Text: "actual"},
	}
	answer, err := control.AdmitRelationIdentity(struct{}{}, nil, &constraint)
	if err != nil || answer != control.Admitted {
		t.Fatal("constraint rename did not reach ordinary staged execution", answer, err)
	}
	for name, mutate := range map[string]func(*parser.AlterTableRenameConstraint){
		"conditional":        func(s *parser.AlterTableRenameConstraint) { s.IfExists = true },
		"only":               func(s *parser.AlterTableRenameConstraint) { s.Only = true },
		"unqualified":        func(s *parser.AlterTableRenameConstraint) { s.Name.Parts = s.Name.Parts[1:] },
		"temporary alias":    func(s *parser.AlterTableRenameConstraint) { s.Name = target("pg_temp") },
		"temporary physical": func(s *parser.AlterTableRenameConstraint) { s.Name = target("pg_temp_42") },
		"missing old":        func(s *parser.AlterTableRenameConstraint) { s.Constraint.Text = "" },
		"missing new":        func(s *parser.AlterTableRenameConstraint) { s.NewName.Text = "" },
	} {
		t.Run("constraint/"+name, func(t *testing.T) {
			s := constraint
			mutate(&s)
			if answer, err := control.AdmitRelationIdentity(struct{}{}, nil, &s); err == nil || answer != control.NotHandled {
				t.Fatal("unsupported constraint rename admitted", answer, err)
			}
		})
	}
	trigger := parser.AlterTrigger{
		Table: target("public"), Name: parser.Identifier{Text: "temporary"},
		Target: parser.Identifier{Text: "actual"}, Action: "rename",
	}
	admitTrigger := control.AdmitStatement[struct{}](control.CheckAlterTrigger)
	if answer, err := admitTrigger(struct{}{}, nil, &trigger); err != nil || answer != control.Admitted {
		t.Fatal("trigger rename did not reach ordinary staged execution", answer, err)
	}
	for name, mutate := range map[string]func(*parser.AlterTrigger){
		"negative dependency": func(s *parser.AlterTrigger) { s.Action = "no_depends" },
		"unknown action":      func(s *parser.AlterTrigger) { s.Action = "disable" },
		"unqualified":         func(s *parser.AlterTrigger) { s.Table.Parts = s.Table.Parts[1:] },
		"temporary alias":     func(s *parser.AlterTrigger) { s.Table = target("pg_temp") },
		"temporary physical":  func(s *parser.AlterTrigger) { s.Table = target("pg_temp_42") },
		"missing old":         func(s *parser.AlterTrigger) { s.Name.Text = "" },
		"missing new":         func(s *parser.AlterTrigger) { s.Target.Text = "" },
	} {
		t.Run("trigger/"+name, func(t *testing.T) {
			s := trigger
			mutate(&s)
			if answer, err := admitTrigger(struct{}{}, nil, &s); err == nil || answer != control.NotHandled {
				t.Fatal("unsupported trigger lifecycle admitted", answer, err)
			}
		})
	}
	dependency := trigger
	dependency.Action = "depends"
	if answer, err := admitTrigger(struct{}{}, nil, &dependency); err != nil || answer != control.Admitted {
		t.Fatal("positive extension metadata admission changed", answer, err)
	}
	other := &parser.AlterTable{Name: target("public"), Actions: []parser.AlterTableAction{{Action: parser.DropConstraint, Constraint: constraint.Constraint}}}
	if answer, err := control.AdmitRelationIdentity(struct{}{}, nil, other); err != nil || answer != control.NotHandled {
		t.Fatal("constraint rename admitted generic ALTER TABLE", answer, err)
	}
	if answer, err := admitTrigger(struct{}{}, nil, other); err != nil || answer != control.NotHandled {
		t.Fatal("trigger rename admitted generic ALTER TABLE", answer, err)
	}
}
