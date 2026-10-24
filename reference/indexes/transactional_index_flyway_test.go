package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

const transactionalIndexFlywaySQL = `CREATE INDEX "flyway_schema_history_s_idx" ON "public"."flyway_schema_history" ("success")`

const transactionalIndexFlywayTable = `CREATE TABLE "public"."flyway_schema_history" (
    "installed_rank" INT NOT NULL,
    "version" VARCHAR(50),
    "description" VARCHAR(200) NOT NULL,
    "type" VARCHAR(20) NOT NULL,
    "script" VARCHAR(1000) NOT NULL,
    "checksum" INTEGER,
    "installed_by" VARCHAR(100) NOT NULL,
    "installed_on" TIMESTAMP NOT NULL DEFAULT now(),
    "execution_time" INTEGER NOT NULL,
    "success" BOOLEAN NOT NULL
)`

// Each route retains its statement across BEGIN or across creation of the private
// parent. Preparing a fresh statement at Execute would not test late binding.
func TestReferenceTransactionalIndexFlyway(t *testing.T) {
	for _, privateParent := range []bool{false, true} {
		for _, goTx := range []bool{false, true} {
			for _, route := range []string{"direct", "held-before", "held-inside"} {
				for _, commit := range []bool{false, true} {
					t.Run(fmt.Sprintf("private=%t/go=%t/%s/commit=%t", privateParent, goTx, route, commit), func(t *testing.T) {
						p := fixture.NewScenario(t)
						for _, b := range p.Backends() {
							t.Run(b.Name, func(t *testing.T) {
								owner, peer := fixture.LockConnections(t, p.Ctx, b.DB)
								runOn := func(e fixture.Executor, s fixture.Step) {
									fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
								}
								if !privateParent {
									runOn(owner, fixture.Exec(`CREATE TABLE public.flyway_schema_history(success BOOLEAN NOT NULL)`, 0))
									runOn(owner, fixture.Exec(`INSERT INTO public.flyway_schema_history VALUES(true),(false)`, 2))
								}
								var held *sql.Stmt
								if route == "held-before" {
									var err error
									held, err = owner.PrepareContext(p.Ctx, transactionalIndexFlywaySQL)
									if err != nil {
										t.Fatal("prepare before BEGIN and private parent existence", err)
									}
									defer held.Close()
								}
								var e fixture.Executor = owner
								var tx *sql.Tx
								if goTx {
									var err error
									tx, err = owner.BeginTx(p.Ctx, nil)
									if err != nil {
										t.Fatal(err)
									}
									defer tx.Rollback()
									e = tx
								} else {
									runOn(owner, fixture.Exec("BEGIN", 0))
									defer owner.ExecContext(p.Ctx, "ROLLBACK")
								}
								if route == "held-inside" {
									var err error
									held, err = e.PrepareContext(p.Ctx, transactionalIndexFlywaySQL)
									if err != nil {
										t.Fatal("prepare inside BEGIN before private parent existence", err)
									}
									defer held.Close()
								}
								if privateParent {
									runOn(e, fixture.Exec(transactionalIndexFlywayTable, 0))
									runOn(e, fixture.Exec(`INSERT INTO public.flyway_schema_history(installed_rank,version,description,type,script,checksum,installed_by,execution_time,success) VALUES(1,NULL,'first','SQL','V1.sql',NULL,'tester',4,true),(2,'2','second','SQL','V2.sql',9,'tester',5,false)`, 2))
								}
								indexAbsent := fixture.Select(`SELECT relname FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='flyway_schema_history_s_idx'`, []string{"relname"}, []string{"NAME"}, nil)
								runOn(e, indexAbsent)
								var execute fixture.Executor = e
								if held != nil {
									active := held
									if goTx && route == "held-before" {
										active = tx.StmtContext(p.Ctx, held)
										defer active.Close()
									}
									execute = fixture.HeldStatement{Executor: e, Statement: active, SQL: transactionalIndexFlywaySQL}
								}
								runOn(execute, fixture.Exec(transactionalIndexFlywaySQL, 0))
								var indexID, parentID int64
								const identities = `SELECT 'public.flyway_schema_history_s_idx'::regclass::bigint,'public.flyway_schema_history'::regclass::bigint`
								var identityRow *sql.Row
								if goTx {
									identityRow = tx.QueryRowContext(p.Ctx, identities)
								} else {
									identityRow = owner.QueryRowContext(p.Ctx, identities)
								}
								if err := identityRow.Scan(&indexID, &parentID); err != nil {
									t.Fatal(err)
								}
								if indexID <= 0 || parentID <= 0 || indexID == parentID {
									t.Fatalf("invalid index/parent identities: %d/%d", indexID, parentID)
								}
								definition := "CREATE INDEX flyway_schema_history_s_idx ON public.flyway_schema_history USING btree (success)"
								checkIndex := func(target fixture.Executor) {
									runOn(target, fixture.Select(`SELECT c.oid::bigint,c.relname,c.relkind,n.nspname,i.indrelid::bigint,i.indisunique,i.indisvalid,i.indisready FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_index i ON i.indexrelid=c.oid WHERE n.nspname='public' AND c.relname='flyway_schema_history_s_idx'`, []string{"oid", "relname", "relkind", "nspname", "indrelid", "indisunique", "indisvalid", "indisready"}, []string{"INT8", "NAME", "CHAR", "NAME", "INT8", "BOOL", "BOOL", "BOOL"}, [][]any{{indexID, "flyway_schema_history_s_idx", "i", "public", parentID, false, true, true}}))
									runOn(target, fixture.Select(`SELECT schemaname,tablename,indexname,tablespace,indexdef FROM pg_indexes WHERE schemaname='public' AND indexname='flyway_schema_history_s_idx'`, []string{"schemaname", "tablename", "indexname", "tablespace", "indexdef"}, []string{"NAME", "NAME", "NAME", "NAME", "TEXT"}, [][]any{{"public", "flyway_schema_history", "flyway_schema_history_s_idx", nil, definition}}))
									runOn(target, fixture.Select(`SELECT pg_get_indexdef('public.flyway_schema_history_s_idx'::regclass) AS definition`, []string{"definition"}, []string{"TEXT"}, [][]any{{definition}}))
									column := int64(1)
									if privateParent {
										column = 10
									}
									runOn(target, fixture.Select(`SELECT objsubid,refobjsubid,deptype FROM pg_depend WHERE classid='pg_class'::regclass AND objid=$1::oid AND refclassid='pg_class'::regclass AND refobjid=$2::oid ORDER BY refobjsubid`, []string{"objsubid", "refobjsubid", "deptype"}, []string{"INT4", "INT4", "CHAR"}, [][]any{{int64(0), column, "a"}}, indexID, parentID))
								}
								rows := fixture.Select(`SELECT success FROM public.flyway_schema_history ORDER BY success`, []string{"success"}, []string{"BOOL"}, [][]any{{false}, {true}})
								checkIndex(e)
								runOn(e, rows)
								runOn(peer, indexAbsent)
								wantTable := int64(1)
								if privateParent {
									wantTable = 0
								}
								tableCount := func(n int64) fixture.Step {
									return fixture.Select(`SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='flyway_schema_history'`, []string{"count"}, []string{"INT8"}, [][]any{{n}})
								}
								runOn(peer, tableCount(wantTable))
								runOn(peer, fixture.Select(`SELECT count(*) FROM pg_depend WHERE classid='pg_class'::regclass AND objid=$1::oid`, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, indexID))
								if goTx {
									var err error
									if commit {
										err = tx.Commit()
									} else {
										err = tx.Rollback()
									}
									if err != nil {
										t.Fatal(err)
									}
								} else if commit {
									runOn(e, fixture.Exec("COMMIT", 0))
								} else {
									runOn(e, fixture.Exec("ROLLBACK", 0))
								}
								if commit {
									checkIndex(peer)
									runOn(peer, rows)
								} else {
									runOn(peer, indexAbsent)
									runOn(peer, tableCount(wantTable))
									runOn(peer, fixture.Select(`SELECT count(*) FROM pg_depend WHERE classid='pg_class'::regclass AND objid=$1::oid`, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, indexID))
									if !privateParent {
										runOn(peer, rows)
									}
								}
							})
						}
					})
				}
			}
		}
	}
}
