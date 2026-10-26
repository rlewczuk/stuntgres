package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func shmemInput(t *testing.T, e *core.Engine, oid ids.OID) pgcatalog.Input {
	t.Helper()
	view := e.CatalogRead(); address := ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:oid}
	source, ok := view.Relation(address); if !ok { t.Fatal("missing shared-memory view", oid) }
	return pgcatalog.Input{Context:t.Context(), Catalog:view, Owner:view.Owner(), Ref:view.Owner().Domain.Ref(view.Epoch(),address), Source:source, Roles:e.RoleStore.PublishedRead(view.Owner(),view)}
}

func shmemUnitRows(t *testing.T, in pgcatalog.Input, rows [][]sqltype.Value) {
	t.Helper()
	want := [][]string{{"stuntgres simulated arena","4096","65537","65600"},{"<anonymous>","NULL","4096","4096"},{"NULL","69696","978880","978880"}}
	if in.Source.Address().ObjectID == 12138 { want = [][]string{{"stuntgres simulated arena","0","69632"},{"stuntgres simulated arena","NULL","0"}} }
	if len(rows) != len(want) { t.Fatal("cardinality", rows) }
	for i, row := range rows {
		if len(row) != len(want[i]) || cap(row) != len(row) { t.Fatal("bounded row", row) }
		for j, value := range row {
			if value.Type() != in.Source.Columns[j].Usage.Type { t.Fatal("foreign cell type", value) }
			got := "NULL"; if !value.IsNull() { var err error; got, err = value.Type().Output(t.Context(),value); if err != nil { t.Fatal(err) } }
			if got != want[i][j] { t.Fatal(i,j,got,want[i][j]) }
		}
	}
}

