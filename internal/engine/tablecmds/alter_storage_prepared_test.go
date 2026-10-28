package tablecmds_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"reflect"
	"io"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
)

func TestAlterStoragePreparedOwnership(t *testing.T) {
	e:=engine.New()
	s:=enginetest.StartSession(t,e,nil)
	enginetest.SessionSQL(t,s,`CREATE TABLE storage_owned(a text)`)
	enginetest.SessionSQL(t,s,`INSERT INTO storage_owned VALUES('payload')`)
	enginetest.SessionSQL(t,s,`BEGIN`)
	cycle:=s.NewProtocolCycle()
	command:=func(fn func(*core.StatementScope)error) {
		t.Helper()
		op,_,err:=s.AcquireCommand(context.Background());if err!=nil {t.Fatal(err)};defer op.Done()
		scope,err:=e.StartProtocolCommand(op,cycle);if err!=nil {t.Fatal(err)}
		if err:=scope.Finish(fn(scope));err!=nil {t.Fatal(err)}
	}
	command(func(scope *core.StatementScope)error {_,err:=scope.ParseProtocol("storage_statement",`SELECT a, COALESCE(-(7::integer),0) AS retained FROM storage_owned`,nil);return err})
	bind:=func(name string){command(func(scope *core.StatementScope)error {_,err:=scope.BindProtocolPortal(name,"storage_statement",&storageBindInput{bytes.NewReader([]byte{0,0,0,0,0,0})});return err})}
	registry:=core.PayloadOf(s).PreparedSQL
	definition:=registry.Lookup("storage_statement")
	owner:=definition.Description.Folds
	if owner==nil || !owner.Live(){t.Fatal("missing actual definition owner")}
	bind("storage_before")
	portal:=registry.Binding("storage_before")
	root,analysis:=portal.Resource.Root,portal.Entry.Description.Analysis
	accepted:=owner.Current()
	saved:=accepted
	if len(accepted.Entries)==0 {t.Fatal("missing actual retained fold backing")}
	entries:=append([]exec.FoldConstant(nil),accepted.Entries...)
	if root==nil || analysis==nil || analysis.Domain!=e.Catalog.Domain || analysis.Epoch!=e.CatalogRead().Epoch() || portal.Entry.Description.Folds!=owner {t.Fatal("missing captured graph/analysis authority")}
	ref:=enginetest.RoleTableRef(t,e,"storage_owned")
	old:=e.CatalogRead().MustRelation(ref.Address)
	found:=false
	for _,dependency:=range analysis.Dependencies {if dependency.Ref.Address==ref.Address {found=true;if dependency.Ref.Domain()!=analysis.Domain || dependency.Revision!=old.Revision(){t.Fatal("invalid retained dependency")}}}
	if !found {t.Fatal("missing selected table dependency")}
	enginetest.SessionSQL(t,s,`ALTER TABLE storage_owned ALTER a SET STORAGE EXTERNAL`)
	bind("storage_after")
	nextPortal:=registry.Binding("storage_after")
	if nextPortal.Resource.Root==root || nextPortal.Entry.Description.Folds!=owner || nextPortal.Entry.Description.Analysis==analysis || portal.Resource.Root!=root || portal.Entry.Description.Analysis!=analysis {t.Fatal("rebind mutated old graph or failed to refresh analysis")}
	if accepted.Generation!=saved.Generation || owner.Current().Generation<saved.Generation || &accepted.Entries[0]!=&saved.Entries[0] || !reflect.DeepEqual(accepted.Entries,entries) {t.Fatal("retained fold generation/backing changed")}
	// Retire the actual definition; old portals keep their owned graph, but
	// the retired owner can no longer accept a prepared publication.
	var publication *core.FoldPublication
	var environment core.PreparedEnvironment
	op,err:=s.Acquire(context.Background());if err!=nil {t.Fatal(err)}
	e.Mu.Lock()
	auth,err:=e.AuthorizationLocked(op)
	if err==nil {environment,err=e.PreparedEnvironmentLocked(auth,true)}
	if err==nil {publication,err=core.BeginFoldPublication(&definition.Description,definition.Body,environment,auth.Catalog,nil)}
	e.Mu.Unlock();op.Done()
	if err!=nil || publication==nil {t.Fatal(publication,err)}
	command(func(scope *core.StatementScope)error{return scope.CloseProtocolStatement("storage_statement")})
	op,err=s.Acquire(context.Background());if err!=nil {t.Fatal(err)}
	e.Mu.Lock();auth,err=e.AuthorizationLocked(op)
	if err==nil {environment,err=e.PreparedEnvironmentLocked(auth,true)}
	if err==nil {err=publication.Accept(op.Ctx,auth.Catalog,environment,e.Catalog.State.Epoch())}
	e.Mu.Unlock();op.Done()
	if err!=ids.ErrCatalogReplaced || owner.Live() || publication.Group.Sealed || portal.Resource.Root!=root || portal.Entry.Description.Analysis!=analysis {t.Fatal("retired owner publication or graph mutation",err)}
}

type storageBindInput struct{ *bytes.Reader }
func (r *storageBindInput) Uint16()(uint16,error){var n uint16;err:=binary.Read(r.Reader,binary.BigEndian,&n);return n,err}
func (r *storageBindInput) Int32()(int32,error){var n int32;err:=binary.Read(r.Reader,binary.BigEndian,&n);return n,err}
func (r *storageBindInput) Bytes(n int)([]byte,error){b:=make([]byte,n);_,err:=io.ReadFull(r.Reader,b);return b,err}
func (r *storageBindInput) End()error{if r.Len()!=0{return io.ErrUnexpectedEOF};return nil}
