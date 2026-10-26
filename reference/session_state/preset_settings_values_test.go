package reference_test

import (
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePresetSettingsInventory(t *testing.T) {
	physicalCases(t,nil,func(s *physicalCase) {
		values := s.presetInventory(false)
		if got := s.presetInventory(true); !reflect.DeepEqual(got,values) { t.Fatal(got,values) }
		for _, prepared := range []bool{false,true} {
			s.presetReads(values,prepared)
			for _, d := range presetDefinitions {
				step := fixture.Select("SHOW \""+strings.ToUpper(d.name)+"\"",[]string{d.name},[]string{"TEXT"},[][]any{{values[d.name]}}); step.Prepared = prepared; s.run(step)
			}
			q := "SHOW ALL"
			stmt, err := s.conn.PrepareContext(s.p.Ctx,q); if err != nil { t.Fatal(err) }
			rows, err := s.conn.QueryContext(s.p.Ctx,q)
			if prepared { if rows != nil { rows.Close() }; rows,err = stmt.QueryContext(s.p.Ctx) }
			if err != nil { t.Fatal(err) }
			seen := map[string]bool{}
			for rows.Next() {
				var name,value,description string; if err := rows.Scan(&name,&value,&description); err != nil { t.Fatal(err) }
				for _, d := range presetDefinitions { if name == d.name { if seen[name] || value != values[name] || description != d.description { t.Fatal(name,value,description) }; seen[name] = true } }
			}
			if err := rows.Err(); err != nil || len(seen) != 20 { t.Fatal(seen,err) }; rows.Close(); stmt.Close()
			step := fixture.Select("SELECT current_setting('preset_unknown',true)",[]string{"current_setting"},[]string{"TEXT"},[][]any{{nil}}); step.Prepared = prepared; s.run(step)
			step = physicalError("SELECT current_setting('preset_unknown')",physicalDiagnostic("42704",`unrecognized configuration parameter "preset_unknown"`),prepared,true); s.run(step)
			step = physicalError("SHOW preset_unknown",physicalDiagnostic("42704",`unrecognized configuration parameter "preset_unknown"`),prepared,true)
			if prepared { step.ErrorStage = "prepare" }; s.run(step)
			step = physicalError("SELECT current_setting('preset_unknown',false)",physicalDiagnostic("42704",`unrecognized configuration parameter "preset_unknown"`),prepared,true); s.run(step)
		}
	})
}

func TestReferencePresetSettingsContext(t *testing.T) {
	t.Run("local-notice-and-binder",func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, backend := range p.Backends() {
			var notices []fixture.Diagnostic
			_,conn,recorder := fixture.NoticeReferencePool(t,p,backend.PG,&notices)
			for _, d := range presetDefinitions {
				for _, prepared := range []bool{false,true} {
					before := len(notices)
					fixture.RunStep(t,p.Ctx,conn,recorder,physicalError("SET LOCAL "+d.name+"='bogus'",presetDenied(d.name),prepared,false))
					want := fixture.Diagnostic{Severity:"WARNING",SeverityUnlocalized:"WARNING",Code:"25P01",Message:"SET LOCAL can only be used in transaction blocks"}
					if len(notices) != before+1 || notices[before] != want { t.Fatal(notices) }
					bad := physicalDiagnostic("22P02",`invalid input syntax for type boolean: "maybe"`)
					bad.Context = "unnamed portal parameter $3 = '...'"
					fixture.RunStep(t,p.Ctx,conn,recorder,physicalError("SELECT set_config($1,$2,$3)",bad,prepared,true,d.name,"bogus","maybe"))
				}
			}
			for _, prepared := range []bool{false,true} {
				bad := physicalDiagnostic("42601",`syntax error at or near ")"`)
				bad.Position = fixture.Position(38)
				step := physicalError("SELECT set_config('segment_size','v',)",bad,prepared,true)
				if prepared { step.ErrorStage = "prepare" }
				fixture.RunStep(t,p.Ctx,conn,recorder,step)
			}
		}
	})
	for _, def := range presetDefinitions {
		t.Run(def.name,func(t *testing.T) {
			physicalCases(t,nil,func(s *physicalCase) {
				values := s.presetInventory(false)
				s.run(fixture.Exec("CREATE TABLE preset_sentinel (id integer PRIMARY KEY, value text)",0))
				s.run(fixture.Exec("INSERT INTO preset_sentinel VALUES (1,'kept'),(2,NULL)",2))
				s.run(fixture.Exec("SET application_name='preset-owner'",0))
				peer, err := s.connPoolPeer(); if err != nil { t.Fatal(err) }; defer peer.conn.Close()
				peer.run(fixture.Exec("SET application_name='preset-peer'",0))
				alternate := "alternate"
				switch def.kind { case "integer": alternate = "1"; if def.min == def.max { alternate = def.current }; case "bool": alternate = "on"; if def.current == "on" { alternate = "off" }; case "enum": alternate = "on" }
				for _, prepared := range []bool{false,true} {
					for _, input := range []string{values[def.name],alternate,"bogus unit/type"} {
						s.run(physicalError("SET "+def.name+"="+physicalLiteral(input),presetDenied(def.name),prepared,false))
						s.presetUnchanged(values,"preset-owner")
					}
					for _, q := range []string{"SET "+def.name+"=DEFAULT","RESET "+def.name,"SET \""+strings.ToUpper(def.name)+"\"='bogus'"} { s.run(physicalError(q,presetDenied(def.name),prepared,false)); s.presetUnchanged(values,"preset-owner") }
					for _, local := range []any{false,true,nil} { for _, value := range []any{values[def.name],alternate,"bogus unit/type",nil} {
						s.run(physicalError("SELECT set_config($1,$2,$3)",presetDenied(def.name),prepared,true,def.name,value,local)); s.presetUnchanged(values,"preset-owner")
					} }
					upper := strings.ToUpper(def.name)
					s.run(physicalError("SET \""+upper+"\"='a','b'",physicalDiagnostic("22023","SET "+upper+" takes only one argument"),prepared,false))
					s.run(physicalError("SELECT set_config(NULL,'x',false)",physicalDiagnostic("22004","SET requires parameter name"),prepared,true))
				}
				for _, q := range []string{"SET "+def.name+"='bogus'","RESET "+def.name,"SELECT set_config('"+def.name+"',NULL,false)"} {
					stmt,err := s.conn.PrepareContext(s.p.Ctx,q); if err != nil { t.Fatal(err) }
					s.presetUnchanged(values,"preset-owner")
					for range 2 { fixture.RunStep(t,s.p.Ctx,fixture.HeldStatement{Executor:s.conn,Statement:stmt,SQL:q},s.recorder,physicalError(q,presetDenied(def.name),false,strings.HasPrefix(q,"SELECT"))) }; stmt.Close()
				}
				s.run(fixture.Exec("BEGIN",0))
				for _, prepared := range []bool{false,true} {
					s.run(fixture.Exec("SAVEPOINT preset_error",0))
					s.run(physicalError("SET LOCAL "+def.name+"='bogus'",presetDenied(def.name),prepared,false))
					s.run(physicalError("SHOW "+def.name,physicalDiagnostic("25P02","current transaction is aborted, commands ignored until end of transaction block"),false,true))
					s.run(fixture.Exec("ROLLBACK TO SAVEPOINT preset_error",0)); s.presetUnchanged(values,"preset-owner")
					s.run(fixture.Exec("RELEASE SAVEPOINT preset_error",0))
				}
				s.run(fixture.Exec("COMMIT",0)); s.run(fixture.Exec("BEGIN",0)); s.run(fixture.Exec("ROLLBACK",0))
				s.run(fixture.Exec("RESET ALL",0)); s.presetUnchanged(values,""); peer.presetUnchanged(values,"preset-peer")
				duplicate := physicalDiagnostic("23505",`duplicate key value violates unique constraint "preset_sentinel_pkey"`)
				duplicate.Detail, duplicate.Schema, duplicate.Table, duplicate.Constraint = "Key (id)=(1) already exists.", "public", "preset_sentinel", "preset_sentinel_pkey"
				s.run(physicalError("INSERT INTO preset_sentinel VALUES (1,'duplicate')",duplicate,false,false))
			})
		})
	}
}