func TestPGShmemSimulationContract(t *testing.T) {
	e := engine.New(); foreign := engine.New()
	for _, oid := range []ids.OID{12134,12138} {
		in := shmemInput(t,e,oid); other := shmemInput(t,foreign,oid)
		if in.Source == other.Source || in.Owner == other.Owner || in.Ref == other.Ref { t.Fatal("shared engine identity") }
		acl := in.Source.Security.ACL
		stats := in.Roles.LookupRef("pg_read_all_stats")
		if !acl.Explicit || len(acl.Entries) != 2 || acl.Entries[0].Grantor != in.Source.Owner || acl.Entries[0].Grantee.Role != in.Source.Owner || acl.Entries[0].Privileges != authorization.TableGrantPrivileges || acl.Entries[1].Grantor != in.Source.Owner || acl.Entries[1].Grantee.Role != stats || acl.Entries[1].Privileges != authorization.SelectPrivilege { t.Fatal("exact owned ACL",acl) }
		for _, entry := range acl.Entries { if entry.Grantee.Public || entry.GrantOptions != 0 { t.Fatal(entry) } }
		routineOID := ids.OID(5052)
		if oid == 12138 { routineOID = 4100 }
		address := ids.ObjectAddress{ClassID:ids.ProcedureClassID,ObjectID:routineOID}
		procedure, present := in.Catalog.Procedure(address)
		foreignProcedure, foreignPresent := other.Catalog.Procedure(address)
		if !present || !foreignPresent || procedure == foreignProcedure || procedure.Owner != in.Source.Owner || procedure.Builtin == nil || procedure.Builtin.Providers != 0 || !procedure.Builtin.ReturnsSet || !procedure.Strict || procedure.Result != 2249 || procedure.Builtin.Parallel != 's' || procedure.Display.Language != catalog.InternalRoutineLanguage { t.Fatal("noncallable owned backing declaration", procedure) }
		if routine.BuiltinRoutine(routineOID) != procedure.Builtin { t.Fatal("backing declaration lacks canonical identity authority") }
		if err := (stored.ProcedureProvider{}).Validate(in.Catalog, procedure); err != nil { t.Fatal("genuine metadata declaration rejected", err) }
		if declaration, err := routine.LookupBuiltinRoutineDeclaration(functions.CatalogRoutineProvider, procedure.Name(), nil); err == nil || declaration != nil { t.Fatal("metadata declaration acquired scalar provider", declaration, err) }
		outputs := procedure.OutputDeclaration()
		if outputs == nil || outputs.Len() != len(in.Source.Columns) || outputs.Inputs() != 0 { t.Fatal("backing OUT declaration", outputs) }
		for i, column := range in.Source.Columns {
			if outputs.Type(i) != ids.OID(column.Usage.Type.ID()) || outputs.Name(i) != column.Name { t.Fatal("backing OUT field", i) }
		}
		routineACL := procedure.ACL
		if !routineACL.Explicit || len(routineACL.Entries) != 2 { t.Fatal("backing ACL", routineACL) }
		for i, entry := range routineACL.Entries {
			grantee := procedure.Owner
			if i == 1 { grantee = stats }
			if entry.Grantor != procedure.Owner || entry.Grantee.Public || entry.Grantee.Role != grantee || entry.Privileges != authorization.ExecutePrivilege || entry.GrantOptions != 0 { t.Fatal("exact backing ACL", entry) }
		}
		produce, ok := pgcatalog.ProducerFor(oid); if !ok { t.Fatal("producer absent") }
		rows, err := produce(in); if err != nil { t.Fatal(err) }; shmemUnitRows(t,in,rows)
		for _, edit := range []func(*pgcatalog.Input){
			func(x *pgcatalog.Input){x.Owner=nil},func(x *pgcatalog.Input){x.Owner=other.Owner},func(x *pgcatalog.Input){x.Catalog=other.Catalog},func(x *pgcatalog.Input){x.Source=other.Source},func(x *pgcatalog.Input){x.Source=nil},func(x *pgcatalog.Input){x.Ref=other.Ref},func(x *pgcatalog.Input){x.Ref.Epoch++},func(x *pgcatalog.Input){x.Ref.Address.ObjectID++},func(x *pgcatalog.Input){x.Roles=other.Roles},
		} { wrong:=in; edit(&wrong); out,err:=produce(wrong); if out!=nil || err==nil { t.Fatal("foreign authority accepted",out,err) } }
		for _, edit := range []func(*catalog.Relation){func(r *catalog.Relation){r.Columns=r.Columns[:1]},func(r *catalog.Relation){r.Columns[0].Name="wrong"},func(r *catalog.Relation){r.Columns[0].Usage.Type,_=in.Catalog.LookupType(sqltype.Name)},func(r *catalog.Relation){r.Columns[0].Usage.Type=other.Source.Columns[0].Usage.Type}} {
			state:=*in.Catalog.State(); source:=*in.Source; source.Columns=slices.Clone(source.Columns); edit(&source); state.SetDescriptor(source.Address(),&source)
			wrong:=in; wrong.Catalog=e.Catalog.Read(&state); wrong.Source=&source
			out,err:=produce(wrong); if out!=nil || err==nil { t.Fatal("invalid shape accepted",out,err) }
		}
		canceled,cancel:=context.WithCancel(t.Context());cancel()
		for _,ctx:=range []context.Context{canceled,&pgEnumCancelDuringCapture{Context:t.Context(),after:2},&pgEnumCancelDuringCapture{Context:t.Context(),after:4}} { wrong:=in;wrong.Context=ctx;out,err:=produce(wrong);if out!=nil || !errors.Is(err,context.Canceled){t.Fatal("partial cancellation",out,err)} }
	}
}

func TestPGShmemCaptureOwnership(t *testing.T) {
	e:=engine.New()
	for _,oid:=range []ids.OID{12134,12138} {
		in:=shmemInput(t,e,oid);produce,_:=pgcatalog.ProducerFor(oid)
		first,err:=core.CaptureCatalogSourceRows(in,produce);if err!=nil {t.Fatal(err)}
		second,err:=produce(in);if err!=nil {t.Fatal(err)}
		if first.Ref!=in.Ref || &first.Anonymous[0][0]==&second[0][0] {t.Fatal("retained backing/ref")}
		second[0][0]=sqltype.String(in.Source.Columns[0].Usage.Type,"changed")
		state:=*in.Catalog.State();replacement:=*in.Source;replacement.Columns=slices.Clone(replacement.Columns);replacement.ObjectRevision++;state.SetDescriptor(replacement.Address(),&replacement)
		wrong:=in;wrong.Catalog=e.Catalog.Read(&state)
		if out,err:=core.CaptureCatalogSourceRows(wrong,produce);out!=nil || err==nil {t.Fatal("stale source published",out,err)}
		shmemUnitRows(t,in,first.Anonymous)
		routineOID := ids.OID(5052)
		if oid == 12138 { routineOID = 4100 }
		shmemRoutineACLAuthority(t, e, routineOID)
		shmemUnitRows(t, in, first.Anonymous)
	}
}

