package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDomainPreparedLifecycle(t *testing.T) {
	domainReferenceScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder, notices *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
		for _, q := range []string{"CREATE DOMAIN domain_s.di AS integer", "CREATE DOMAIN domain_s.bytes AS bytea", "CREATE TABLE domain_s.held (id integer,v domain_s.di)", "CREATE TABLE domain_s.buffers (v domain_s.bytes)"} {
			run(fixture.Exec(q, 0))
		}
		// External catalog control: obtain the original PostgreSQL identity before
		// DROP. Internal cache diagnostics must not hardcode a physical oracle OID.
		var oldDomainOID int64
		err := conn.QueryRowContext(p.Ctx, "SELECT t.oid::bigint FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname='domain_s' AND t.typname='di'").Scan(&oldDomainOID)
		if err != nil {
			t.Fatal(err)
		}
		if oldDomainOID <= 0 {
			t.Fatalf("invalid PostgreSQL domain OID %d", oldDomainOID)
		}
		insertQ := "INSERT INTO domain_s.held VALUES ($1,$2)"
		selectQ := "SELECT id,v FROM domain_s.held ORDER BY id"
		castQ := "SELECT $1::domain_s.di AS v FROM public.one"
		prepare := func(q string) *sql.Stmt {
			stmt, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := stmt.Close(); err != nil {
					t.Error(err)
				}
			})
			return stmt
		}
		insert, selectStmt, cast := prepare(insertQ), prepare(selectQ), prepare(castQ)
		inserter := fixture.HeldStatement{Executor: conn, Statement: insert, SQL: insertQ}
		selector := fixture.HeldStatement{Executor: conn, Statement: selectStmt, SQL: selectQ}
		caster := fixture.HeldStatement{Executor: conn, Statement: cast, SQL: castQ}
		heldError := func(x fixture.HeldStatement, want fixture.Diagnostic, queryRows bool, args ...any) {
			s := fixture.ErrorDiagnostic(x.SQL, want, args...)
			s.SelectRows = queryRows
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, x, recorder, s)
		}
		// Explicit backend boundary: PostgreSQL retains physical parameter OIDs
		// and reports its internal cache failure. Native rebinds current syntax
		// while enforcing immutable logical parameter/result contracts.
		parameterChanged := func(index string) fixture.Diagnostic {
			want := fixture.ControlsReferenceDiagnostic("0A000", "cached plan must not change parameter type")
			if backend.PG {
				want = fixture.ControlsReferenceDiagnostic("XX000", fmt.Sprintf("cache lookup failed for type %d", oldDomainOID))
				want.Context = "unnamed portal parameter $" + index
			}
			return want
		}
		want := make([][]any, 0, 3)
		for i, v := range []any{int64(7), int64(8), nil} {
			fixture.RunStep(t, p.Ctx, inserter, recorder, fixture.Exec(insertQ, 1, int64(i+1), v))
			want = append(want, []any{int64(i + 1), v})
			fixture.RunStep(t, p.Ctx, selector, recorder, fixture.Select(selectQ, []string{"id", "v"}, []string{"INT4", "INT4"}, want))
			fixture.RunStep(t, p.Ctx, caster, recorder, fixture.Select(castQ, []string{"v"}, []string{"INT4"}, [][]any{{v}}, v))
		}
		run(fixture.Exec("CREATE DOMAIN domain_s.da AS name[]", 0))
		arrayQ := "SELECT $1::domain_s.da AS a FROM public.one"
		array := prepare(arrayQ)
		for _, v := range []any{"{alpha,NULL}", "[0:1]={beta,gamma}", nil, "{}"} {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: array, SQL: arrayQ}, recorder, fixture.Select(arrayQ, []string{"a"}, []string{"_NAME"}, [][]any{{v}}, v))
		}
		domainReferenceConcurrent(t, p, backend)
		// Import detaches the mutable byte buffer; no per-selected-cell copying.
		buffer := []byte{0, 97, 255}
		// The builtin annotation lets pq encode binary bytes using BYTEA's OID17;
		// ordinary assignment then restores the domain's distinct identity.
		run(fixture.Exec("INSERT INTO domain_s.buffers VALUES ($1::bytea)", 1, buffer))
		buffer[1] = 98
		run(fixture.Select("SELECT v FROM domain_s.buffers", []string{"v"}, []string{"BYTEA"}, [][]any{{string([]byte{0, 97, 255})}}))
		run(fixture.Prepared(fixture.Select("SELECT v FROM domain_s.buffers", []string{"v"}, []string{"BYTEA"}, [][]any{{string([]byte{0, 97, 255})}})))
		*notices = nil
		run(fixture.Exec("DROP DOMAIN domain_s.di CASCADE", 0))
		fixture.DomainReferenceNotice(t, notices, "drop cascades to column v of table domain_s.held", "")
		missingColumn := fixture.ControlsReferenceDiagnostic("42703", `column "v" does not exist`)
		missingColumn.Position = "11"
		heldError(selector, missingColumn, true)
		insertAfterDrop := fixture.ControlsReferenceDiagnostic("42601", "INSERT has more expressions than target columns")
		insertAfterDrop.Position = fixture.Position(strings.Index(insertQ, "$2") + 1)
		castAfterDrop := fixture.ControlsReferenceDiagnostic("42704", `type "domain_s.di" does not exist`)
		castAfterDrop.Position = fixture.Position(strings.Index(castQ, "domain_s.di") + 1)
		if backend.PG {
			insertAfterDrop = parameterChanged("2")
			castAfterDrop = parameterChanged("1")
		}
		heldError(inserter, insertAfterDrop, false, int64(4), int64(9))
		heldError(caster, castAfterDrop, true, int64(9))
		run(fixture.Exec("DROP TABLE domain_s.held", 0))
		run(fixture.Exec("CREATE DOMAIN domain_s.di AS integer", 0))
		run(fixture.Exec("CREATE TABLE domain_s.held (id integer,v domain_s.di)", 0))
		run(fixture.Exec("INSERT INTO domain_s.held VALUES (5,10)", 1))
		resultChanged := fixture.ControlsReferenceDiagnostic("0A000", "cached plan must not change result type")
		heldError(selector, resultChanged, true)
		heldError(inserter, parameterChanged("2"), false, int64(6), int64(11))
		heldError(caster, parameterChanged("1"), true, int64(11))
		run(fixture.Prepared(fixture.Select(selectQ, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(5), int64(10)}})))
		// Fresh prepare always resolves the replacement domain generation.
		run(fixture.Prepared(fixture.Select(castQ, []string{"v"}, []string{"INT4"}, [][]any{{int64(12)}}, int64(12))))
		// A different replacement base still cannot mutate either held contract.
		*notices = nil
		run(fixture.Exec("DROP DOMAIN domain_s.di CASCADE", 0))
		fixture.DomainReferenceNotice(t, notices, "drop cascades to column v of table domain_s.held", "")
		run(fixture.Exec("DROP TABLE domain_s.held", 0))
		run(fixture.Exec("CREATE DOMAIN domain_s.di AS text", 0))
		run(fixture.Exec("CREATE TABLE domain_s.held (id integer,v domain_s.di)", 0))
		run(fixture.Exec("INSERT INTO domain_s.held VALUES (7,'text')", 1))
		heldError(selector, resultChanged, true)
		heldError(caster, parameterChanged("1"), true, "text")
		run(fixture.Prepared(fixture.Select(selectQ, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "text"}})))
		run(fixture.Prepared(fixture.Select(castQ, []string{"v"}, []string{"TEXT"}, [][]any{{"fresh"}}, "fresh")))
	})
}

