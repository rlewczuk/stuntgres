package schemacmds_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func tablefuncUnitInstalled(t *testing.T, e *core.Engine) *catalog.Extension {
	t.Helper()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	e.Mu.Lock()
	stage, err := schemacmds.StageExtensionLocked(e, &core.StatementScope{Op: op}, &parser.CreateExtension{Name: parser.Identifier{Text: "tablefunc"}})
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	extensionUnitPublish(t, e, op, stage)
	view := e.CatalogRead()
	return view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "tablefunc"}))
}

func TestTablefuncCrosstabProviderIdentity(t *testing.T) {
	e := engine.New()
	ext := tablefuncUnitInstalled(t, e)
	view := e.CatalogRead()
	control := extension.StockTablefunc.Control()
	if control.Version != "1.0" || !control.Trusted || !control.Superuser || !control.Relocatable || control.Comment == nil || *control.Comment != "functions that manipulate whole tables, including crosstab" {
		t.Fatal(control)
	}
	*control.Comment = "mutated"
	if *extension.StockTablefunc.Control().Comment == "mutated" {
		t.Fatal("shared control comment")
	}
	if err := extension.StockTablefunc.ValidateExtension(view, ext); err != nil {
		t.Fatal(err)
	}
	if len(ext.Members) != 16 {
		t.Fatal("member inventory", ext.Members)
	}
	for _, slot := range []int{5, 12, 13, 14, 15} {
		p := view.MustProcedure(ext.Members[slot])
		if err := (extension.CatalogProvider{Class: ids.ProcedureClassID}).Validate(view, p); err != nil {
			t.Fatal("shared installed procedure validation", slot, err)
		}
		symbol, err := extension.ProcedureSymbol(view, p)
		expected := "crosstab"
		if slot >= 12 && slot <= 14 {
			expected = fmt.Sprintf("crosstab%d", slot-10)
		}
		if err != nil || symbol != expected || p.Volatility != expr.Stable || !p.Strict || !p.Installed.ReturnsSet || p.Installed.Parallel != 'u' || p.Owner != e.RoleStore.Domain.Principal(10) {
			t.Fatal("procedure declaration", slot, p, symbol, err)
		}
		for _, mutate := range []func(*catalog.Procedure){func(p *catalog.Procedure) { p.Strict = false }, func(p *catalog.Procedure) { p.Result = 25 }, func(p *catalog.Procedure) { p.Signature = "25,25" }, func(p *catalog.Procedure) { p.ObjectRevision = 0 }, func(p *catalog.Procedure) { p.Implementation = new(extension.TablefuncProvider) }, func(p *catalog.Procedure) { p.Installed.Rows = 1 }, func(p *catalog.Procedure) { p.Installed.Parallel = 's' }, func(p *catalog.Procedure) { p.Installed.Parallel = 'r' }, func(p *catalog.Procedure) { p.Installed.Parallel = 0 }, func(p *catalog.Procedure) { p.Display.Source = "crosstab_hash" }} {
			state := catalog.CloneState(view.State())
			bad := *p
			mutate(&bad)
			state.SetDescriptor(p.Address(), &bad)
			if extension.StockTablefunc.ValidateExtension(e.Catalog.Read(state), ext) == nil {
				t.Fatal("forged procedure accepted", slot, bad)
			}
			if (extension.CatalogProvider{Class: ids.ProcedureClassID}).Validate(e.Catalog.Read(state), &bad) == nil {
				t.Fatal("shared validator accepted forged procedure", slot, bad)
			}
		}
	}
	roles, err := extension.InstalledTypes(view, ext)
	if err != nil || len(roles) != 3 {
		t.Fatal(roles, err)
	}
	for i, role := range roles {
		name := fmt.Sprintf("tablefunc_crosstab_%d", 2+i)
		if role.Name != name || role.Type.Address() != ext.Members[6+2*i] || role.Type.Owner != e.RoleStore.Domain.Principal(10) {
			t.Fatal("symbolic declaration role", role)
		}
	}
	// Generated arrays remain direct members with portable owner roles, but
	// are not independent SQL declarations or literal names ending in [].
	for slot := 6; slot <= 11; slot++ {
		typ := view.MustType(ext.Members[slot])
		name := fmt.Sprintf("tablefunc_crosstab_%d", 2+(slot-6)/2)
		if slot%2 == 1 {
			name += "[]"
		}
		if role, err := extension.MemberTypeRole(view, ext, typ); err != nil || role != name {
			t.Fatal("symbolic member role", slot, role, err)
		}
		if slot%2 == 1 {
			forged := *typ
			forged.SQLType, err = sqltype.Array(typ.SQLType.ID(), typ.SQLType.Name(), typ.SQLType.Element())
			if err != nil {
				t.Fatal(err)
			}
			if forged.Address() != typ.Address() {
				t.Fatal("forged array changed address")
			}
			if _, err := extension.MemberTypeRole(view, ext, &forged); err == nil {
				t.Fatal("same-address foreign array codec accepted", slot)
			}
		}
		state := catalog.CloneState(view.State())
		bad := *typ
		bad.Extension = ids.ObjectAddress{}
		state.SetDescriptor(bad.Address(), &bad)
		if extension.StockTablefunc.ValidateExtension(e.Catalog.Read(state), ext) == nil {
			t.Fatal("missing type membership accepted")
		}
		// A detached type is not itself an ABI violation. Preserve the
		// reciprocal-inventory negative above, then independently forge this
		// routine's actual nominal prerequisite for its callable-owner proof.
		origin := *bad.Origin
		installation := *origin.Installation
		origin.Installation = &installation
		bad.Origin = &origin
		if (extension.CatalogProvider{Class: ids.ProcedureClassID}).Validate(e.Catalog.Read(state), view.MustProcedure(ext.Members[12+(slot-6)/2])) == nil {
			t.Fatal("shared validator accepted unsealed nominal prerequisite")
		}
	}
	for _, slot := range []int{6, 8, 10} {
		typ := view.MustType(ext.Members[slot])
		backing := view.MustRelation(typ.OwningRelation)
		if catalog.ExtensionMembership(backing) != (ids.ObjectAddress{}) || backing.RelationKind != catalog.CompositeRelation || backing.RowType != typ.SQLType.ID() {
			t.Fatal("standalone backing", backing)
		}
		state := catalog.CloneState(view.State())
		state.RemoveDescriptor(backing.Address())
		if extension.StockTablefunc.ValidateExtension(e.Catalog.Read(state), ext) == nil {
			t.Fatal("missing backing accepted")
		}
		if (extension.CatalogProvider{Class: ids.ProcedureClassID}).Validate(e.Catalog.Read(state), view.MustProcedure(ext.Members[12+(slot-6)/2])) == nil {
			t.Fatal("shared validator accepted missing composite backing")
		}
	}
	e.Mu.Lock()
	stage := e.BeginCatalogStage(view)
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	edges, err := core.DomainDependencyClosure(context.Background(), stage, []ids.ObjectAddress{ext.Address()})
	if err != nil {
		t.Fatal(err)
	}
	closure := make(map[ids.ObjectAddress]bool, len(edges))
	for _, edge := range edges {
		closure[edge.Address] = true
	}
	for _, address := range ext.Members {
		if !closure[address] {
			t.Fatal("extension drop omits member", address)
		}
	}
	for _, slot := range []int{6, 8, 10} {
		if !closure[view.MustType(ext.Members[slot]).OwningRelation] {
			t.Fatal("extension drop omits backing", slot)
		}
	}
	p := view.MustProcedure(ext.Members[12])
	ref := ids.ObjectRef{Address: p.Address(), Epoch: view.Epoch()}
	// A same-number foreign ref cannot supply the owner's domain provenance.
	if extension.ValidateTablefuncIdentity(view, p, ref) == nil {
		t.Fatal("foreign reference accepted")
	}
	good := e.Catalog.Domain.Ref(view.Epoch(), p.Address())
	if err := extension.ValidateTablefuncIdentity(view, p, good); err != nil {
		t.Fatal("current reference rejected", err)
	}
	stale := good
	stale.Epoch++
	if extension.ValidateTablefuncIdentity(view, p, stale) == nil {
		t.Fatal("stale epoch accepted")
	}
	foreign := engine.New().Catalog.Domain.Ref(view.Epoch(), p.Address())
	if extension.ValidateTablefuncIdentity(view, p, foreign) == nil {
		t.Fatal("same-number foreign domain accepted")
	}
	copied := *p
	if extension.StockTablefunc.ValidateProcedure(view, &copied) == nil {
		t.Fatal("detached procedure adopted")
	}
}

