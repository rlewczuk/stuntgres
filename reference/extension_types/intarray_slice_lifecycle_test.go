package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func intarraySliceDefaults(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO ia_slice_rows(id,a) VALUES (4,'{22,9}')",1))
	intarrayBoth(t,p,b,c,intarraySelect(b,"SELECT d,s FROM ia_slice_rows WHERE id=4",[]string{"d","s"},[]string{"_INT4","_INT4"},[]bool{true,true},[][]any{{"{-7}","{22,33,22}"}}))
	fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("DELETE FROM ia_slice_rows WHERE id=4",1))
	for _,prepared := range []bool{false,true} {
		intarrayError(t,p,b,c,"INSERT INTO ia_slice_rows(id,a) VALUES (10,'{22}'),(11,'{NULL}')",prepared,"execute",intarraySetNullError())
	}
	intarrayBoth(t,p,b,c,intarraySliceStoredCheck(b))
}

func intarraySlicePresentation(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
	t.Helper()
	var held []fixture.HeldStatement
	for _,column := range []string{"n","i","tail","span","singleton","counted","found"} {
		q := "SELECT "+column+" AS v FROM ia_slice_view WHERE id=$1::integer"
		stmt,err := c.PrepareContext(p.Ctx,q); if err != nil { t.Fatal(err) }; defer stmt.Close()
		held = append(held,fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q})
	}
	check := func() {
		intarrayBoth(t,p,b,c,intarraySliceStoredCheck(b))
		for i,h := range held {
			family := "INT4"; want := any(int64(4))
			switch i { case 1,6: want = int64(2); case 2: family,want = "_INT4","{22,33,22}"; case 3: family,want = "_INT4","{22,33}"; case 4: family,want = "_INT4","{1}" }
			s := intarraySelect(b,h.SQL,[]string{"v"},[]string{family},[]bool{true},[][]any{{want}},int64(1))
			fixture.RunStep(t,p.Ctx,h,b.Recorder,s)
		}
	}
	check()
	reader,err := b.DB.Conn(p.Ctx); if err != nil { t.Fatal(err) }; defer reader.Close()
	old := intarraySliceRetain(t,p,b,reader); defer old.rows.Close()
	run := func(q string) { fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(q,0)) }
	run("CREATE SCHEMA ia_collision")
	for _,tc := range intarraySliceCases[:5] {
		run("ALTER FUNCTION ia_provider."+tc.name+"("+strings.Join(tc.types,",")+") RENAME TO renamed_"+tc.name)
		check()
	}
	for _,signature := range []string{"NONE,integer[]","integer[],integer"} {
		run("ALTER OPERATOR ia_provider.# ("+signature+") SET SCHEMA ia_collision")
		check()
		run("ALTER OPERATOR ia_collision.# ("+signature+") SET SCHEMA ia_provider")
	}
	run("ALTER EXTENSION intarray SET SCHEMA ia_collision")
	check()
	intarraySliceConsume(t,b,old)
	for _,tc := range intarraySliceCases[:5] {
		q := strings.Replace(tc.expression(tc.literals(tc.args)),"ia_provider."+tc.name,"ia_collision.renamed_"+tc.name,1)
		intarrayBoth(t,p,b,c,intarraySliceResult(b,"SELECT "+q+" AS v",tc.family,tc.want))
	}
	intarrayBoth(t,p,b,c,intarraySliceResult(b,"SELECT OPERATOR(ia_collision.#) '{11,22,33,22}'::integer[] AS v","INT4",int64(4)))
	intarrayBoth(t,p,b,c,intarraySliceResult(b,"SELECT '{11,22,33,22}'::integer[] OPERATOR(ia_collision.#) 22 AS v","INT4",int64(2)))
	intarraySliceDefaults(t,p,b,c)
	run("ALTER EXTENSION intarray SET SCHEMA ia_provider")
	for _,tc := range intarraySliceCases[:5] { run("ALTER FUNCTION ia_provider.renamed_"+tc.name+"("+strings.Join(tc.types,",")+") RENAME TO "+tc.name) }
	check()
}

