package reference_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func pgIndexDiagnostics(run func(fixture.Step), smoke bool) {
	cases := []struct {q,code,message,position string}{
		{"SELECT missing FROM pg_catalog.pg_index","42703",`column "missing" does not exist`,"8"},
		{"SELECT /*é*/ missing FROM pg_catalog.pg_index","42703",`column "missing" does not exist`,"14"},
		{"SELECT oid FROM pg_catalog.pg_index","42703",`column "oid" does not exist`,"8"},
		{"SELECT 'bad'::oid FROM pg_catalog.pg_index WHERE FALSE","22P02",`invalid input syntax for type oid: "bad"`,"8"},
		{"SELECT '4294967296'::oid FROM pg_catalog.pg_index WHERE FALSE","22003",`value "4294967296" is out of range for type oid`,"8"},
	}
	if smoke { cases = cases[:2] }
	for _,tc := range cases {
		for _,prepared := range []bool{false,true} {
			s := fixture.ImplicitError(tc.q,fixture.Diagnostic{Code:tc.code,Message:tc.message,Position:tc.position})
			s.SelectRows,s.Prepared,s.ErrorStage = true,prepared,"execute"; if prepared { s.ErrorStage = "prepare" }; run(s)
		}
	}
}

func pgIndexOrdinaryError(t *testing.T,err error) {
	t.Helper()
	if err == nil { t.Fatal("ordinary error absent") }
	var state interface { SQLState() string }
	if errors.As(err,&state) { t.Fatalf("ordinary error acquired SQLSTATE: %v",err) }
}

func TestReferencePGIndexDiagnostics(t *testing.T) {
	fixture.ConcurrentIndexScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s) }; pgIndexDiagnostics(run,false)
		q := "SELECT * FROM pg_catalog.pg_index WHERE indexrelid=$1::oid"
		stmt,err := b.DB.PrepareContext(p.Ctx,q); if err != nil { t.Fatal(err) }; defer stmt.Close()
		// NumInput enforces Go argument cardinality on held statements; a
		// parameter-free pq Query uses the simple protocol instead.
		for _, args := range [][]any{nil, {int64(1), int64(2)}} {
			rows, err := stmt.QueryContext(p.Ctx, args...)
			if rows != nil { rows.Close(); t.Fatal("invalid arguments returned rows") }
			pgIndexOrdinaryError(t, err)
		}
		for _, held := range []bool{false, true} {
			if held {
				rows, err := stmt.QueryContext(p.Ctx, make(chan int))
				if rows != nil { rows.Close(); t.Fatal("invalid conversion returned rows") }
				pgIndexOrdinaryError(t, err)
			} else {
				rows, err := b.DB.QueryContext(p.Ctx, q, make(chan int))
				if rows != nil { rows.Close(); t.Fatal("invalid conversion returned rows") }
				pgIndexOrdinaryError(t, err)
			}
		}
		if !b.PG {
			ctx,cancel := context.WithCancel(p.Ctx); cancel()
			rows,err := b.DB.QueryContext(ctx,q,int64(1)); if rows != nil { rows.Close(); t.Fatal("canceled query rows") }; pgIndexOrdinaryError(t,err); if !errors.Is(err,context.Canceled) { t.Fatal(err) }
			rows,err = stmt.QueryContext(ctx,int64(1)); if rows != nil { rows.Close(); t.Fatal("canceled held rows") }; pgIndexOrdinaryError(t,err); if !errors.Is(err,context.Canceled) { t.Fatal(err) }
			prepared,err := b.DB.PrepareContext(ctx,q); if prepared != nil { prepared.Close(); t.Fatal("canceled prepare statement") }; pgIndexOrdinaryError(t,err); if !errors.Is(err,context.Canceled) { t.Fatal(err) }
		}
	})
}