func domainReferenceConcurrent(t *testing.T, p *fixture.Pair, backend fixture.Backend) {
	t.Helper()
	stmt, err := backend.DB.PrepareContext(p.Ctx, "SELECT $1::domain_s.di AS i,$2::domain_s.da AS a FROM public.one")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	type result struct {
		worker       int
		value, array any
		err          error
	}
	start := make(chan struct{})
	results := make(chan result, 4)
	// Every worker sends once, including every failure; receive all results before
	// statements/pools close. No sleeps or shared mutable argument buffers.
	for worker := range 4 {
		go func() {
			<-start
			result := result{worker: worker}
			rows, err := stmt.QueryContext(p.Ctx, int64(worker), fmt.Sprintf("{worker%d,NULL}", worker))
			if err != nil {
				result.err = err
				results <- result
				return
			}
			if rows.Next() {
				result.err = rows.Scan(&result.value, &result.array)
				if result.err == nil && rows.Next() {
					result.err = fmt.Errorf("extra concurrent row")
				}
			} else {
				result.err = fmt.Errorf("missing concurrent row")
			}
			if result.err == nil {
				result.err = rows.Err()
			}
			if err := rows.Close(); result.err == nil {
				result.err = err
			}
			results <- result
		}()
	}
	close(start)
	observed := make([]result, 0, 4)
	for range 4 {
		observed = append(observed, <-results)
	}
	for _, result := range observed {
		if result.err != nil {
			t.Error(result.err)
			continue
		}
		fixture.AssertRawValue(t, backend.PG, "INT4", result.value)
		fixture.AssertRawValue(t, backend.PG, "_NAME", result.array)
		if fixture.Normalize(result.value) != int64(result.worker) || fixture.Normalize(result.array) != fmt.Sprintf("{worker%d,NULL}", result.worker) {
			t.Errorf("concurrent worker=%d values=%#v,%#v", result.worker, result.value, result.array)
		}
	}
}
