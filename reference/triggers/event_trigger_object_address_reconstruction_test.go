package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func droppedRoutine(schema, name, kind string, args []string) droppedRecord {
	identity := droppedQualified(schema, name) + "("
	for i, arg := range args {
		if i > 0 {
			identity += ","
		}
		identity += arg
	}
	identity += ")"
	return droppedRecord{class: 1255, original: true, kind: kind, schema: schema, identity: identity, names: []string{schema, name}, args: args, lookup: "pg_proc WHERE oid=" + droppedLiteral(identity) + "::regprocedure"}
}

const droppedReconstructSQL = "SELECT a.object_type,r.classid,r.objid,r.objsubid FROM public.et_do_audit a CROSS JOIN LATERAL pg_get_object_address(a.object_type,a.address_names,a.address_args) r WHERE a.pass=1 AND a.original AND a.object_type IN ('table','function','procedure')"
const droppedHeldAddressSQL = "SELECT * FROM pg_get_object_address($1,$2::text[],$3::text[])"

// Expected OIDs come only from the current catalog, not historical audit IDs or
// the resolver. Saved address columns are consumed without rewriting their cells.
func droppedReconstructionCheck(d droppedCase, source string, roots []droppedRecord) {
	d.t.Helper()
	q := "SELECT a.object_type,r.classid::bigint AS classid,r.objid::bigint AS objid,r.objsubid FROM " + source + " a CROSS JOIN LATERAL pg_get_object_address(a.object_type,a.address_names,a.address_args) r WHERE a.pass=1 AND a.original AND a.object_type IN ('table','function','procedure')"
	var want [][]any
	for _, r := range roots {
		var id int64
		if err := d.c.QueryRowContext(d.p.Ctx, "SELECT oid::bigint FROM "+r.lookup).Scan(&id); err != nil || id <= 0 {
			d.t.Fatal("current catalog identity", r.identity, id, err)
		}
		want = append(want, []any{r.kind, r.class, id, int64(0)})
	}
	s := fixture.Select(q, []string{"object_type", "classid", "objid", "objsubid"}, []string{"TEXT", "INT8", "INT8", "INT4"}, want)
	s.Bag = true
	d.run(s)
	d.run(fixture.Prepared(s))
	raw := "SELECT a.object_type,r.classid,r.objid,r.objsubid FROM " + source + " a CROSS JOIN LATERAL pg_get_object_address(a.object_type,a.address_names,a.address_args) r WHERE a.pass=1 AND a.original AND a.object_type IN ('table','function','procedure')"
	var rawRows [][]any
	for _, row := range want {
		copyRow := append([]any(nil), row...)
		if d.b.PG {
			copyRow[1], copyRow[2] = fmt.Sprint(row[1]), fmt.Sprint(row[2])
		}
		rawRows = append(rawRows, copyRow)
	}
	for _, empty := range []bool{false, true} {
		query := raw
		rows := rawRows
		if empty {
			query += " LIMIT 0"
			rows = nil
		}
		s := fixture.Select(query, []string{"object_type", "classid", "objid", "objsubid"}, []string{"TEXT", "OID", "OID", "INT4"}, rows)
		s.Bag = true
		s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
			cols, err := r.ColumnTypes()
			if err != nil || len(cols) != 4 {
				t.Fatal("reconstruction metadata", cols, err)
			}
			fixture.ScalarAssertMetadata(t, d.b.PG, cols, 1, 2, 3)
			for i := 1; i < 4; i++ {
				want := reflect.TypeFor[int64]()
				if d.b.PG {
					want = reflect.TypeFor[any]()
					if i == 3 {
						want = reflect.TypeFor[int32]()
					}
				}
				if cols[i].ScanType() != want {
					t.Fatal("address scan type", i, cols[i].ScanType(), want)
				}
				if n, k := cols[i].Length(); n != 0 || k {
					t.Fatal("address length", i, n, k)
				}
				if a, z, k := cols[i].DecimalSize(); a != 0 || z != 0 || k {
					t.Fatal("address precision", i, a, z, k)
				}
			}
		}
		d.run(s)
		d.run(fixture.Prepared(s))
		stmt, err := d.c.PrepareContext(d.p.Ctx, query)
		if err != nil {
			d.t.Fatal(err)
		}
		fixture.RunStep(d.t, d.p.Ctx, fixture.HeldStatement{Executor: d.c, Statement: stmt, SQL: query}, d.b.Recorder, s)
		if err := stmt.Close(); err != nil {
			d.t.Fatal(err)
		}
	}
}