func TestReferencePGIndexDisclosure(t *testing.T) {
	fixture.RoleReferenceCleanup(t,[]string{"pgi_reader"})
	fixture.SchemaReferenceCleanup(t,"pgi_hidden")
	fixture.ConcurrentIndexScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
		owner,reader := fixture.LockConnections(t,p.Ctx,b.DB)
		run,read := transactionalIndexRun(t,p,b,owner),transactionalIndexRun(t,p,b,reader)
		for _,q := range []string{"CREATE ROLE pgi_reader","CREATE SCHEMA pgi_hidden","CREATE TABLE pgi_hidden.t(n INT)","CREATE INDEX pgi_hidden_ix ON pgi_hidden.t(n)"} { run(fixture.Exec(q,0)) }
		var indexOID,parentOID int64
		if err := owner.QueryRowContext(p.Ctx,"SELECT 'pgi_hidden.pgi_hidden_ix'::regclass::oid::bigint,'pgi_hidden.t'::regclass::oid::bigint").Scan(&indexOID,&parentOID); err != nil || indexOID <= 0 || parentOID <= 0 { t.Fatal(indexOID,parentOID,err) }
		defer func() {
			for _,q := range []string{"RESET ROLE","GRANT SELECT ON pg_catalog.pg_index TO PUBLIC"} { if _,err := reader.ExecContext(context.Background(),q); err != nil { t.Error("restore catalog authority",err) } }
		}()
		read(fixture.Exec("SET ROLE pgi_reader",0))
		s := pgIndexLogical("pgi_hidden_ix",[][]any{pgIndexOrdinary(1,1,"1","0","1978","0",nil,nil)})
		s.Query = strings.Replace(s.Query,"ns.nspname='public'","ns.nspname='pgi_hidden'",1)
		pgIndexBoth(read,s)
		pgIndexBoth(read,fixture.Select("SELECT indexrelid=$1::oid AS index_identity,indrelid=$2::oid AS parent_identity FROM pg_index WHERE indexrelid=$1::oid",[]string{"index_identity","parent_identity"},[]string{"BOOL","BOOL"},[][]any{{true,true}},indexOID,parentOID))
		q := "SELECT * FROM pg_catalog.pg_index WHERE FALSE"
		stmt,err := reader.PrepareContext(p.Ctx,q); if err != nil { t.Fatal(err) }; defer stmt.Close()
		held := fixture.HeldStatement{Executor:reader,Statement:stmt,SQL:q}
		fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.Select(q,pgIndexColumns,pgIndexTypes,nil))
		run(fixture.Exec("REVOKE SELECT ON pg_catalog.pg_index FROM PUBLIC",0))
		for _,query := range []string{q,"SELECT * FROM pg_catalog.pg_index"} {
			s := fixture.ImplicitError(query,fixture.Diagnostic{Code:"42501",Message:"permission denied for table pg_index"}); s.SelectRows,s.ErrorStage = true,"execute"; pgIndexBoth(read,s)
			if query == q { fixture.RunStep(t,p.Ctx,held,b.Recorder,s) }
		}
		run(fixture.Exec("GRANT SELECT ON pg_catalog.pg_index TO PUBLIC",0)); pgIndexBoth(read,s)
		fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.Select(q,pgIndexColumns,pgIndexTypes,nil))
		for _,query := range []string{"UPDATE pg_catalog.pg_index SET indisvalid=false WHERE FALSE","DELETE FROM pg_catalog.pg_index WHERE FALSE"} {
			s := fixture.ImplicitError(query,fixture.Diagnostic{Code:"42501",Message:"permission denied for table pg_index"}); s.ErrorStage = "execute"; pgIndexBoth(read,s)
		}
		read(fixture.Exec("RESET ROLE",0))
		for _,query := range []string{"UPDATE pg_catalog.pg_index SET indisvalid=false WHERE FALSE","DELETE FROM pg_catalog.pg_index WHERE FALSE"} {
			if b.PG {
				tx,err := reader.BeginTx(p.Ctx,nil); if err != nil { t.Fatal(err) }
				fixture.RunStep(t,p.Ctx,tx,b.Recorder,fixture.Exec(query,0)); if err := tx.Rollback(); err != nil { t.Fatal(err) }
			} else { s := fixture.ImplicitError(query,fixture.Diagnostic{Code:"0A000",Message:"catalog source mutation is unsupported"}); s.ErrorStage = "execute"; pgIndexBoth(read,s) }
		}
		pgIndexBoth(read,s)
	})
}

func TestReferencePGIndexMutationAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pgi_mutator"})
	fixture.ConcurrentIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn := fixture.ConcurrentIndexConn(t, p, b)
		run := transactionalIndexRun(t, p, b, conn)
		defer func() {
			if _, err := conn.ExecContext(context.Background(), "RESET ROLE"); err != nil {
				t.Error("restore mutation authority", err)
			}
		}()
		for _, q := range []string{
			"CREATE ROLE pgi_mutator",
			"GRANT USAGE ON SCHEMA public TO pgi_mutator",
			"CREATE TABLE pgi_authority(n INT)",
			"CREATE INDEX pgi_authority_ix ON pgi_authority(n)",
			"INSERT INTO pgi_authority VALUES(7)",
		} {
			count := int64(0)
			if strings.HasPrefix(q, "INSERT") {
				count = 1
			}
			run(fixture.Exec(q, count))
		}
		check := func() {
			pgIndexBoth(run, pgIndexLogical("pgi_authority_ix", [][]any{pgIndexOrdinary(1, 1, "1", "0", "1978", "0", nil, nil)}))
			pgIndexBoth(run, fixture.Select("SELECT typname FROM pg_catalog.pg_type WHERE oid=23", []string{"typname"}, []string{"NAME"}, [][]any{{"int4"}}))
		}
		for _, target := range []struct {
			name, update, predicate string
		}{
			{"pg_index", "indisvalid=false", "indexrelid='public.pgi_authority_ix'::regclass"},
			{"pg_type", "typname='pgi_corrupted'", "oid=23"},
		} {
			for _, q := range []string{
				"UPDATE pg_catalog." + target.name + " SET " + target.update + " WHERE " + target.predicate,
				"DELETE FROM pg_catalog." + target.name + " WHERE " + target.predicate,
			} {
				// Prepare as superuser, then prove execution uses the restricted
				// current actor rather than retained prepare-time authority.
				statement, err := conn.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer statement.Close()
				run(fixture.Exec("SET ROLE pgi_mutator", 0))
				failure := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table " + target.name})
				failure.ErrorStage = "execute"
				pgIndexBoth(run, failure)
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: statement, SQL: q}, b.Recorder, failure)
				check()
				run(fixture.Exec("RESET ROLE", 0))
				if !b.PG {
					failure = fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "catalog source mutation is unsupported"})
					failure.ErrorStage = "execute"
					pgIndexBoth(run, failure)
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: statement, SQL: q}, b.Recorder, failure)
				} else {
					// PostgreSQL superusers can modify physical catalogs. Admit
					// only a harmless zero-row statement inside rollback.
					tx, err := conn.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec(q+" AND FALSE", 0))
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
				}
				if err := statement.Close(); err != nil {
					t.Fatal(err)
				}
				check()
			}
		}
		run(fixture.Select("SELECT n FROM pgi_authority", []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}}))
	})
}
