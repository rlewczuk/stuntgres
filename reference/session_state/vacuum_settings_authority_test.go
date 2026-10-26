package reference_test

import (
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceVacuumSettingsAuthority(t *testing.T) {
 physicalCases(t,[]string{"vacuum_authority_login","vacuum_authority_reader"},func(s *physicalCase) {
  s.run(fixture.Exec("CREATE ROLE vacuum_authority_login LOGIN PASSWORD 'role-core-fixture'",0))
  s.run(fixture.Exec("CREATE ROLE vacuum_authority_reader",0))
  s.vacuumLogicalSetup("vacuum_authority_login")
  ordinary:=s.login("vacuum_authority_login"); peer:=s.lockPeer()
  for _,prepared:=range []bool{false,true} {
   ordinary.vacuumInventory(prepared,false)
   for _,m:=range vacuumMutations {
    ordinary.vacuumAssign(m,prepared,false); ordinary.vacuumAssign(m,prepared,true)
    d:=vacuumDefinition(m.name); peer.vacuumValue(d.name,d.show,d.raw,"default",prepared)
    ordinary.vacuumLogicalRows(false); peer.vacuumLogicalRows(false)
   }
   ordinary.vacuumLogicalErrors(prepared)
   ordinary.run(fixture.Exec("RESET ALL",0))
  }
  s.run(fixture.Exec("GRANT pg_read_all_settings TO vacuum_authority_reader",0))
  s.run(fixture.Exec("GRANT vacuum_authority_reader TO vacuum_authority_login WITH INHERIT TRUE",0))
  ordinary.vacuumInventory(true,true)
  s.run(fixture.Exec("REVOKE vacuum_authority_reader FROM vacuum_authority_login",0))
  ordinary.vacuumInventory(false,false)
  ordinary.run(fixture.Exec("INSERT INTO public.vacuum_child VALUES(2,1,2)",1))
  ordinary.vacuumLogicalRows(true); peer.vacuumLogicalRows(true)
 })
}

func (s *physicalCase) vacuumLogicalSetup(login string) {
 s.run(fixture.Exec("CREATE TABLE public.vacuum_parent(id integer PRIMARY KEY)",0))
 s.run(fixture.Exec("CREATE TABLE public.vacuum_child(id integer PRIMARY KEY, parent_id integer REFERENCES public.vacuum_parent(id), v integer CONSTRAINT vacuum_child_positive CHECK(v>0))",0))
 s.run(fixture.Exec("INSERT INTO public.vacuum_parent VALUES(1)",1))
 s.run(fixture.Exec("INSERT INTO public.vacuum_child VALUES(1,1,7)",1))
 s.run(fixture.Exec("GRANT USAGE ON SCHEMA public TO "+login,0))
 s.run(fixture.Exec("GRANT SELECT,INSERT ON public.vacuum_parent,public.vacuum_child TO "+login,0))
}
func (s *physicalCase) vacuumLogicalRows(extra bool) {
 s.t.Helper()
 rows:=[][]any{{int64(1),int64(1),int64(7)}}
 if extra { rows=append(rows,[]any{int64(2),int64(1),int64(2)}) }
 s.run(fixture.Select("SELECT id,parent_id,v FROM public.vacuum_child ORDER BY id",[]string{"id","parent_id","v"},[]string{"INT4","INT4","INT4"},rows))
 s.run(fixture.Select("SELECT id FROM public.vacuum_parent ORDER BY id",[]string{"id"},[]string{"INT4"},[][]any{{int64(1)}}))
}
func (s *physicalCase) vacuumLogicalErrors(prepared bool) {
 duplicate:=physicalDiagnostic("23505",`duplicate key value violates unique constraint "vacuum_child_pkey"`)
 duplicate.Detail="Key (id)=(1) already exists."
 duplicate.Schema,duplicate.Table,duplicate.Constraint="public","vacuum_child","vacuum_child_pkey"
 check:=physicalDiagnostic("23514",`new row for relation "vacuum_child" violates check constraint "vacuum_child_positive"`)
 check.Detail="Failing row contains (3, 1, -1)."
 check.Schema,check.Table,check.Constraint="public","vacuum_child","vacuum_child_positive"
 foreign:=physicalDiagnostic("23503",`insert or update on table "vacuum_child" violates foreign key constraint "vacuum_child_parent_id_fkey"`)
 foreign.Detail=`Key (parent_id)=(99) is not present in table "vacuum_parent".`
 foreign.Schema,foreign.Table,foreign.Constraint="public","vacuum_child","vacuum_child_parent_id_fkey"
 for _,tc:=range []struct{query string; d fixture.Diagnostic}{
  {"INSERT INTO public.vacuum_child VALUES(2,1,2),(1,1,9)",duplicate},
  {"INSERT INTO public.vacuum_child VALUES(2,1,2),(3,1,-1)",check},
  {"INSERT INTO public.vacuum_child VALUES(2,1,2),(4,99,4)",foreign},
 } { s.run(physicalError(tc.query,tc.d,prepared,false)); s.vacuumLogicalRows(false) }
}