func TestReferenceEventTriggerObjectAddressReconstruction(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, kind := range []string{"table", "function", "procedure", "domain_array"} {
			t.Run(fmt.Sprintf("%s/prepared_%t", kind, prepared), func(t *testing.T) {
				fixture.SchemaReferenceCleanup(t, "Odd S")
				droppedScenario(t, func(d droppedCase) {
					d.setup()
					d.absenceCallback()
					d.install("'DROP TABLE','DROP FUNCTION','DROP PROCEDURE'")
					d.exec(`CREATE SCHEMA "Odd S"`, `CREATE DOMAIN "Odd S"."D q" AS integer`, `CREATE FUNCTION "Odd S"."F.q"(bigint) RETURNS bigint LANGUAGE SQL AS $$SELECT $1$$`)
					var ddl, drop string
					var expected []droppedRecord
					var root droppedRecord
					var message, code string
					switch kind {
					case "table":
						ddl = `CREATE TABLE "Odd S"."T q"(i integer)`
						drop = `DROP TABLE "Odd S"."T q"`
						expected = droppedTable("Odd S", "T q", false)
						root = expected[0]
						code = "42P01"
						message = `relation "Odd S.T q" does not exist`
					case "function":
						ddl = `CREATE FUNCTION "Odd S"."F.q"(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1$$`
						drop = `DROP FUNCTION "Odd S"."F.q"(integer)`
						root = droppedRoutine("Odd S", "F.q", "function", []string{"integer"})
						expected = []droppedRecord{root}
						code = "42883"
						message = `function Odd S.F.q(integer) does not exist`
					case "procedure":
						ddl = `CREATE PROCEDURE "Odd S"."P q"(integer) LANGUAGE SQL AS $$SELECT $1$$`
						drop = `DROP PROCEDURE "Odd S"."P q"(integer)`
						root = droppedRoutine("Odd S", "P q", "procedure", []string{"integer"})
						expected = []droppedRecord{root}
						code = "42883"
						message = `procedure Odd S.P q(integer) does not exist`
					case "domain_array":
						ddl = `CREATE FUNCTION "Odd S"."F domain"("Odd S"."D q",integer[]) RETURNS integer LANGUAGE SQL AS $$SELECT 7$$`
						drop = `DROP FUNCTION "Odd S"."F domain"("Odd S"."D q",integer[])`
						root = droppedRoutine("Odd S", "F domain", "function", []string{`"Odd S"."D q"`, "integer[]"})
						expected = []droppedRecord{root}
						code = "42883"
						message = `function Odd S.F domain("Odd S"."D q", integer[]) does not exist`
					}
					d.exec(ddl)
					stmt, err := d.c.PrepareContext(d.p.Ctx, droppedHeldAddressSQL)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held := fixture.HeldStatement{Executor: d.c, Statement: stmt, SQL: droppedHeldAddressSQL}
					expected = d.capture(expected)
					d.drop(drop, prepared)
					d.bag(1, expected)
					d.bag(2, expected)
					d.phases(len(expected))
					var actualKind, names, args string
					if err := d.c.QueryRowContext(d.p.Ctx, "SELECT object_type,address_names::text,address_args::text FROM public.et_do_audit WHERE pass=1 AND original").Scan(&actualKind, &names, &args); err != nil {
						t.Fatal(err)
					}
					missing := fixture.ImplicitError(droppedReconstructSQL, fixture.Diagnostic{Code: code, Message: message})
					missing.SelectRows, missing.ErrorStage = true, "execute"
					d.run(missing)
					d.run(fixture.Prepared(missing))
					hm := fixture.ImplicitError(droppedHeldAddressSQL, fixture.Diagnostic{Code: code, Message: message}, actualKind, names, args)
					hm.SelectRows, hm.ErrorStage = true, "execute"
					fixture.RunStep(t, d.p.Ctx, held, d.b.Recorder, hm)
					outside := fixture.ErrorDiagnostic("SELECT * FROM pg_event_trigger_dropped_objects()", droppedProtocolDiagnostic(""))
					outside.SelectRows, outside.ErrorStage = true, "execute"
					d.run(outside)
					d.exec(ddl)
					checkHeld := func() {
						t.Helper()
						var id int64
						if err := d.c.QueryRowContext(d.p.Ctx, "SELECT oid::bigint FROM "+root.lookup).Scan(&id); err != nil {
							t.Fatal(err)
						}
						row := []any{root.class, id, int64(0)}
						if d.b.PG {
							row = []any{fmt.Sprint(root.class), fmt.Sprint(id), int64(0)}
						}
						fixture.RunStep(t, d.p.Ctx, held, d.b.Recorder, fixture.Select(droppedHeldAddressSQL, []string{"classid", "objid", "objsubid"}, []string{"OID", "OID", "INT4"}, [][]any{row}, actualKind, names, args))
					}
					droppedReconstructionCheck(d, "public.et_do_audit", []droppedRecord{root})
					checkHeld()
					// The same handle sees a second disappearance/recreation, not only
					// the first cached subid. Historical input strings remain untouched.
					d.exec("ALTER EVENT TRIGGER et_do_drop DISABLE", "ALTER EVENT TRIGGER et_do_end DISABLE", drop)
					fixture.RunStep(t, d.p.Ctx, held, d.b.Recorder, hm)
					d.exec(ddl)
					checkHeld()
					d.bag(1, expected)
					d.bag(2, expected)
					d.run(fixture.Select(`SELECT "Odd S"."F.q"(9::bigint) AS v`, []string{"v"}, []string{"INT8"}, [][]any{{int64(9)}}))
					droppedRecipeFailure(d, root)
				})
			})
		}
	}
}

