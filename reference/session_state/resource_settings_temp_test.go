package reference_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/stdlib"
	"stuntgres"
	"stuntgres/reference/fixture"
)

func resourceTemporaryStreams(t *testing.T) {
	physicalCases(t,nil,func(s *physicalCase) {
		// Genuine streaming COPY runs on a dedicated pinned transport connection.
		var notices []fixture.Diagnostic
		stream:=fixture.CopyNoticeConnection(s.t,s.p,s.pg,&notices,nil)
		probe:=&physicalCase{s.t,s.p,stream,s.pg,s.recorder}
		probe.run(fixture.Exec("CREATE TEMP TABLE resource_temp_stream(v integer)",0))
		probe.run(fixture.Exec("SET temp_buffers=2048",0))
		result,err:=fixture.CopyFrom(s.p.Ctx,stream,s.pg,"COPY resource_temp_stream FROM STDIN",strings.NewReader("11\n"))
		if err!=nil || result.Count!=1 || result.CommandTag!="COPY 1" { s.t.Fatal("COPY FROM",result,err) }
		probe.run(physicalError("SET temp_buffers='1024'",resourceTempError("1024"),false,false))
		var output bytes.Buffer
		if s.pg {
			err=stream.Raw(func(raw any)error {
				conn,ok:=raw.(*stdlib.Conn); if !ok { return fmt.Errorf("unexpected COPY connection %T",raw) }
				tag,e:=conn.Conn().PgConn().CopyTo(s.p.Ctx,&output,"COPY resource_temp_stream TO STDOUT")
				if e==nil { result=stuntgres.CopyResult{Count:tag.RowsAffected(),CommandTag:tag.String()} }; return e
			})
		} else { result,err=stuntgres.CopyTo(s.p.Ctx,stream,"COPY resource_temp_stream TO STDOUT",&output) }
		if err!=nil || result.Count!=1 || result.CommandTag!="COPY 1" || output.String()!="11\n" { s.t.Fatal("COPY TO",result,output.String(),err) }
		probe.run(physicalError("SET temp_buffers='1024'",resourceTempError("1024"),true,false))
		// Foreign TEMP failure is not the caller's own successful data access.
		s.run(fixture.Exec("CREATE TEMP TABLE resource_temp_foreign(v integer)",0))
		s.run(fixture.Exec("INSERT INTO resource_temp_foreign VALUES(7)",1))
		var namespace string
		if err:=s.conn.QueryRowContext(s.p.Ctx,"SELECT schemaname FROM pg_tables WHERE tablename='resource_temp_foreign'").Scan(&namespace); err!=nil { s.t.Fatal(err) }
		peer:=s.lockPeer()
		for _, prepared:=range []bool{false,true} {
			for _, query:=range []string{"SELECT v FROM "+namespace+".resource_temp_foreign","INSERT INTO "+namespace+".resource_temp_foreign VALUES(9)"} {
				peer.run(physicalError(query,physicalDiagnostic("0A000","cannot access temporary tables of other sessions"),prepared,strings.HasPrefix(query,"SELECT")))
			}
		}
		peer.run(fixture.Exec("SET temp_buffers=3072",0))
		peer.resourceValue("temp_buffers","24MB","3072","session",true)
		// The native simulation excludes sequences; PostgreSQL's physical local
		// buffer timing is deliberately not claimed for this native-only phase.
		if !s.pg {
			peer.run(fixture.Exec("CREATE TEMP SEQUENCE resource_temp_sequence",0))
			peer.run(fixture.Select("SELECT nextval('resource_temp_sequence')",[]string{"nextval"},[]string{"INT8"},[][]any{{int64(1)}}))
			peer.run(fixture.Exec("SET temp_buffers=2048",0))
		}
		peer.run(fixture.Exec("CREATE TEMP TABLE resource_temp_empty(v integer)",0))
		peer.run(fixture.Exec("SET temp_buffers=3072",0))
		peer.run(fixture.Exec("INSERT INTO resource_temp_empty SELECT 11",1))
		peer.run(physicalError("SET temp_buffers='1024'",resourceTempError("1024"),true,false))
	})
}
