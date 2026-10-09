package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

func TestReferenceDomainPreparedLifecycle(t *testing.T) {
	domainReferenceScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn, recorder *referenceRecorder, notices *[]referenceDiagnostic) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, recorder, s) }
		for _, q := range []string{"CREATE DOMAIN domain_s.di AS integer", "CREATE DOMAIN domain_s.bytes AS bytea", "CREATE TABLE domain_s.held (id integer,v domain_s.di)", "CREATE TABLE domain_s.buffers (v domain_s.bytes)"} {
			run(referenceExec(q, 0))
		}
		// External catalog control: obtain the original PostgreSQL identity before
		// DROP. Internal cache diagnostics must not hardcode a physical oracle OID.
		var oldDomainOID int64
		if backend.pg {
			err := conn.QueryRowContext(p.ctx, "SELECT t.oid::bigint FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname='domain_s' AND t.typname='di'").Scan(&oldDomainOID)
			if err != nil {
				t.Fatal(err)
			}
			if oldDomainOID <= 0 {
				t.Fatalf("invalid PostgreSQL domain OID %d", oldDomainOID)
			}
		}
		insertQ := "INSERT INTO domain_s.held VALUES ($1,$2)"
		selectQ := "SELECT id,v FROM domain_s.held ORDER BY id"
		castQ := "SELECT $1::domain_s.di AS v FROM public.one"
		prepare := func(q string) *sql.Stmt {
			stmt, err := conn.PrepareContext(p.ctx, q)
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
		inserter := referenceHeldStatement{referenceExecutor: conn, statement: insert, query: insertQ}
		selector := referenceHeldStatement{referenceExecutor: conn, statement: selectStmt, query: selectQ}
		caster := referenceHeldStatement{referenceExecutor: conn, statement: cast, query: castQ}
		heldError := func(x referenceHeldStatement, want referenceDiagnostic, queryRows bool, args ...any) {
			s := referenceErrorDiagnostic(x.query, want, args...)
			s.selectRows = queryRows
			s.errorStage = "execute"
			referenceRunStep(t, p.ctx, x, recorder, s)
		}
		// Explicit backend boundary: PostgreSQL retains physical parameter OIDs
		// and reports its internal cache failure. Native rebinds current syntax
		// while enforcing immutable logical parameter/result contracts.
		parameterChanged := func(index string) referenceDiagnostic {
			want := controlsReferenceDiagnostic("0A000", "cached plan must not change parameter type")
			if backend.pg {
				want = controlsReferenceDiagnostic("XX000", fmt.Sprintf("cache lookup failed for type %d", oldDomainOID))
				want.Context = "unnamed portal parameter $" + index
			}
			return want
		}
		want := make([][]any, 0, 3)
		for i, v := range []any{int64(7), int64(8), nil} {
			referenceRunStep(t, p.ctx, inserter, recorder, referenceExec(insertQ, 1, int64(i+1), v))
			want = append(want, []any{int64(i + 1), v})
			referenceRunStep(t, p.ctx, selector, recorder, referenceSelect(selectQ, []string{"id", "v"}, []string{"INT4", "INT4"}, want))
			referenceRunStep(t, p.ctx, caster, recorder, referenceSelect(castQ, []string{"v"}, []string{"INT4"}, [][]any{{v}}, v))
		}
		run(referenceExec("CREATE DOMAIN domain_s.da AS name[]", 0))
		arrayQ := "SELECT $1::domain_s.da AS a FROM public.one"
		array := prepare(arrayQ)
		for _, v := range []any{"{alpha,NULL}", "[0:1]={beta,gamma}", nil, "{}"} {
			referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: conn, statement: array, query: arrayQ}, recorder, referenceSelect(arrayQ, []string{"a"}, []string{"_NAME"}, [][]any{{v}}, v))
		}
		domainReferenceConcurrent(t, p, backend)
		// Import detaches the mutable byte buffer; no per-selected-cell copying.
		buffer := []byte{0, 97, 255}
		// The builtin annotation lets pq encode binary bytes using BYTEA's OID17;
		// ordinary assignment then restores the domain's distinct identity.
		run(referenceExec("INSERT INTO domain_s.buffers VALUES ($1::bytea)", 1, buffer))
		buffer[1] = 98
		run(referenceSelect("SELECT v FROM domain_s.buffers", []string{"v"}, []string{"BYTEA"}, [][]any{{string([]byte{0, 97, 255})}}))
		run(referencePrepared(referenceSelect("SELECT v FROM domain_s.buffers", []string{"v"}, []string{"BYTEA"}, [][]any{{string([]byte{0, 97, 255})}})))
		*notices = nil
		run(referenceExec("DROP DOMAIN domain_s.di CASCADE", 0))
		domainReferenceNotice(t, notices, "drop cascades to column v of table domain_s.held", "")
		missingColumn := controlsReferenceDiagnostic("42703", `column "v" does not exist`)
		missingColumn.Position = "11"
		heldError(selector, missingColumn, true)
		insertAfterDrop := controlsReferenceDiagnostic("42601", "INSERT has more expressions than target columns")
		insertAfterDrop.Position = referencePosition(strings.Index(insertQ, "$2") + 1)
		castAfterDrop := controlsReferenceDiagnostic("42704", `type "domain_s.di" does not exist`)
		castAfterDrop.Position = referencePosition(strings.Index(castQ, "domain_s.di") + 1)
		if backend.pg {
			insertAfterDrop = parameterChanged("2")
			castAfterDrop = parameterChanged("1")
		}
		heldError(inserter, insertAfterDrop, false, int64(4), int64(9))
		heldError(caster, castAfterDrop, true, int64(9))
		run(referenceExec("DROP TABLE domain_s.held", 0))
		run(referenceExec("CREATE DOMAIN domain_s.di AS integer", 0))
		run(referenceExec("CREATE TABLE domain_s.held (id integer,v domain_s.di)", 0))
		run(referenceExec("INSERT INTO domain_s.held VALUES (5,10)", 1))
		resultChanged := controlsReferenceDiagnostic("0A000", "cached plan must not change result type")
		heldError(selector, resultChanged, true)
		heldError(inserter, parameterChanged("2"), false, int64(6), int64(11))
		heldError(caster, parameterChanged("1"), true, int64(11))
		run(referencePrepared(referenceSelect(selectQ, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(5), int64(10)}})))
		// Fresh prepare always resolves the replacement domain generation.
		run(referencePrepared(referenceSelect(castQ, []string{"v"}, []string{"INT4"}, [][]any{{int64(12)}}, int64(12))))
		// A different replacement base still cannot mutate either held contract.
		*notices = nil
		run(referenceExec("DROP DOMAIN domain_s.di CASCADE", 0))
		domainReferenceNotice(t, notices, "drop cascades to column v of table domain_s.held", "")
		run(referenceExec("DROP TABLE domain_s.held", 0))
		run(referenceExec("CREATE DOMAIN domain_s.di AS text", 0))
		run(referenceExec("CREATE TABLE domain_s.held (id integer,v domain_s.di)", 0))
		run(referenceExec("INSERT INTO domain_s.held VALUES (7,'text')", 1))
		heldError(selector, resultChanged, true)
		heldError(caster, parameterChanged("1"), true, "text")
		run(referencePrepared(referenceSelect(selectQ, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "text"}})))
		run(referencePrepared(referenceSelect(castQ, []string{"v"}, []string{"TEXT"}, [][]any{{"fresh"}}, "fresh")))
	})
}

func domainReferenceConcurrent(t *testing.T, p *referencePair, backend referenceBackend) {
	t.Helper()
	stmt, err := backend.db.PrepareContext(p.ctx, "SELECT $1::domain_s.di AS i,$2::domain_s.da AS a FROM public.one")
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
			rows, err := stmt.QueryContext(p.ctx, int64(worker), fmt.Sprintf("{worker%d,NULL}", worker))
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
		referenceAssertRawValue(t, backend.pg, "INT4", result.value)
		referenceAssertRawValue(t, backend.pg, "_NAME", result.array)
		if referenceNormalize(result.value) != int64(result.worker) || referenceNormalize(result.array) != fmt.Sprintf("{worker%d,NULL}", result.worker) {
			t.Errorf("concurrent worker=%d values=%#v,%#v", result.worker, result.value, result.array)
		}
	}
}
