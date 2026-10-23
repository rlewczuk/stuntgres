package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRangeTypeValues(t *testing.T) {
	rangeTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, run func(fixture.Step)) {
		rangeTypeSetup(run)
		rangeTypeSeed(run)
		for _, prepared := range []bool{false, true} {
			s := rangeTypeData(b, rangeTypeRows())
			s.Prepared = prepared
			run(s)
		}
		run(fixture.Exec(`INSERT INTO range_data VALUES (6,'[,]','{[1,2],[3,4]}'),(7,'(4,4]','{[1,2),[2,4]}'),(8,'(,3]','{empty,(,3],[3,)}')`, 3))
		run(rangeTypeSelect(b, `SELECT k,r,m FROM range_data WHERE k>5 ORDER BY k`, []string{"k", "r", "m"}, []string{"INT4", "CUSTOM_SPAN", "CUSTOM_SPANS"}, [][]any{{int64(6), "(,)", "{[1,2],[3,4]}"}, {int64(7), "empty", "{[1,4]}"}, {int64(8), "(,3]", "{(,)}"}}, []bool{false, true, true}))
	})
}

func TestReferenceRangeTypeFeatureSmoke(t *testing.T) {
	rangeTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, run func(fixture.Step)) {
		rangeTypeSetup(run)
		rangeTypeSeed(run)
		run(rangeTypeData(b, rangeTypeRows()))
		run(fixture.Prepared(fixture.Exec(`INSERT INTO range_data VALUES ($1,$2,$3)`, 1, int64(6), "[1,3]", "{[1,3]}")))
		want := append(rangeTypeRows(), []any{int64(6), "[1,3]", "{[1,3]}"})
		run(rangeTypeData(b, want))
	})
}

func TestReferenceRangeTypeBinds(t *testing.T) {
	rangeTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, run func(fixture.Step)) {
		rangeTypeSetup(run)
		const q = `INSERT INTO range_data VALUES ($1,$2,$3)`
		stmt, err := conn.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
		for i, args := range [][]any{{int64(1), "[1,3]", "{[1,3]}"}, {int64(2), []byte("[1,4)"), []byte("{[1,3),[2,4)}")}, {int64(3), nil, nil}, {int64(4), []byte(nil), []byte(nil)}} {
			s := fixture.Exec(q, 1, args...)
			if i%2 == 0 {
				run(s)
			} else {
				run(fixture.Prepared(s))
			}
			args[0] = int64(i + 11)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, args...))
			for _, arg := range args {
				if bytes, ok := arg.([]byte); ok {
					for j := range bytes {
						bytes[j] = 'x'
					}
				}
			}
		}
		want := [][]any{{int64(1), "[1,3]", "{[1,3]}"}, {int64(2), "[1,4)", "{[1,4)}"}, {int64(3), nil, nil}, {int64(4), nil, nil}, {int64(11), "[1,3]", "{[1,3]}"}, {int64(12), "[1,4)", "{[1,4)}"}, {int64(13), nil, nil}, {int64(14), nil, nil}}
		run(rangeTypeData(b, want))
		const selectSQL = `SELECT r,m FROM range_data WHERE k=$1`
		reader, err := conn.PrepareContext(p.Ctx, selectSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		for _, key := range []int64{1, 2, 3, 1} {
			row := want[key-1][1:]
			s := rangeTypeSelect(b, selectSQL, []string{"r", "m"}, []string{"CUSTOM_SPAN", "CUSTOM_SPANS"}, [][]any{row}, []bool{true, true}, key)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: reader, SQL: selectSQL}, b.Recorder, s)
		}
		run(fixture.Exec(`CREATE TYPE other_span AS RANGE(SUBTYPE=int4,MULTIRANGE_TYPE_NAME=other_spans)`, 0))
		for _, tc := range []struct{ q, column, target, source, input string }{
			{`INSERT INTO range_data VALUES(99,'[1,3]'::other_span,NULL)`, "r", "custom_span", "other_span", "'[1,3]'"},
			{`INSERT INTO range_data VALUES(99,NULL,'{[1,3]}'::other_spans)`, "m", "custom_spans", "other_spans", "'{[1,3]}'"},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42804", Message: `column "` + tc.column + `" is of type ` + tc.target + ` but expression is of type ` + tc.source, Hint: "You will need to rewrite or cast the expression.", Position: strconv.Itoa(strings.Index(tc.q, tc.input) + 1)})
				s.Prepared, s.ErrorStage = prepared, "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				run(s)
				run(rangeTypeData(b, want))
			}
		}
	})
}

