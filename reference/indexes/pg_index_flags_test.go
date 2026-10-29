package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func pgIndexPartialSeed(run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE pgi_partial(n INT,s TEXT,active BOOL)",0))
	run(fixture.Exec("CREATE UNIQUE INDEX pgi_partial_u ON pgi_partial(n) WHERE active",0))
	run(fixture.Exec("CREATE INDEX pgi_partial_expr ON pgi_partial(lower(s),(n+1)) WHERE active",0))
	run(fixture.Exec("INSERT INTO pgi_partial VALUES(1,'one',true),(1,'off',false),(1,'off',false)",3))
}

func pgIndexPartialCheck(run func(fixture.Step)) {
	unique := pgIndexOrdinary(1,1,"1","0","1978","0",nil,"active")
	unique[4] = true
	pgIndexBoth(run,pgIndexLogical("pgi_partial_u",[][]any{unique}))
	pgIndexBoth(run,pgIndexLogical("pgi_partial_expr",[][]any{pgIndexOrdinary(2,2,"0 0","100 0","3126 1978","0 0","lower(s), (n + 1)","active")}))
	pgIndexBoth(run,fixture.Select(`SELECT indexprs IS NULL AS no_expr,indpred IS NULL AS no_pred FROM pg_index WHERE indexrelid='pgi_partial_expr'::regclass`,[]string{"no_expr","no_pred"},[]string{"BOOL","BOOL"},[][]any{{false,false}}))
	for _, prepared := range []bool{false,true} {
		s := fixture.ErrorDiagnostic("INSERT INTO pgi_partial VALUES(2,'prefix',true),(1,'bad',true)",fixture.UniqueReferenceDuplicate("pgi_partial","pgi_partial_u","Key (n)=(1) already exists."))
		s.Prepared, s.ErrorStage = prepared,"execute"
		run(s)
	}
	pgIndexBoth(run,fixture.Select("SELECT n,s,active FROM pgi_partial ORDER BY active,s",[]string{"n","s","active"},[]string{"INT4","TEXT","BOOL"},[][]any{{int64(1),"off",false},{int64(1),"off",false},{int64(1),"one",true}}))
}

