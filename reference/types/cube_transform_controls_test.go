package reference_test

import (
	"bytes"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

const cubeTransformDefaultDDL = "CREATE TABLE cube_transform_defaults(id integer,s cube DEFAULT cube_subset('(1,3,5),(6,7,8)'::cube,'{3,2,1,1}'::integer[]),u cube DEFAULT cube_union('(1,2)'::cube,'(3,4)'::cube),i cube DEFAULT cube_inter('(1,2)'::cube,'(3,4)'::cube),e cube DEFAULT cube_enlarge('(1,2),(3,4)'::cube,0.5,3))"
const cubeTransformIdentities = "SELECT 'public.cube'::regtype::oid::bigint,'public.cube_subset(public.cube,integer[])'::regprocedure::oid::bigint,'public.cube_enlarge(public.cube,double precision,integer)'::regprocedure::oid::bigint"

func cubeTransformExpected(id int64) []any {
	return []any{id,"(5, 3, 1, 1),(8, 7, 6, 6)","(1, 2),(3, 4)","(3, 4),(1, 2)","(0.5, 1.5, -0.5),(3.5, 4.5, 0.5)"}
}

func cubeTransformTable(b fixture.Backend, table string, rows [][]any) fixture.Step {
	s := cubeConstructorSelect(b,"SELECT id,s,u,i,e FROM "+table+" ORDER BY id",[]string{"id","s","u","i","e"},[]string{"INT4","CUBE","CUBE","CUBE","CUBE"},rows)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		cubeMetadata(t,b.PG,rows,[]string{"id","s","u","i","e"},[]bool{false,true,true,true,true})
	}
	return s
}

func cubeTransformStoredSetup(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,b.Recorder,s) }
	cubeInstall(t,p,b,c)
	run(fixture.Exec("CREATE TABLE cube_transform_source(id integer NOT NULL,v cube)",0))
	run(fixture.Exec("INSERT INTO cube_transform_source VALUES(1,'(1,3,5),(6,7,8)'::cube),(2,NULL)",2))
	run(fixture.Exec("CREATE VIEW cube_transform_view AS SELECT id,cube_subset(v,'{3,2,1,1}'::integer[]) AS s,cube_union(v,'(0)'::cube) AS u,cube_inter(v,'(0)'::cube) AS i,cube_enlarge(v,0.5,4) AS e FROM cube_transform_source",0))
	run(fixture.Exec(cubeTransformDefaultDDL,0))
	run(fixture.Exec("INSERT INTO cube_transform_defaults(id) VALUES(1)",1))
	run(fixture.Exec("CREATE TABLE cube_transform_precision(id integer,v cube)",0))
	run(fixture.Exec("INSERT INTO cube_transform_precision VALUES(1,cube_subset('(0.10000000000000002)'::cube,'{1}'::integer[])),(2,cube_inter('(1,2)'::cube,'(3,4)'::cube)),(3,NULL),(4,cube_subset('(1,2),(1,3)'::cube,'{1}'::integer[])),(5,cube_subset('(1),(2)'::cube,'{}'::integer[])),(6,cube_subset('(NaN,1),(NaN,2)'::cube,'{1}'::integer[])),(7,cube_subset('(-0,1),(0,2)'::cube,'{1}'::integer[]))",7))
}

func cubeTransformVerifyStored(t *testing.T,p *fixture.Pair,b fixture.Backend,db fixture.Executor,rec *fixture.Recorder,owned bool) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,db,rec,s) }
	run(cubeTransformTable(b,"cube_transform_defaults",[][]any{cubeTransformExpected(1)}))
	view := cubeTransformTable(b,"cube_transform_view",[][]any{
		{int64(1),"(5, 3, 1, 1),(8, 7, 6, 6)","(0, 0, 0),(6, 7, 8)","(1, 3, 5),(0, 0, 0)","(0.5, 2.5, 4.5, -0.5),(6.5, 7.5, 8.5, 0.5)"},
		{int64(2),nil,nil,nil,nil},
	})
	run(view); run(fixture.Prepared(view))
	run(fixture.CubeSelect(b,"SELECT id,v FROM cube_transform_precision ORDER BY id",[]string{"id","v"},[][]any{{int64(1),"(0.10000000000000002)"},{int64(2),"(3, 4),(1, 2)"},{int64(3),nil},{int64(4),"(1)"},{int64(5),"()"},{int64(6),"(NaN),(NaN)"},{int64(7),"(-0)"}}))
	run(fixture.Select("SELECT id,cube_is_point(v) AS point FROM cube_transform_precision WHERE id>=4 ORDER BY id",[]string{"id","point"},[]string{"INT4","BOOL"},[][]any{{int64(4),true},{int64(5),true},{int64(6),false},{int64(7),true}}))
	run(fixture.Exec("INSERT INTO cube_transform_defaults(id) VALUES(2)",1))
	run(cubeTransformTable(b,"cube_transform_defaults",[][]any{cubeTransformExpected(1),cubeTransformExpected(2)}))
	run(fixture.Exec("DELETE FROM cube_transform_defaults WHERE id=2",1))
	for _, s := range cubeTransformCatalogChecks("public",owned) { run(s) }
	if owned { cubeTransformCheckOwners(t,p,db,rec) }
}