func TestReferenceIntarraySliceLifecycle(t *testing.T) {
	t.Run("stored_and_presentation",func(t *testing.T) {
		intarrayScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
			intarraySliceSeed(t,p,b,c)
			intarraySliceDefaults(t,p,b,c)
			intarraySlicePresentation(t,p,b,c)
		})
	})
	t.Run("detached_originals",func(t *testing.T) {
		intarrayScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
			intarraySliceSeed(t,p,b,c)
			run := func(q string) { fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(q,0)) }
			ids := intarraySliceCatalog(t,p,b,c,intarrayFixtureOwner(b))
			for _,q := range []string{"BEGIN","DROP EXTENSION intarray CASCADE","ROLLBACK"} { run(q) }
			for i,tc := range intarraySliceCases[:5] {
				q := "SELECT 'ia_provider."+tc.name+"("+strings.Join(tc.types,",")+")'::regprocedure::oid=$1::oid AS original"
				intarrayBoth(t,p,b,c,fixture.Select(q,[]string{"original"},[]string{"BOOL"},[][]any{{true}},ids[i]))
			}
			run("CREATE SCHEMA ia_collision")
			for _,signature := range []string{"NONE,integer[]","integer[],integer"} {
				run("ALTER EXTENSION intarray DROP OPERATOR ia_provider.# ("+signature+")")
				run("ALTER OPERATOR ia_provider.# ("+signature+") SET SCHEMA ia_collision")
			}
			for _,tc := range intarraySliceCases[:5] {
				signature := "("+strings.Join(tc.types,",")+")"
				run("ALTER EXTENSION intarray DROP FUNCTION ia_provider."+tc.name+signature)
				run("ALTER FUNCTION ia_provider."+tc.name+signature+" RENAME TO original_"+tc.name)
				run("ALTER FUNCTION ia_provider.original_"+tc.name+signature+" SET SCHEMA ia_collision")
			}
			run("DROP EXTENSION intarray CASCADE")
			intarrayBoth(t,p,b,c,intarraySliceStoredCheck(b))
			intarraySliceDefaults(t,p,b,c)
			run("CREATE EXTENSION intarray SCHEMA ia_provider VERSION '1.5'")
			intarrayBoth(t,p,b,c,intarraySliceStoredCheck(b))
			intarraySliceDefaults(t,p,b,c)
			for i,tc := range intarraySliceCases[:5] {
				signature := "("+strings.Join(tc.types,",")+")"
				q := "SELECT 'ia_collision.original_"+tc.name+signature+"'::regprocedure::oid=$1::oid AS original,'ia_provider."+tc.name+signature+"'::regprocedure::oid<>$1::oid AS distinct_installation"
				intarrayBoth(t,p,b,c,fixture.Select(q,[]string{"original","distinct_installation"},[]string{"BOOL","BOOL"},[][]any{{true,true}},ids[i]))
				expression := strings.Replace(tc.expression(tc.literals(tc.args)),"ia_provider."+tc.name,"ia_collision.original_"+tc.name,1)
				intarrayBoth(t,p,b,c,intarraySliceResult(b,"SELECT "+expression+" AS v",tc.family,tc.want))
				intarrayBoth(t,p,b,c,intarraySliceResult(b,"SELECT "+tc.expression(tc.literals(tc.args))+" AS v",tc.family,tc.want))
			}
			for i,signature := range []string{"NONE,integer[]","integer[],integer"} {
				backer := "icount(integer[])"; if i == 1 { backer = "idx(integer[],integer)" }
				q := "SELECT o.oid=$1::oid AS original,o.oprcode::oid='ia_collision.original_"+backer+"'::regprocedure::oid AS original_backer,o.oid<>'ia_provider.#("+signature+")'::regoperator::oid AS distinct_installation FROM pg_operator o WHERE o.oid='ia_collision.#("+signature+")'::regoperator::oid"
				intarrayBoth(t,p,b,c,fixture.Select(q,[]string{"original","original_backer","distinct_installation"},[]string{"BOOL","BOOL","BOOL"},[][]any{{true,true,true}},ids[5+i]))
			}
			intarrayBoth(t,p,b,c,intarraySliceResult(b,"SELECT OPERATOR(ia_collision.#) '{11,22,33,22}'::integer[] AS v","INT4",int64(4)))
			intarrayBoth(t,p,b,c,intarraySliceResult(b,"SELECT '{11,22,33,22}'::integer[] OPERATOR(ia_collision.#) 22 AS v","INT4",int64(2)))
			// Dropping the detached authority really removes its consumers. A NULL
			// call cannot resurrect it using the newly installed same-name recipe.
			q := "SELECT ia_collision.original_idx($1::integer[],$2::integer) AS v"
			stmt,err := c.PrepareContext(p.Ctx,q); if err != nil { t.Fatal(err) }; defer stmt.Close()
			held := fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
			fixture.RunStep(t,p.Ctx,held,b.Recorder,intarraySliceResult(b,q,"INT4",nil,nil,int64(22)))
			run("DROP FUNCTION ia_collision.original_idx(integer[],integer) CASCADE")
			want := fixture.ControlsReferenceDiagnostic("42883","function ia_collision.original_idx(integer[], integer) does not exist")
			want.Hint,want.Position = "No function matches the given name and argument types. You might need to add explicit type casts.","8"
			failure := fixture.ErrorDiagnostic(q,want,nil,int64(22)); failure.SelectRows,failure.ErrorStage = true,"execute"
			fixture.RunStep(t,p.Ctx,held,b.Recorder,failure)
			intarrayBoth(t,p,b,c,intarraySliceResult(b,"SELECT ia_provider.idx('{11,22,33,22}'::integer[],22) AS v","INT4",int64(2)))
			run("DROP EXTENSION intarray CASCADE")
			intarrayBoth(t,p,b,c,intarraySelect(b,"SELECT id,a FROM ia_slice_rows ORDER BY id",[]string{"id","a"},[]string{"INT4","_INT4"},[]bool{false,true},[][]any{{int64(1),intarraySliceA},{int64(2),"{}"},{int64(3),nil}}))
		})
	})
}
