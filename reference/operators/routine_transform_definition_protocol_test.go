//go:build !noservertests

package reference_test

import (
 "context"
 "testing"
 "time"

 "github.com/jackc/pgx/v5/pgproto3"
 "stuntgres/reference/fixture"
)

func TestReferenceRoutineTransformDefinitionProtocol(t *testing.T) {
 f:=fixture.NewServerFixture(t)
 ctx,cancel:=context.WithTimeout(f.Ctx,60*time.Second); defer cancel()
 for _,b:=range f.Backends() {
  t.Run(b.Name,func(t *testing.T) {
   w:=triggerProtocolConnect(t,ctx,f.DSNFor(b))
   defer func(){ triggerProtocolCode(t,w.query(t,"ROLLBACK"),"") }()
   const probe="SELECT count(*) FROM pg_transform WHERE trftype='integer'::regtype AND trflang=(SELECT oid FROM pg_language WHERE lanname='sql')"
   const create="CREATE TRANSFORM FOR integer LANGUAGE sql (FROM SQL WITH FUNCTION pg_catalog.dsimple_init(internal))"
   const drop="DROP TRANSFORM FOR integer LANGUAGE sql"
   triggerProtocolScalar(t,w,probe,"0")
   frames:=w.cycle(t,&pgproto3.Parse{Name:"td_create",Query:create},&pgproto3.Describe{ObjectType:'S',Name:"td_create"})
   triggerProtocolTags(t,frames,"1tnZ")
   triggerProtocolReady(t,frames,'I')
   triggerProtocolScalar(t,w,probe,"0")
   // Sync ends an implicit transaction and destroys its portals. The split
   // Bind/Describe and later Execute therefore require an explicit owner.
   triggerProtocolCode(t,w.query(t,"BEGIN"),"")
   frames=w.cycle(t,&pgproto3.Bind{DestinationPortal:"td_portal",PreparedStatement:"td_create"},&pgproto3.Describe{ObjectType:'P',Name:"td_portal"})
   definitionWireNoError(t,frames)
   triggerProtocolTags(t,frames,"2nZ")
   triggerProtocolReady(t,frames,'T')
   triggerProtocolScalar(t,w,probe,"0")
   frames=w.cycle(t,&pgproto3.Execute{Portal:"td_portal"})
   definitionWireNoError(t,frames)
   triggerProtocolTags(t,frames,"CZ")
   triggerProtocolCompletions(t,frames,"CREATE TRANSFORM")
   triggerProtocolReady(t,frames,'T')
   triggerProtocolCompletions(t,w.query(t,"COMMIT"),"COMMIT")
   triggerProtocolScalar(t,w,probe,"1")
   frames=w.cycle(t,&pgproto3.Parse{Name:"td_drop",Query:drop},&pgproto3.Describe{ObjectType:'S',Name:"td_drop"})
   triggerProtocolTags(t,frames,"1tnZ")
   triggerProtocolScalar(t,w,probe,"1")
   frames=w.cycle(t,&pgproto3.Bind{DestinationPortal:"td_remove",PreparedStatement:"td_drop"},&pgproto3.Describe{ObjectType:'P',Name:"td_remove"},&pgproto3.Execute{Portal:"td_remove"})
   triggerProtocolTags(t,frames,"2nCZ")
   triggerProtocolCompletions(t,frames,"DROP TRANSFORM")
   triggerProtocolReady(t,frames,'I')
   triggerProtocolScalar(t,w,probe,"0")
   frames=w.query(t,create+"; SELECT 1/0")
   triggerProtocolError(t,frames,"22012","division by zero","")
   triggerProtocolReady(t,frames,'I')
   triggerProtocolScalar(t,w,probe,"0")
  })
 }
}

func definitionWireNoError(t *testing.T,frames [][]byte) {
 t.Helper()
 for _,frame:=range frames {
  if frame[0]!='E' { continue }
  var diagnostic pgproto3.ErrorResponse
  if err:=diagnostic.Decode(frame[5:]); err!=nil { t.Fatal(err) }
  t.Fatalf("unexpected SQL packet diagnostic: %#v",diagnostic)
 }
}
