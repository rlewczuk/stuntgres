package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceWALSettingsValidation(t *testing.T) {
	for _, mutable := range walMutable {
		t.Run(mutable.name,func(t *testing.T) {
			physicalCases(t,[]string{"wal_validation_user"},func(admin *physicalCase) {
				admin.run(fixture.Exec("CREATE ROLE wal_validation_user LOGIN PASSWORD 'role-core-fixture'",0))
				admin.run(fixture.Exec("CREATE TABLE public.wal_validation_data(v integer)",0))
				admin.run(fixture.Exec("INSERT INTO public.wal_validation_data VALUES(7)",1))
				s := admin; d := walDefinition(mutable.name)
				if d.context == "user" { s = admin.login("wal_validation_user") }
				peer,err := admin.connPoolPeer(); if err != nil { t.Fatal(err) }; defer peer.conn.Close()
				for _, prepared := range []bool{false,true} {
					s.walSet(d.name,mutable.input,prepared)
					s.walValue(d.name,mutable.show,mutable.raw,"session",prepared)
					peer.walValue(d.name,d.show,d.raw,"default",prepared)
					for _, query := range []string{"SET "+d.name+"=DEFAULT","RESET "+d.name,"SELECT set_config('"+d.name+"',NULL,false)","RESET ALL"} {
						if query[0] == 'S' && len(query) > 6 && query[:6] == "SELECT" {
							step := fixture.Select(query,[]string{"set_config"},[]string{"TEXT"},[][]any{{d.show}}); step.Prepared = prepared; s.run(step)
						} else { step := fixture.Exec(query,0); step.Prepared = prepared; s.run(step) }
						s.walValue(d.name,d.show,d.raw,"default",prepared)
						s.walSet(d.name,mutable.input,prepared)
					}
					// Holding the mutation must not apply it during preparation.
					query := "SELECT set_config($1,$2,false)"
					stmt,err := s.conn.PrepareContext(s.p.Ctx,query); if err != nil { t.Fatal(err) }
					s.walValue(d.name,mutable.show,mutable.raw,"session",false)
					fixture.RunStep(t,s.p.Ctx,fixture.HeldStatement{Executor:s.conn,Statement:stmt,SQL:query},s.recorder,fixture.Select(query,[]string{"set_config"},[]string{"TEXT"},[][]any{{d.show}},d.name,walReset(d)))
					stmt.Close()
					// Explicitly assigning the default remains a session assignment.
					s.walValue(d.name,d.show,d.raw,"session",false)
					s.run(fixture.Exec("RESET ALL",0))
					for _, tc := range walAccepted(d.name) {
						s.walSet(d.name,tc.input,prepared); s.walValue(d.name,tc.show,tc.raw,"session",prepared)
						step := fixture.Select("SELECT set_config($1,$2,false)",[]string{"set_config"},[]string{"TEXT"},[][]any{{tc.show}},d.name,tc.input); step.Prepared = prepared; s.run(step)
					}
					for _, local := range []any{true,nil} {
						s.run(fixture.Exec("RESET "+d.name,0))
						s.run(fixture.Exec("BEGIN",0))
						step := fixture.Select("SELECT set_config($1,$2,$3)",[]string{"set_config"},[]string{"TEXT"},[][]any{{mutable.show}},d.name,mutable.input,local)
						step.Prepared = prepared; s.run(step)
						s.walValue(d.name,mutable.show,mutable.raw,"session",prepared)
						s.run(fixture.Exec("COMMIT",0))
						if local == true { s.walValue(d.name,d.show,d.raw,"default",prepared) } else { s.walValue(d.name,mutable.show,mutable.raw,"session",prepared) }
					}
					s.walSet(d.name,mutable.input,prepared)
					for _, tc := range walRejected(d.name) {
						diagnostic := physicalDiagnostic("22023",tc.message); diagnostic.Hint = tc.hint
						for _, query := range []string{"SET "+d.name+"="+physicalLiteral(tc.input),"SELECT set_config('"+d.name+"',"+physicalLiteral(tc.input)+",false)"} {
							s.run(physicalError(query,diagnostic,prepared,query[:6] == "SELECT"))
							s.walValue(d.name,mutable.show,mutable.raw,"session",prepared)
							peer.walValue(d.name,d.show,d.raw,"default",prepared)
							admin.run(fixture.Select("SELECT v FROM public.wal_validation_data",[]string{"v"},[]string{"INT4"},[][]any{{int64(7)}}))
						}
					}
					s.run(fixture.Exec("RESET ALL",0))
				}
			})
		})
	}
}

type walAcceptedValue struct{ input, show, raw string }
func walAccepted(name string) []walAcceptedValue {
	switch name {
	case "commit_delay": return []walAcceptedValue{{"0","0","0"},{"100000","100000","100000"}}
	case "commit_siblings": return []walAcceptedValue{{"2.5","2","2"},{"3.5","4","4"},{"0","0","0"},{"1000","1000","1000"}}
	case "wal_skip_threshold": return []walAcceptedValue{{"1MB","1MB","1024"},{"0","0","0"}}
	case "synchronous_commit": return []walAcceptedValue{{"LOCAL","local","local"},{"REMOTE_WRITE","remote_write","remote_write"},{"REMOTE_APPLY","remote_apply","remote_apply"},{"TRUE","on","on"},{"YES","on","on"},{"1","on","on"},{"FALSE","off","off"},{"NO","off","off"},{"0","off","off"}}
	case "wal_compression": return []walAcceptedValue{{"pglz","pglz","pglz"},{"lz4","lz4","lz4"},{"zstd","zstd","zstd"},{"ON","pglz","pglz"},{"TRUE","pglz","pglz"},{"YES","pglz","pglz"},{"1","pglz","pglz"},{"false","off","off"},{"no","off","off"},{"0","off","off"}}
	default: return []walAcceptedValue{{"off","off","off"},{"on","on","on"},{"t","on","on"},{"of","off","off"},{"1","on","on"},{"0","off","off"}}
	}
}
type walRejectedValue struct { input, message, hint string }
func walRejected(name string) []walRejectedValue {
	invalid := func(input,hint string) walRejectedValue { return walRejectedValue{input,"invalid value for parameter \""+name+"\": \""+input+"\"",hint} }
	switch name {
	case "commit_delay": return []walRejectedValue{{"100001","100001 is outside the valid range for parameter \"commit_delay\" (0 .. 100000)",""},{"-1","-1 is outside the valid range for parameter \"commit_delay\" (0 .. 100000)",""},invalid("1ms","")}
	case "commit_siblings": return []walRejectedValue{{"1001","1001 is outside the valid range for parameter \"commit_siblings\" (0 .. 1000)",""},invalid("not-an-int","")}
	case "wal_skip_threshold": return []walRejectedValue{{"-1","-1 kB is outside the valid range for parameter \"wal_skip_threshold\" (0 kB .. 2147483647 kB)",""},invalid("1mb","Valid units for this parameter are \"B\", \"kB\", \"MB\", \"GB\", and \"TB\"."),invalid("2147483648","Value exceeds integer range.")}
	case "synchronous_commit": return []walRejectedValue{invalid("o","Available values: local, remote_write, remote_apply, on, off."),invalid(" on ","Available values: local, remote_write, remote_apply, on, off.")}
	case "wal_compression": return []walRejectedValue{invalid("o","Available values: pglz, lz4, zstd, on, off."),invalid("brotli","Available values: pglz, lz4, zstd, on, off."),invalid(" on ","Available values: pglz, lz4, zstd, on, off.")}
	default: return []walRejectedValue{{"o","parameter \""+name+"\" requires a Boolean value",""}}
	}
}