func TestReferencePGIndexFlags(t *testing.T) {
	for _, tc := range []struct {name, declaration string; unique, nulls, primary, exclusion bool}{
		{"plain","",false,false,false,false},
		{"unique","",true,false,false,false},
		{"nulls","",true,true,false,false},
		{"unique_constraint","CONSTRAINT pgi_flag_ix UNIQUE(n)",true,false,false,false},
		{"primary","CONSTRAINT pgi_flag_ix PRIMARY KEY(n)",true,false,true,false},
		{"exclusion","CONSTRAINT pgi_flag_ix EXCLUDE USING btree(n WITH =)",false,false,false,true},
	} {
		t.Run(tc.name,func(t *testing.T) {
			fixture.ConcurrentIndexScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
				c := fixture.ConcurrentIndexConn(t,p,b)
				run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,b.Recorder,s) }
				decl := ""; if tc.declaration != "" { decl = ","+tc.declaration }
				run(fixture.Exec("CREATE TABLE pgi_flags(n INT,payload TEXT"+decl+")",0))
				if tc.declaration == "" {
					prefix,suffix := "",""
					if tc.unique { prefix = "UNIQUE " }
					if tc.nulls { suffix = " NULLS NOT DISTINCT" }
					run(fixture.Exec("CREATE "+prefix+"INDEX pgi_flag_ix ON pgi_flags(n) INCLUDE(payload)"+suffix,0))
				}
				natts,key := int64(2),"1 2"; if tc.declaration != "" { natts,key = 1,"1" }
				row := pgIndexOrdinary(natts,1,key,"0","1978","0",nil,nil)
				row[4],row[5],row[6],row[7] = tc.unique,tc.nulls,tc.primary,tc.exclusion
				s := pgIndexLogical("pgi_flag_ix",[][]any{row})
				stmt,err := c.PrepareContext(p.Ctx,s.Query); if err != nil { t.Fatal(err) }; defer stmt.Close()
				held := fixture.HeldStatement{Executor:c,Statement:stmt,SQL:s.Query}
				run(s); fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
				if tc.declaration != "" {
					pgIndexBoth(run,fixture.Select(`SELECT conindid='pgi_flag_ix'::regclass AS linked,conrelid='pgi_flags'::regclass AS parent FROM pg_constraint WHERE conname='pgi_flag_ix'`,[]string{"linked","parent"},[]string{"BOOL","BOOL"},[][]any{{true,true}}))
				}
				run(fixture.Exec("INSERT INTO pgi_flags VALUES(1,'first')",1))
				for _, prepared := range []bool{false,true} {
					var fail fixture.Step
					switch {
					case tc.unique: fail = fixture.ErrorDiagnostic("INSERT INTO pgi_flags VALUES(2,'prefix'),(1,'different payload')",fixture.UniqueReferenceDuplicate("pgi_flags","pgi_flag_ix","Key (n)=(1) already exists."))
					case tc.exclusion: fail = fixture.ErrorDiagnostic("INSERT INTO pgi_flags VALUES(2,'prefix'),(1,'conflict')",fixture.ExclusionReferenceConflict("pgi_flags","pgi_flag_ix","n","1","1"))
					default: continue
					}
					fail.Prepared,fail.ErrorStage = prepared,"execute"; run(fail)
				}
				if tc.primary {
					pgIndexBoth(run,fixture.ErrorDiagnostic("INSERT INTO pgi_flags VALUES(NULL,'null')",fixture.PrimaryKeyReferenceNull("pgi_flags","n","null, null")))
				} else {
					run(fixture.Exec("INSERT INTO pgi_flags VALUES(NULL,'null')",1))
					if tc.nulls { pgIndexBoth(run,fixture.ErrorDiagnostic("INSERT INTO pgi_flags VALUES(NULL,'second')",fixture.UniqueReferenceDuplicate("pgi_flags","pgi_flag_ix","Key (n)=(null) already exists."))) } else { run(fixture.Exec("INSERT INTO pgi_flags VALUES(NULL,'second')",1)) }
				}
				run(fixture.Exec("INSERT INTO pgi_flags VALUES(3,'disjoint')",1))
				values := [][]any{{int64(1),"first"},{int64(3),"disjoint"}}
				if tc.name == "plain" {
					run(fixture.Exec("INSERT INTO pgi_flags VALUES(1,'duplicate')",1))
					values = [][]any{{int64(1),"duplicate"},{int64(1),"first"},{int64(3),"disjoint"}}
				}
				pgIndexBoth(run,fixture.Select("SELECT n,payload FROM pgi_flags WHERE n IS NOT NULL ORDER BY n,payload",[]string{"n","payload"},[]string{"INT4","TEXT"},values))
				nulls := int64(2)
				if tc.primary { nulls = 0 } else if tc.nulls { nulls = 1 }
				pgIndexBoth(run,fixture.Select("SELECT count(*) FROM pgi_flags WHERE n IS NULL",[]string{"count"},[]string{"INT8"},[][]any{{nulls}}))
				fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
			})
		})
	}
	for _, kind := range []string{"UNIQUE(n)","PRIMARY KEY(n)","EXCLUDE USING btree(n WITH =)"} {
		for _, mode := range []string{"IMMEDIATE","DEFERRED"} {
			t.Run(kind+"/"+mode,func(t *testing.T) {
				fixture.ConcurrentIndexScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
					c := fixture.ConcurrentIndexConn(t,p,b)
					run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,b.Recorder,s) }
					run(fixture.Exec("CREATE TABLE pgi_deferred(id INT,n INT,CONSTRAINT pgi_deferred_ix "+kind+" DEFERRABLE INITIALLY "+mode+")",0))
					row := pgIndexOrdinary(1,1,"2","0","1978","0",nil,nil)
					row[4],row[6],row[7],row[8] = kind != "EXCLUDE USING btree(n WITH =)",kind == "PRIMARY KEY(n)",kind == "EXCLUDE USING btree(n WITH =)",false
					s := pgIndexLogical("pgi_deferred_ix",[][]any{row})
					stmt,err := c.PrepareContext(p.Ctx,s.Query); if err != nil { t.Fatal(err) }; defer stmt.Close()
					held := fixture.HeldStatement{Executor:c,Statement:stmt,SQL:s.Query}
					run(s)
					for _,q := range []string{"BEGIN","SET CONSTRAINTS pgi_deferred_ix DEFERRED"} { run(fixture.Exec(q,0)) }
					run(fixture.Exec("INSERT INTO pgi_deferred VALUES(1,7),(2,7)",2))
					fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
					run(fixture.Exec("DELETE FROM pgi_deferred WHERE id=2",1))
					run(fixture.Exec("SET CONSTRAINTS pgi_deferred_ix IMMEDIATE",0))
					fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
					run(fixture.Exec("COMMIT",0)); run(s)
				})
			})
		}
	}
	t.Run("partial",func(t *testing.T) { fixture.ConcurrentIndexScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s) }; pgIndexPartialSeed(run); pgIndexPartialCheck(run)
	}) })
	t.Run("clustering",func(t *testing.T) { fixture.ConcurrentIndexScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
		a,peer := fixture.LockConnections(t,p.Ctx,b.DB)
		run := transactionalIndexRun(t,p,b,a); other := transactionalIndexRun(t,p,b,peer)
		for _,q := range []string{"CREATE TABLE pgi_cluster(n INT)","CREATE INDEX pgi_cluster_a ON pgi_cluster(n)","CREATE INDEX pgi_cluster_b ON pgi_cluster(n DESC)","ALTER TABLE pgi_cluster CLUSTER ON pgi_cluster_a"} { run(fixture.Exec(q,0)) }
		check := func(apply func(fixture.Step), first,second bool) { pgIndexBoth(apply,fixture.Select(`SELECT c.relname,i.indisclustered,i.indcheckxmin,i.indisreplident FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid WHERE c.relname IN ('pgi_cluster_a','pgi_cluster_b') ORDER BY c.relname`,[]string{"relname","indisclustered","indcheckxmin","indisreplident"},[]string{"NAME","BOOL","BOOL","BOOL"},[][]any{{"pgi_cluster_a",first,false,false},{"pgi_cluster_b",second,false,false}})) }
		check(run,true,false)
		for _,q := range []string{"BEGIN","SAVEPOINT mark","ALTER TABLE pgi_cluster CLUSTER ON pgi_cluster_b"} { run(fixture.Exec(q,0)) }
		check(run,false,true); check(other,true,false)
		run(fixture.Exec("ROLLBACK TO mark",0)); check(run,true,false)
		run(fixture.Exec("ALTER TABLE pgi_cluster SET WITHOUT CLUSTER",0)); check(run,false,false)
		run(fixture.Exec("ROLLBACK",0)); check(run,true,false)
		run(fixture.Exec("ALTER TABLE pgi_cluster CLUSTER ON pgi_cluster_b",0)); check(run,false,true)
		run(fixture.Exec("ALTER TABLE pgi_cluster SET WITHOUT CLUSTER",0)); check(run,false,false)
	}) })
}
