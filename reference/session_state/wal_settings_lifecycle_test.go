package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceWALSettingsLifecycle(t *testing.T) {
	for _, m := range walMutable {
		t.Run(m.name,func(t *testing.T) {
			physicalCases(t,nil,func(s *physicalCase) {
				d := walDefinition(m.name)
				peer,err := s.connPoolPeer(); if err != nil { t.Fatal(err) }; defer peer.conn.Close()
				readers := []string{"SHOW "+m.name,"SELECT current_setting('"+m.name+"')","SELECT setting FROM pg_settings WHERE name='"+m.name+"'"}
				for _, query := range readers {
					stmt,err := s.conn.PrepareContext(s.p.Ctx,query); if err != nil { t.Fatal(err) }
					defer stmt.Close()
					check := func(changed bool) {
						column,value := "current_setting",d.show
						if query == readers[0] { column = m.name }; if query == readers[2] { column,value = "setting",d.raw }
						if changed { value = m.show; if column == "setting" { value = m.raw } }
						fixture.RunStep(t,s.p.Ctx,fixture.HeldStatement{Executor:s.conn,Statement:stmt,SQL:query},s.recorder,fixture.Select(query,[]string{column},[]string{"TEXT"},[][]any{{value}}))
					}
					for _, prepared := range []bool{false,true} {
						check(false)
						s.run(fixture.Exec("BEGIN",0)); s.walSet(m.name,m.input,prepared); check(true)
						s.run(fixture.Exec("ROLLBACK",0)); check(false)
						s.run(fixture.Exec("BEGIN",0)); s.walSet(m.name,m.input,prepared); s.run(fixture.Exec("COMMIT",0)); check(true)
						for _, end := range []string{"COMMIT","ROLLBACK"} {
							s.run(fixture.Exec("BEGIN",0))
							step := fixture.Exec("SET LOCAL "+m.name+"="+physicalLiteral(walReset(d)),0); step.Prepared = prepared; s.run(step); check(false)
							s.run(fixture.Exec(end,0)); check(true)
						}
						s.run(fixture.Exec("RESET "+m.name,0))
						s.run(fixture.Exec("BEGIN",0)); s.walSet(m.name,m.input,prepared)
						s.run(fixture.Exec("SAVEPOINT wal_lifecycle",0))
						step := fixture.Exec("SET LOCAL "+m.name+"="+physicalLiteral(walReset(d)),0); step.Prepared = prepared; s.run(step); check(false)
						s.run(fixture.Exec("ROLLBACK TO SAVEPOINT wal_lifecycle",0)); check(true)
						s.run(fixture.Exec("RELEASE SAVEPOINT wal_lifecycle",0)); s.run(fixture.Exec("COMMIT",0)); check(true)
						// SET followed by LOCAL commits the session candidate, not
						// the temporary effective value.
						s.run(fixture.Exec("RESET "+m.name,0)); s.run(fixture.Exec("BEGIN",0)); s.walSet(m.name,m.input,prepared)
						s.run(step); check(false); s.run(fixture.Exec("COMMIT",0)); check(true)
						bad := walRejected(m.name)[0]; diagnostic := physicalDiagnostic("22023",bad.message); diagnostic.Hint = bad.hint
						s.run(fixture.Exec("BEGIN",0)); s.run(fixture.Exec("SAVEPOINT wal_failure",0))
						s.run(physicalError("SET "+m.name+"="+physicalLiteral(bad.input),diagnostic,prepared,false))
						s.run(physicalError("SELECT 1",physicalDiagnostic("25P02","current transaction is aborted, commands ignored until end of transaction block"),false,true))
						s.run(fixture.Exec("ROLLBACK TO SAVEPOINT wal_failure",0)); check(true)
						s.run(fixture.Exec("RELEASE SAVEPOINT wal_failure",0)); s.run(fixture.Exec("ROLLBACK",0)); check(true)
						peer.walValue(m.name,d.show,d.raw,"default",prepared)
						if !s.pg {
							rows,err := s.conn.QueryContext(s.p.Ctx,"SHOW "+m.name); if err != nil { t.Fatal(err) }
							s.run(fixture.Exec("RESET "+m.name,0))
							var retained string; if !rows.Next() { t.Fatal("missing retained value",rows.Err()) }; if err := rows.Scan(&retained); err != nil || retained != m.show { t.Fatal(retained,err) }; if rows.Next() || rows.Err() != nil { t.Fatal("extra retained row",rows.Err()) }; rows.Close()
						} else { s.run(fixture.Exec("RESET "+m.name,0)) }
						check(false)
					}
				}
			})
		})
	}
}
