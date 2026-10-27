package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGSubscriptionNativeControls(t *testing.T) {
	withSubscriptionReader(t,func(p *fixture.Pair,b fixture.Backend,admin,actor *sql.Conn) {
		q := "SELECT subconninfo FROM pg_catalog.pg_subscription WHERE FALSE"
		stmt,err := actor.PrepareContext(p.Ctx,q)
		if err != nil { t.Fatal(err) }; defer stmt.Close()
		held := fixture.HeldStatement{actor,stmt,q}
		check := func() {
			subscriptionShape(t,p,b,admin)
			fixture.RunStep(t,p.Ctx,admin,b.Recorder,subscriptionEmpty(b.PG,"SELECT * FROM pg_catalog.pg_subscription",false))
			subscriptionPublic(t,p,b,actor)
			subscriptionDenied(t,p,b,held,q,false)
		}
		check()
		if b.PG { return } // Reset/Restore are native host operations, not SQL parity.
		fixture.RunStep(t,p.Ctx,admin,b.Recorder,fixture.Exec("CREATE TABLE subcat_sentinel(v INT)",0))
		fixture.RunStep(t,p.Ctx,admin,b.Recorder,fixture.Exec("INSERT INTO subcat_sentinel VALUES(77)",1))
		retained,err := p.Native.QueryContext(p.Ctx,"SELECT * FROM pg_catalog.pg_subscription")
		if err != nil { t.Fatal(err) }; defer retained.Close()
		metadata := subscriptionEmpty(false,"SELECT * FROM pg_catalog.pg_subscription",false)
		metadata.CheckMetadata(t,retained)
		before,err := retained.ColumnTypes()
		if err != nil { t.Fatal(err) }
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx,p.Native,&dump); err != nil { t.Fatal(err) }
		if dump.Len() == 0 { t.Fatal("empty dump") }
		if err := stuntgres.Reset(p.Ctx,p.Native); err != nil { t.Fatal(err) }
		check()
		if err := stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
		check()
		fixture.RunStep(t,p.Ctx,admin,b.Recorder,fixture.Select("SELECT v FROM subcat_sentinel",[]string{"v"},[]string{"INT4"},[][]any{{int64(77)}}))
		after,err := retained.ColumnTypes()
		if err != nil || !reflect.DeepEqual(before,after) { t.Fatal("retained metadata changed",before,after,err) }
		metadata.CheckMetadata(t,retained)
		if retained.Next() || retained.Err() != nil { t.Fatal("retained subscription empty rows",retained.Err()) }
	})
}
