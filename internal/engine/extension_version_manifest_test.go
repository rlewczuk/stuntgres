package engine

import (
	"reflect"
	"slices"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"testing"
)

func extensionVersionUnitCreate(version string) *parser.CreateExtension {
	return &parser.CreateExtension{Name: parser.Identifier{Text: "fuzzystrmatch"}, Options: []parser.ExtensionOption{{Name: "version", Value: parser.Identifier{Text: version}}}}
}
func extensionVersionUnitUpdate() *parser.AlterExtensionUpdate {
	return &parser.AlterExtensionUpdate{Name: parser.Identifier{Text: "fuzzystrmatch"}, Options: []parser.ExtensionOption{{Name: "version", Value: parser.Identifier{Text: "1.2"}}}}
}
func extensionVersionUnitInstalled(t *testing.T, version string) (*Engine, *session.Operation, *catalog.Extension) {
	t.Helper()
	e := New()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	if err := extensionUnitExecute(t, e, op, extensionVersionUnitCreate(version)); err != nil {
		t.Fatal(err)
	}
	ext := e.CatalogRead().MustExtension(e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "fuzzystrmatch"}))
	return e, op, ext
}
func extensionVersionUnitRevoke(t *testing.T, e *Engine, op *session.Operation) {
	t.Helper()
	s := &parser.ObjectGrant{Kind: "function", Revoke: true, Privileges: []parser.GrantPrivilege{{Name: "execute"}}, Grantees: []parser.GrantGrantee{{Public: true}}, Routines: []parser.GrantRoutineTarget{{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "public"}, {Text: "levenshtein"}}}, SignatureSpecified: true, Arguments: []parser.GrantRoutineArgument{{Type: parser.TypeName{Name: "text"}}, {Type: parser.TypeName{Name: "text"}}}}}}
	if err := extensionUnitExecute(t, e, op, s); err != nil {
		t.Fatal(err)
	}
}

type extensionVersionMemberSnapshot struct {
	procedure *catalog.Procedure
	copy      catalog.Procedure
	acl       authorization.ACLValue
}

func extensionVersionUnitMembers(t *testing.T, view catalog.Read, ext *catalog.Extension) map[ids.ObjectAddress]extensionVersionMemberSnapshot {
	t.Helper()
	result := make(map[ids.ObjectAddress]extensionVersionMemberSnapshot, len(ext.Members))
	for _, address := range ext.Members {
		p := view.MustProcedure(address)
		acl := p.ACL
		acl.Entries = slices.Clone(acl.Entries)
		result[address] = extensionVersionMemberSnapshot{p, *p, acl}
	}
	return result
}
func extensionVersionUnitUnchanged(t *testing.T, view catalog.Read, members map[ids.ObjectAddress]extensionVersionMemberSnapshot) {
	t.Helper()
	for address, old := range members {
		if view.Get(address) != old.procedure || !reflect.DeepEqual(*old.procedure, old.copy) || !authorization.EqualACL(old.procedure.ACL, old.acl) {
			t.Fatal("old descriptor/provider/ACL changed", address)
		}
	}
}