func cubeTransformPending(t *testing.T,p *fixture.Pair,db fixture.Executor) (*sql.Rows,[]*sql.ColumnType,[]any) {
	t.Helper()
	rows,err := db.QueryContext(p.Ctx,"SELECT id,s,u,i,e FROM cube_transform_defaults ORDER BY id")
	if err != nil { t.Fatal(err) }
	cols,err := rows.ColumnTypes()
	if err != nil { rows.Close(); t.Fatal(err) }
	return rows,cols,cubeSpatialMetadataSnapshot(t,cols)
}

func cubeTransformDrain(t *testing.T,rows *sql.Rows) {
	t.Helper()
	var got [][]any
	for rows.Next() {
		var id,s,u,i,e any
		if err := rows.Scan(&id,&s,&u,&i,&e); err != nil { t.Fatal(err) }
		got = append(got,[]any{id,s,u,i,e})
	}
	if err := rows.Err(); err != nil { t.Fatal(err) }
	if err := rows.Close(); err != nil { t.Fatal(err) }
	if !reflect.DeepEqual(got,[][]any{cubeTransformExpected(1)}) { t.Fatalf("retained rows %#v",got) }
}

func cubeTransformControlCycle(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,full bool) {
	t.Helper()
	if full {
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE ROLE cube_transform_owner",0))
		if b.PG {
			cubeTransformPGOwners(t,p,c,b.Recorder)
		} else {
			var original bytes.Buffer
			if err := stuntgres.Dump(p.Ctx,p.Native,&original); err != nil { t.Fatal(err) }
			if err := stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(cubeTransformOwnerScript(t,original.String()))); err != nil { t.Fatal(err) }
		}
	}
	cubeTransformVerifyStored(t,p,b,c,b.Recorder,full)
	if b.PG {
		for _, q := range []string{"DROP VIEW cube_transform_view","DROP TABLE cube_transform_defaults","DROP TABLE cube_transform_precision","DROP TABLE cube_transform_source","DROP EXTENSION cube"} {
			fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(q,0))
		}
		if full { fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("DROP ROLE cube_transform_owner",0)) }
		cubeTransformStoredSetup(t,p,b,c)
		if full {
			fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE ROLE cube_transform_owner",0))
			cubeTransformPGOwners(t,p,c,b.Recorder)
		}
		cubeTransformVerifyStored(t,p,b,c,b.Recorder,full)
		return
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx,p.Native,&dump); err != nil { t.Fatal(err) }
	text := dump.String()
	if ext,table := strings.Index(text,"CREATE EXTENSION"),strings.Index(text,"CREATE TABLE"); ext < 0 || table <= ext || strings.Contains(text,"CREATE FUNCTION") || strings.Contains(text,"CREATE OPERATOR") || strings.Contains(text,"CREATE TYPE") { t.Fatal("extension dump ordering",text) }
	old,cols,metadata := cubeTransformPending(t,p,c)
	defer old.Close()
	if err := stuntgres.Reset(p.Ctx,p.Native); err != nil { t.Fatal(err) }
	if err := stuntgres.Restore(p.Ctx,p.Native,bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
	cubeTransformVerifyStored(t,p,b,c,b.Recorder,full)
	cubeTransformDrain(t,old)
	if !reflect.DeepEqual(cubeSpatialMetadataSnapshot(t,cols),metadata) { t.Fatal("retained metadata changed") }
	if !full { return }
	var source [3]int64
	if err := c.QueryRowContext(p.Ctx,cubeTransformIdentities).Scan(&source[0],&source[1],&source[2]); err != nil { t.Fatal(err) }
	fresh := fixture.NewFixture(t,false,true)
	owner,err := fresh.Memory.Conn(fresh.Ctx)
	if err != nil { t.Fatal(err) }
	defer owner.Close()
	cubeSpatialRetainCollisions(t,fresh,owner,source)
	fixture.RunStep(t,fresh.Ctx,fresh.Memory,fresh.MemoryRecord,fixture.Exec("CREATE ROLE cube_transform_owner",0))
	if err := stuntgres.Restore(fresh.Ctx,fresh.Native,bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
	var destination [3]int64
	if err := fresh.Memory.QueryRowContext(fresh.Ctx,cubeTransformIdentities).Scan(&destination[0],&destination[1],&destination[2]); err != nil { t.Fatal(err) }
	for i,id := range destination { if id <= 0 || id == source[i] { t.Fatal("unreminted identity",source,destination) } }
	cubeTransformVerifyStored(t,fresh,b,fresh.Memory,fresh.MemoryRecord,true)
	pending,oldCols,oldMetadata := cubeTransformPending(t,fresh,fresh.Memory)
	defer pending.Close()
	bad := "CREATE EXTENSION cube; CREATE TABLE staged(v cube DEFAULT cube_union('1'::cube,'2'::cube)); INSERT INTO staged VALUES('1'::cube); INSERT INTO staged VALUES(cube_union('1'::cube,'2'::cube));"
	err = stuntgres.Restore(fresh.Ctx,fresh.Native,strings.NewReader(bad))
	if err == nil || err.Error() != "stuntgres: invalid restore scalar" { t.Fatal("Restore expression boundary",err) }
	var state interface{ SQLState() string }
	if errors.As(err,&state) { t.Fatal("ordinary Restore error acquired SQLSTATE",err) }
	cubeTransformVerifyStored(t,fresh,b,fresh.Memory,fresh.MemoryRecord,true)
	cubeTransformDrain(t,pending)
	if !reflect.DeepEqual(cubeSpatialMetadataSnapshot(t,oldCols),oldMetadata) { t.Fatal("failed Restore changed metadata") }
	var again bytes.Buffer
	if err := stuntgres.Dump(fresh.Ctx,fresh.Native,&again); err != nil { t.Fatal(err) }
	if !bytes.Equal(dump.Bytes(),again.Bytes()) {
		t.Fatalf("reminted dump changed\nsource dump:\n%s\nreminted dump:\n%s",dump.String(),again.String())
	}
	if p.Postgres != nil {
		// Replay the actual emitted native SQL, not a hand-authored PG surrogate.
		replay,err := p.Postgres.Conn(p.Ctx)
		if err != nil { t.Fatal(err) }
		defer replay.Close()
		pg := fixture.Backend{PG:true,DB:p.Postgres,Recorder:p.PostgresRecord}
		if _,err := replay.ExecContext(p.Ctx,"RESET SESSION AUTHORIZATION; RESET ROLE; DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil { t.Fatal(err) }
		fixture.RunStep(t,p.Ctx,replay,p.PostgresRecord,fixture.Exec("CREATE ROLE cube_transform_owner",0))
		if _,err := replay.ExecContext(p.Ctx,text); err != nil { t.Fatal("native dump PostgreSQL replay",err) }
		cubeTransformPGOwners(t,p,replay,pg.Recorder)
		cubeTransformVerifyStored(t,p,pg,replay,pg.Recorder,true)
		if _,err := replay.ExecContext(p.Ctx,"RESET SESSION AUTHORIZATION; RESET ROLE; DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil { t.Fatal(err) }
		fixture.RunStep(t,p.Ctx,replay,p.PostgresRecord,fixture.Exec("DROP ROLE cube_transform_owner",0))
	}
}

func TestReferenceCubeTransformControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t,[]string{"cube_transform_owner"})
	cubeConstructorScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
		cubeTransformStoredSetup(t,p,b,c)
		cubeTransformControlCycle(t,p,b,c,true)
	})
}