func TestReferenceRangeTypeMetadata(t *testing.T) {
	rangeTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, run func(fixture.Step)) {
		rangeTypeSetup(run)
		query := rangeTypeData(b, nil).Query
		statement, err := conn.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer statement.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: statement, SQL: query}
		check := func(want [][]any) {
			t.Helper()
			for _, prepared := range []bool{false, true} {
				s := rangeTypeData(b, want)
				s.Prepared = prepared
				run(s)
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, rangeTypeData(b, want))
		}
		check(nil)
		run(fixture.Exec(`INSERT INTO range_data VALUES (1,NULL,NULL)`, 1))
		check([][]any{{int64(1), nil, nil}})
		run(fixture.Exec(`INSERT INTO range_data VALUES (2,'[1,3]','{}')`, 1))
		check([][]any{{int64(1), nil, nil}, {int64(2), "[1,3]", "{}"}})
		var rangeID, multiID, classID int64
		for _, probe := range []struct {
			q   string
			dst *int64
		}{
			{`SELECT oid::bigint FROM pg_type WHERE typnamespace='public'::regnamespace AND typname='custom_span'`, &rangeID},
			{`SELECT oid::bigint FROM pg_type WHERE typnamespace='public'::regnamespace AND typname='custom_spans'`, &multiID},
			{`SELECT oid::bigint FROM pg_opclass WHERE opcnamespace='pg_catalog'::regnamespace AND opcname='int4_ops' AND opcmethod=403`, &classID},
		} {
			if err := conn.QueryRowContext(p.Ctx, probe.q).Scan(probe.dst); err != nil {
				t.Fatal(err)
			}
			if *probe.dst <= 0 {
				t.Fatal("invalid identity", *probe.dst)
			}
		}
		run(fixture.Select(`SELECT rngtypid::bigint,rngsubtype::bigint,rngmultitypid::bigint,rngcollation::bigint,rngsubopc::bigint,rngcanonical::oid::bigint,rngsubdiff::oid::bigint FROM pg_range WHERE rngtypid=$1::oid`, []string{"rngtypid", "rngsubtype", "rngmultitypid", "rngcollation", "rngsubopc", "rngcanonical", "rngsubdiff"}, []string{"INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8"}, [][]any{{rangeID, int64(23), multiID, int64(0), classID, int64(0), int64(0)}}, rangeID))
		run(fixture.Select(`SELECT typname,typtype::text,typcollation::bigint FROM pg_type WHERE oid IN ($1::oid,$2::oid) ORDER BY typname`, []string{"typname", "typtype", "typcollation"}, []string{"NAME", "TEXT", "INT8"}, [][]any{{"custom_span", "r", int64(0)}, {"custom_spans", "m", int64(0)}}, rangeID, multiID))
		run(fixture.Select(`SELECT t.typname,a.typelem::bigint=t.oid FROM pg_type t JOIN pg_type a ON a.oid=t.typarray WHERE t.oid IN ($1::oid,$2::oid) ORDER BY t.typname`, []string{"typname", "?column?"}, []string{"NAME", "BOOL"}, [][]any{{"custom_span", true}, {"custom_spans", true}}, rangeID, multiID))
	})
}