func TestTablefuncCrosstabControlIdentity(t *testing.T) {
	e := engine.New()
	ext := tablefuncUnitInstalled(t, e)
	view := e.CatalogRead()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	memberOwner := roleAttributeCreate(t, e, op, "tablefunc_member_owner", true, parser.RoleOptions{})
	state := catalog.CloneState(view.State())
	for _, slot := range []int{6, 8, 10} {
		typ := view.MustType(ext.Members[slot])
		primary := *typ
		primary.ACL = authorization.SchemaACLOwner(primary.ACL, primary.Owner, memberOwner.Ref)
		primary.Owner = memberOwner.Ref
		state.SetDescriptor(primary.Address(), &primary)
		companion := *view.MustType(ext.Members[slot+1])
		companion.ACL = authorization.SchemaACLOwner(companion.ACL, companion.Owner, memberOwner.Ref)
		companion.Owner = memberOwner.Ref
		state.SetDescriptor(companion.Address(), &companion)
		backing := *view.MustRelation(typ.OwningRelation)
		backing.Owner = memberOwner.Ref
		state.SetDescriptor(backing.Address(), &backing)
	}
	metadata, err := extension.OwnerMetadata(e.Catalog.Read(state), e.Roles(), ext)
	if err != nil {
		t.Fatal(err)
	}
	record, found, err := extension.DecodeOwners(strings.TrimSpace(strings.TrimPrefix(metadata, "-- ")))
	if err != nil || !found || len(record.Members) != 16 {
		t.Fatal(record, found, err)
	}
	// Deliberately reverse the record: import must restore coherent pairs without
	// imposing metadata array order or mutating the supplied immutable record.
	for i, j := 0, len(record.Members)-1; i < j; i, j = i+1, j-1 {
		record.Members[i], record.Members[j] = record.Members[j], record.Members[i]
	}
	replay := e.RestoreStage()
	tablefuncUnitInstalled(t, replay)
	replayView := replay.CatalogRead()
	replayExt := replayView.MustExtension(replayView.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "tablefunc"}))
	candidate := catalog.CloneState(replayView.State())
	changed, err := extension.ImportOwners(context.Background(), replay.Catalog.Read(candidate), replay.Roles(), []extension.OwnerRecord{record})
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	restored := replay.Catalog.Read(candidate)
	if err := extension.StockTablefunc.ValidateExtension(restored, replayExt); err != nil {
		t.Fatal(err)
	}
	for _, slot := range []int{6, 8, 10} {
		typ := restored.MustType(replayExt.Members[slot])
		owner, _ := replay.Roles().Lookup(memberOwner.Name)
		if typ.Owner != owner.Ref || restored.MustRelation(typ.OwningRelation).Owner != owner.Ref || restored.MustType(replayExt.Members[slot+1]).Owner != owner.Ref {
			t.Fatal("owner pair/backing lost")
		}
		if err := (extension.CatalogProvider{Class: ids.ProcedureClassID}).Validate(restored, restored.MustProcedure(replayExt.Members[12])); err != nil {
			t.Fatal("shared validator rejected imported composite owners", err)
		}
	}
	for _, slot := range []int{12, 13, 14} {
		p := view.MustProcedure(ext.Members[slot])
		target := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 900000 + ids.OID(slot)}
		mapped := extension.RemapDescriptor(p, map[ids.ObjectAddress]ids.ObjectAddress{{ClassID: ids.TypeClassID, ObjectID: p.Result}: target}).(*catalog.Procedure)
		if mapped.Result != target.ObjectID || p.Result == mapped.Result || mapped.Signature != p.Signature {
			t.Fatal("named result remap", p, mapped)
		}
	}
	for _, reversed := range []bool{false, true} {
		ordered := extensionOwnerRecipeOrder(record)
		if reversed {
			for left, right := 0, len(ordered.Members)-1; left < right; left, right = left+1, right-1 {
				ordered.Members[left], ordered.Members[right] = ordered.Members[right], ordered.Members[left]
			}
		}
		coherent := catalog.CloneState(replayView.State())
		if changed, err := extension.ImportOwners(context.Background(), replay.Catalog.Read(coherent), replay.Roles(), []extension.OwnerRecord{ordered}); err != nil || !changed {
			t.Fatal("coherent requested owners depend on declaration order", reversed, changed, err)
		}
		if err := extension.StockTablefunc.ValidateExtension(replay.Catalog.Read(coherent), replayExt); err != nil {
			t.Fatal("coherent owner import lost current nominal graph", reversed, err)
		}
		for slot := 6; slot <= 11; slot++ {
			t.Run(fmt.Sprintf("reverse_%t_split_%d", reversed, slot), func(t *testing.T) {
				bad := ordered
				bad.Members = append([]extension.MemberOwner(nil), ordered.Members...)
				found := false
				for index := range bad.Members {
					if bad.Members[index].Declaration != nil && *bad.Members[index].Declaration == slot {
						bad.Members[index].Owner = "postgres"
						found = true
					}
				}
				if !found {
					t.Fatal("missing actual released nominal declaration", slot)
				}
				failed := catalog.CloneState(replayView.State())
				if changed, err := extension.ImportOwners(context.Background(), replay.Catalog.Read(failed), replay.Roles(), []extension.OwnerRecord{bad}); err == nil || changed {
					t.Fatal("split composite/array ownership accepted", changed, err)
				}
				for address, original := range replayView.Descriptors() {
					if failed.Get(address) != original {
						t.Fatal("failed owner import changed candidate authority", address)
					}
				}
			})
		}
	}
}