func TestExtensionVersionManifest(t *testing.T) {
	e, op, old := extensionVersionUnitInstalled(t, "1.1")
	if len(old.Members) != 9 || old.Version != "1.1" {
		t.Fatal("base manifest", old)
	}
	extensionVersionUnitRevoke(t, e, op)
	view := e.CatalogRead()
	members := extensionVersionUnitMembers(t, view, old)
	originalMembers := slices.Clone(old.Members)
	for _, member := range members {
		if member.procedure.Name() == "daitch_mokotoff" || member.procedure.Implementation != extension.StockFuzzystrmatch {
			t.Fatal("base contains update member or foreign provider")
		}
	}
	e.mu.Lock()
	stage, err := e.stageExtensionUpdateLocked(&StatementScope{op: op}, extensionVersionUnitUpdate())
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(stage)
	candidate := stage.Get(old.Address()).(*catalog.Extension)
	if candidate == old || candidate.Version != "1.2" || len(candidate.Members) != 10 || candidate.Owner != old.Owner || candidate.TargetNamespace != old.TargetNamespace || candidate.Revision() == old.Revision() {
		t.Fatal("genuine detached update manifest", candidate)
	}
	if old.Version != "1.1" || !reflect.DeepEqual(old.Members, originalMembers) {
		t.Fatal("private stage mutated predecessor")
	}
	extensionVersionUnitUnchanged(t, e.CatalogRead(), members)
	staged := e.stageRead(stage)
	if err := extension.StockFuzzystrmatch.ValidateExtension(staged, candidate); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"1.0", "missing", "1.1"} {
		bad := *candidate
		bad.Version = version
		if err := (extension.CatalogProvider{ids.ExtensionClassID}).Validate(staged, &bad); err == nil {
			t.Fatal("wrong-version manifest admitted", version)
		}
	}
	for _, addresses := range [][]ids.ObjectAddress{nil, candidate.Members[:9], append(slices.Clone(candidate.Members), candidate.Members[0]), append([]ids.ObjectAddress{candidate.Members[0]}, candidate.Members[:9]...)} {
		bad := *candidate
		bad.Members = addresses
		if err := extension.StockFuzzystrmatch.ValidateExtension(staged, &bad); err == nil {
			t.Fatal("missing/extra/duplicate manifest admitted")
		}
	}
	// Same count as a base, but containing genuine DM: version/count checks
	// alone cannot substitute for the exact released selected manifest.
	badBase := *candidate
	badBase.Version, badBase.Members = "1.1", candidate.Members[:9]
	if err := extension.StockFuzzystrmatch.ValidateExtension(staged, &badBase); err == nil {
		t.Fatal("DM in base manifest admitted")
	}
	for _, mutate := range []func(*catalog.Procedure){
		func(p *catalog.Procedure) { p.Implementation = extension.StockUnaccent },
		func(p *catalog.Procedure) { p.Owner = New().roles.domain.Principal(10) },
		func(p *catalog.Procedure) { p.Owner = authorization.PrincipalRef{} },
		func(p *catalog.Procedure) { p.Signature = "23" },
	} {
		state := catalog.CloneState(staged.State())
		proc := *staged.MustProcedure(candidate.Members[0])
		mutate(&proc)
		state.SetDescriptor(proc.Address(), &proc)
		if err := extension.StockFuzzystrmatch.ValidateExtension(e.catalog.read(state), candidate); err == nil {
			t.Fatal("foreign member manifest admitted")
		}
	}
	if err := extensionUnitExecute(t, e, op, extensionVersionUnitUpdate()); err != nil {
		t.Fatal(err)
	}
	installed := e.CatalogRead().MustExtension(old.Address())
	if installed.Version != "1.2" || installed == old || old.Version != "1.1" || !reflect.DeepEqual(old.Members, originalMembers) {
		t.Fatal("successful update mutated predecessor")
	}
	extensionVersionUnitUnchanged(t, e.CatalogRead(), members)
	for _, version := range []string{"1.1", "1.2"} {
		t.Run("Remap"+version, func(t *testing.T) {
			source, _, ext := extensionVersionUnitInstalled(t, version)
			before := source.CatalogRead()
			mapping := map[ids.ObjectAddress]ids.ObjectAddress{ext.Address(): {ClassID: ids.ExtensionClassID, ObjectID: 950000}}
			for i, a := range ext.Members {
				mapping[a] = ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(950001 + i)}
			}
			state := catalog.CloneState(before.State())
			for address := range mapping {
				state.UnbindName(catalog.DescriptorNameKey(before.Get(address)))
				state.RemoveDescriptor(address)
			}
			for address, destination := range mapping {
				d := extension.RemapDescriptor(before.Get(address), mapping)
				state.SetDescriptor(destination, d)
				state.BindName(catalog.DescriptorNameKey(d), destination)
			}
			remapped := state.MustExtension(mapping[ext.Address()])
			view := source.catalog.read(state)
			if remapped.Version != version || remapped.Owner != ext.Owner || remapped.TargetNamespace != ext.TargetNamespace || extension.StockFuzzystrmatch.ValidateExtension(view, remapped) != nil {
				t.Fatal("version remap changed logical manifest")
			}
			for _, a := range ext.Members {
				old, next := before.MustProcedure(a), view.MustProcedure(mapping[a])
				if next.Implementation != old.Implementation || next.Extension != remapped.Address() || next.Owner != old.Owner || next.Signature != old.Signature || !authorization.EqualACL(next.ACL, old.ACL) || before.Get(a) != old {
					t.Fatal("member remap lost genuine identity")
				}
			}
		})
	}
}