func droppedRecipeFailure(d droppedCase, root droppedRecord) {
	d.t.Helper()
	d.exec("CREATE TABLE public.et_addr_source(seq integer,object_type text,address_names text[],address_args text[])", "CREATE TABLE public.et_addr_result(classid oid,objid oid,objsubid integer)")
	d.run(fixture.Exec("INSERT INTO public.et_addr_source SELECT 1,object_type,address_names,address_args FROM public.et_do_audit WHERE pass=1 AND original", 1))
	d.run(fixture.Exec("INSERT INTO public.et_addr_source VALUES(2,'function','{public,et_addr_missing}','{integer}')", 1))
	const consumer = "INSERT INTO public.et_addr_result SELECT r.* FROM (SELECT * FROM public.et_addr_source ORDER BY seq) a CROSS JOIN LATERAL pg_get_object_address(a.object_type,a.address_names,a.address_args) r"
	stmt, err := d.c.PrepareContext(d.p.Ctx, consumer)
	if err != nil {
		d.t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: d.c, Statement: stmt, SQL: consumer}
	check := func() {
		d.run(eventTriggerCount("SELECT count(*) AS n FROM public.et_addr_result", 0))
		d.run(fixture.Select("SELECT seq,object_type,address_names,address_args FROM public.et_addr_source ORDER BY seq", []string{"seq", "object_type", "address_names", "address_args"}, []string{"INT4", "TEXT", "_TEXT", "_TEXT"}, [][]any{{int64(1), root.kind, droppedArray(root.names), droppedArray(root.args)}, {int64(2), "function", "{public,et_addr_missing}", "{integer}"}}))
		droppedReconstructionCheck(d, "public.et_do_audit", []droppedRecord{root})
		d.run(fixture.Select(`SELECT "Odd S"."F.q"(9::bigint) AS v`, []string{"v"}, []string{"INT8"}, [][]any{{int64(9)}}))
	}
	failure := fixture.ImplicitError(consumer, fixture.Diagnostic{Code: "42883", Message: "function public.et_addr_missing(integer) does not exist"})
	failure.ErrorStage = "execute"
	d.run(failure)
	check()
	d.exec("BEGIN", "SAVEPOINT addr_consumer")
	fixture.RunStep(d.t, d.p.Ctx, held, d.b.Recorder, failure)
	d.exec("ROLLBACK TO SAVEPOINT addr_consumer")
	check()
	d.exec("CREATE FUNCTION public.et_addr_missing(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1$$")
	fixture.RunStep(d.t, d.p.Ctx, held, d.b.Recorder, fixture.Exec(consumer, 2))
	d.run(eventTriggerCount("SELECT count(*) AS n FROM public.et_addr_result", 2))
	d.exec("ROLLBACK")
	check()
	if !d.b.PG {
		// Canceled consumers use real saved fields and preserve all source and
		// destination state; ordinary context errors are not SQL diagnostics.
		const query = "SELECT r.* FROM public.et_do_audit a CROSS JOIN LATERAL pg_get_object_address(a.object_type,a.address_names,a.address_args) r WHERE a.pass=1 AND a.original"
		s, err := d.c.PrepareContext(d.p.Ctx, query)
		if err != nil {
			d.t.Fatal(err)
		}
		defer s.Close()
		for _, expired := range []bool{false, true} {
			ctx, cancel := context.WithCancel(d.p.Ctx)
			want := context.Canceled
			if expired {
				cancel()
				ctx, cancel = context.WithDeadline(d.p.Ctx, time.Unix(1, 0))
				want = context.DeadlineExceeded
			} else {
				cancel()
			}
			for _, prepared := range []bool{false, true} {
				var rows *sql.Rows
				var err error
				if prepared {
					rows, err = s.QueryContext(ctx)
				} else {
					rows, err = d.c.QueryContext(ctx, query)
				}
				if rows != nil || !errors.Is(err, want) {
					d.t.Fatal("canceled saved recipe", rows, err, want)
				}
			}
			cancel()
			check()
		}
		rows, err := s.QueryContext(d.p.Ctx)
		if err != nil {
			d.t.Fatal(err)
		}
		fixture.ScalarRows(d.t, false, rows, []string{"classid", "objid", "objsubid"}, []string{"OID", "OID", "INT4"}, [][]any{{root.class, currentDroppedOID(d, root), int64(0)}}, false)
	}
}

func currentDroppedOID(d droppedCase, r droppedRecord) int64 {
	d.t.Helper()
	var id int64
	if err := d.c.QueryRowContext(d.p.Ctx, "SELECT oid::bigint FROM "+r.lookup).Scan(&id); err != nil || id <= 0 {
		d.t.Fatal("current identity", id, err)
	}
	return id
}