// Use the ordinary command planner and publication path, not a private ACL
// setter: the property under test is ownership across genuine grant COW.
func shmemRoutineACLAuthority(t *testing.T, e *core.Engine, oid ids.OID) {
	t.Helper()
	address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
	initial := e.CatalogRead().MustProcedure(address)
	canonical, outputs := initial.Builtin, initial.OutputDeclaration()
	initialSnapshot := *initial
	initialSnapshot.ACL.Entries = slices.Clone(initial.ACL.Entries)
	for _, revoke := range []bool{false, true} {
		before := e.CatalogRead()
		predecessor := before.MustProcedure(address)
		snapshot := *predecessor
		snapshot.ACL.Entries = slices.Clone(predecessor.ACL.Entries)
		command := &parser.ObjectGrant{
			Kind: "function", Revoke: revoke,
			Privileges: []parser.GrantPrivilege{{Name: "execute"}},
			Routines: []parser.GrantRoutineTarget{{
				Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "pg_catalog"}, {Text: predecessor.Name()}}},
				SignatureSpecified: true,
			}},
			Grantees: []parser.GrantGrantee{{Role: parser.RoleSpec{Name: "pg_signal_backend"}}},
		}
		if _, err := enginetest.Execute(e, t.Context(), command, nil); err != nil {
			t.Fatal("ordinary backing-routine ACL publication", oid, revoke, err)
		}
		current := e.CatalogRead()
		replacement := current.MustProcedure(address)
		if replacement == predecessor || current.State() == before.State() ||
			before.MustProcedure(address) != predecessor || !reflect.DeepEqual(*predecessor, snapshot) ||
			!reflect.DeepEqual(*initial, initialSnapshot) {
			t.Fatal("ACL COW mutated or reused a retained predecessor", oid, revoke)
		}
		if replacement.Builtin != canonical || canonical != routine.BuiltinRoutine(oid) ||
			replacement.OutputDeclaration() != outputs || replacement.Owner != initial.Owner ||
			replacement.Builtin.Providers != 0 || len(replacement.ACL.Entries) == 0 || len(predecessor.ACL.Entries) == 0 ||
			&replacement.ACL.Entries[0] == &predecessor.ACL.Entries[0] {
			t.Fatal("ACL replacement lost canonical declaration or owned backing", oid, revoke)
		}
		roles := e.RoleStore.PublishedRead(current.Owner(), current)
		want := slices.Clone(initialSnapshot.ACL.Entries)
		if !revoke {
			want = append(want, authorization.ACLEntry{
				Grantor: initial.Owner,
				Grantee: authorization.ACLGrantee{Role: roles.LookupRef("pg_signal_backend")},
				Privileges: authorization.ExecutePrivilege,
			})
		}
		if !replacement.ACL.Explicit || !slices.Equal(replacement.ACL.Entries, want) {
			t.Fatal("ordinary ACL change did not publish exact authority", oid, revoke, replacement.ACL)
		}
		if err := (stored.ProcedureProvider{}).Validate(current, replacement); err != nil {
			t.Fatal("canonical COW descriptor rejected", oid, revoke, err)
		}
		if declaration, err := routine.LookupBuiltinRoutineDeclaration(^functions.RoutineProvider(0), replacement.Name(), nil); err == nil || declaration != nil {
			t.Fatal("COW metadata acquired a callable provider", oid, revoke, declaration, err)
		}
		// All declaration values and OUT backing remain identical; only the
		// canonical pointer authority is replaced in this unpublished candidate.
		copied := *canonical
		forged := *replacement
		forged.Builtin = &copied
		state := catalog.CloneState(current.State())
		state.SetDescriptor(address, &forged)
		if !reflect.DeepEqual(copied, *canonical) || forged.OutputDeclaration() != outputs {
			t.Fatal("copied declaration is not the equal-looking witness")
		}
		if err := (stored.ProcedureProvider{}).Validate(current.WithState(state), &forged); err == nil {
			t.Fatal("value-identical copied declaration accepted as authority", oid, revoke)
		}
	}
}
