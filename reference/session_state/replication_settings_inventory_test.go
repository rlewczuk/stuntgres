package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceReplicationSettingsInventory(t *testing.T) {
	physicalCases(t, []string{"replication_inventory_plain"}, func(s *physicalCase) {
		s.run(fixture.Exec("CREATE ROLE replication_inventory_plain LOGIN PASSWORD 'role-core-fixture'", 0))
		plain := s.login("replication_inventory_plain")
		for _, prepared := range []bool{false,true} {
			s.replicationReads(true,prepared)
			plain.replicationReads(false,prepared)
		}
	})
}

func (s *physicalCase) replicationSentinel(peer *physicalCase, owner string) {
	s.t.Helper()
	s.replicationInventory(true,false,nil)
	peer.replicationInventory(true,false,nil)
	s.run(fixture.Select("SELECT id,value FROM replication_context_data ORDER BY id", []string{"id","value"}, []string{"INT4","TEXT"}, [][]any{{int64(1),"kept"},{int64(2),nil}}))
	s.run(fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{owner}}))
	peer.run(fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"replication-peer"}}))
}

func TestReferenceReplicationSettingsImmutableContexts(t *testing.T) {
	// The first setting of each (context, type) class runs every route; the
	// others probe direct SET and RESET once each.
	classes := map[[2]string]bool{}
	for _, d := range replicationSettings {
		if d.context != "postmaster" && d.context != "sighup" { continue }
		class := [2]string{d.context, d.kind}
		full := !classes[class]
		classes[class] = true
		t.Run(d.name,func(t *testing.T) {
			physicalCases(t, []string{"replication_context_grantee"},func(s *physicalCase) {
				s.run(fixture.Exec("CREATE TABLE replication_context_data(id integer,value text)",0))
				s.run(fixture.Exec("INSERT INTO replication_context_data VALUES(1,'kept'),(2,NULL)",2))
				if !full {
					s.run(fixture.Exec("SET application_name='replication-owner'",0))
					for _, q := range []string{"SET "+d.name+"='invalid'","RESET "+d.name} {
						s.run(physicalError(q,replicationContext(d),false,false))
					}
					s.run(fixture.Exec("RESET ALL",0))
					s.replicationInventory(true,false,nil)
					s.run(fixture.Select("SELECT id,value FROM replication_context_data ORDER BY id", []string{"id","value"}, []string{"INT4","TEXT"}, [][]any{{int64(1),"kept"},{int64(2),nil}}))
					s.run(fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{""}}))
					return
				}
				s.run(fixture.Exec("CREATE ROLE replication_context_grantee LOGIN PASSWORD 'role-core-fixture'",0))
				s.run(fixture.Exec("GRANT SET ON PARAMETER "+d.name+" TO replication_context_grantee",0))
				defer s.conn.ExecContext(s.p.Ctx,"REVOKE SET ON PARAMETER "+d.name+" FROM replication_context_grantee")
				peer,err := s.connPoolPeer(); if err != nil { t.Fatal(err) }; defer peer.conn.Close()
				peer.run(fixture.Exec("SET application_name='replication-peer'",0))
				user := s.login("replication_context_grantee")
				for _, prepared := range []bool{false,true} {
					queries := []fixture.Step{}
					alternate := "1"; if d.kind == "bool" { alternate = "on"; if d.raw == "on" { alternate = "off" } }; if d.kind == "string" { alternate = "other" }
					for _, value := range []string{d.raw,alternate,"invalid"} {
						queries = append(queries,physicalError("SET "+d.name+"="+physicalLiteral(value),replicationContext(d),prepared,false))
					}
					for _, q := range []string{"SET "+d.name+"=DEFAULT", "RESET "+d.name, "SET \""+strings.ToUpper(d.name)+"\"='invalid'", "SET LOCAL "+d.name+"='invalid'"} {
						queries = append(queries,physicalError(q,replicationContext(d),prepared,false))
					}
					list := d.name == "synchronous_standby_names" || d.name == "synchronized_standby_slots"
					diag := replicationContext(d); if !list { diag = physicalDiagnostic("22023","SET "+d.name+" takes only one argument") }
					queries = append(queries,physicalError("SET "+d.name+"='a','b'",diag,prepared,false))
					for _, local := range []any{false,true,nil} { for _, value := range []any{d.raw,"invalid",nil} {
						queries = append(queries,physicalError("SELECT set_config($1,$2,$3)",replicationContext(d),prepared,true,d.name,value,local))
					} }
					// Every transaction error must abort until rollback to its savepoint.
					s.run(fixture.Exec("SET application_name='replication-owner'",0))
					s.run(fixture.Exec("BEGIN",0))
					for _, step := range queries {
						s.run(fixture.Exec("SAVEPOINT replication_error",0))
						s.run(step)
						s.run(physicalError("SELECT 1",physicalDiagnostic("25P02","current transaction is aborted, commands ignored until end of transaction block"),false,true))
						s.run(fixture.Exec("ROLLBACK TO SAVEPOINT replication_error",0))
						s.replicationSentinel(peer,"replication-owner")
						s.run(fixture.Exec("RELEASE SAVEPOINT replication_error",0))
					}
					s.run(fixture.Exec("COMMIT",0))
					for _, actor := range []*physicalCase{s,user} {
						for _, q := range []string{"SET "+d.name+"='invalid'","RESET "+d.name,"SELECT set_config('"+d.name+"',NULL,false)"} {
							actor.run(physicalError(q,replicationContext(d),prepared,strings.HasPrefix(q,"SELECT")))
							s.replicationSentinel(peer,"replication-owner")
						}
					}
				}
				q := "SET "+d.name+"='invalid'"
				stmt,err := s.conn.PrepareContext(s.p.Ctx,q); if err != nil { t.Fatal(err) }; defer stmt.Close()
				s.replicationSentinel(peer,"replication-owner")
				for range 2 { fixture.RunStep(t,s.p.Ctx,fixture.HeldStatement{Executor:s.conn,Statement:stmt,SQL:q},s.recorder,physicalError(q,replicationContext(d),false,false)); s.replicationSentinel(peer,"replication-owner") }
				s.run(fixture.Exec("RESET ALL",0))
				s.replicationSentinel(peer,"")
			})
		})
	}
}
