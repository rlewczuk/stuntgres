package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func alterSystemControls(t *testing.T,s *alterSystemState) {
	t.Helper()
	if s.b.PG { return }
	held := loadHeld(t,s.p,s.a,"ALTER SYSTEM RESET ALL")
	var dump bytes.Buffer
	if err := stuntgres.Dump(s.p.Ctx,s.p.Native,&dump); err != nil { t.Fatal(err) }
	text := dump.String()
	if !strings.Contains(text,"alter_system_sentinel") || !strings.Contains(text,"INSERT") || strings.Contains(text,"ALTER SYSTEM") || strings.Contains(text,"alter-system-a") || strings.Contains(text,"alter-system-b") || strings.Contains(text,"external") { t.Fatalf("unexpected logical dump: %s",text) }
	if err := stuntgres.Reset(s.p.Ctx,s.p.Native); err != nil { t.Fatal(err) }
	s.failure(t,s.a,"SELECT v FROM alter_system_sentinel",func() fixture.Diagnostic {
		d := loadDiagnostic("42P01",`relation "alter_system_sentinel" does not exist`)
		d.Position = "15"
		return d
	}(),false,true,false)
	for _,item := range []struct{ held fixture.HeldStatement; value string }{{s.showA,"alter-system-a"},{s.showB,"alter-system-b"}} {
		fixture.RunStep(t,s.p.Ctx,item.held,s.b.Recorder,fixture.Select("SHOW application_name",[]string{"application_name"},[]string{"TEXT"},[][]any{{item.value}}))
	}
	if err := stuntgres.Restore(s.p.Ctx,s.p.Native,bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
	s.unchanged(t)
	connector,err := stuntgres.NewDriver().OpenConnector("alter-system-fresh")
	if err != nil { t.Fatal(err) }
	fresh := sql.OpenDB(connector)
	defer fresh.Close()
	if err := stuntgres.Restore(s.p.Ctx,fresh,bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
	recordedFresh, freshRecorder := fixture.RecordedNativePool(t, connector)
	fixture.RunStep(t,s.p.Ctx,recordedFresh,freshRecorder,fixture.Select("SELECT v FROM alter_system_sentinel",[]string{"v"},[]string{"INT4"},[][]any{{int64(7)}}))
	fixture.RunStep(t,s.p.Ctx,recordedFresh,freshRecorder,fixture.Select("SHOW application_name",[]string{"application_name"},[]string{"TEXT"},[][]any{{""}}))
	s.failure(t,held,held.SQL,loadDiagnostic("0A000",alterSystemMessage),false,false,false)
	err = stuntgres.Restore(s.p.Ctx,s.p.Native,strings.NewReader("CREATE TABLE public.alter_system_restore_leak(v integer); ALTER SYSTEM RESET ALL;"))
	want := loadDiagnostic("0A000","restore accepts only CREATE/ALTER/DROP SCHEMA, CREATE TABLE/SEQUENCE/VIEW/INDEX/POLICY/RULE, materialized extension dependency ALTER, row-security/existing-index/eligible NOT VALID ALTER TABLE, INSERT, deferred reference DEFAULT, database-local COMMENT, generated sequence ownership/state and SET SESSION AUTHORIZATION")
	fixture.AssertDiagnostic(t,err,&want)
	s.unchanged(t)
	missing := loadDiagnostic("42P01",`relation "alter_system_restore_leak" does not exist`)
	missing.Position = "15"
	s.failure(t,s.a,"SELECT v FROM alter_system_restore_leak",missing,false,true,false)
}

func TestReferenceAlterSystemControls(t *testing.T) {
	alterSystemScenario(t,func(t *testing.T,s *alterSystemState) {
		for _,q := range alterSystemCommands[:4] { s.failure(t,s.a,q,loadDiagnostic("0A000",alterSystemMessage),false,false,false); s.unchanged(t) }
		alterSystemControls(t,s)
	})
}

func TestReferenceAlterSystemFeatureSmoke(t *testing.T) {
	alterSystemScenario(t,func(t *testing.T,s *alterSystemState) {
		check := alterSystemHost(t,s)
		held := loadHeld(t,s.p,s.a,"ALTER SYSTEM RESET ALL")
		s.unchanged(t)
		s.failure(t,s.a,"ALTER SYSTEM SET work_mem = 1.25",loadDiagnostic("0A000",alterSystemMessage),false,false,false)
		s.failure(t,held,held.SQL,loadDiagnostic("0A000",alterSystemMessage),false,true,false)
		s.exec(t,"BEGIN READ ONLY")
		s.failure(t,held,held.SQL,loadDiagnostic("25001","ALTER SYSTEM cannot run inside a transaction block"),false,false,false)
		s.exec(t,"ROLLBACK")
		s.unchanged(t)
		alterSystemControls(t,s)
		check()
	})
}
