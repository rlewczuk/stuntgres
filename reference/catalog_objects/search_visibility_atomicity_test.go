package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func searchVisibilityNativeCancel(t *testing.T, p *fixture.Pair, c *sql.Conn, k searchVisibilityClass, source string, oid int64) {
	t.Helper()
	ctx, cancel := context.WithCancel(p.Ctx)
	defer cancel()
	raw, err := p.Native.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.ExecContext(p.Ctx, "SET search_path=sv_a,sv_b"); err != nil {
		t.Fatal(err)
	}
	// A raw cursor exposes cancellation between admitted rows independently
	// of database/sql prefetch and without scheduler timing assumptions.
	var cursor driver.Rows
	err = raw.Raw(func(raw any) error {
		var err error
		cursor, err = raw.(driver.QueryerContext).QueryContext(ctx,
			"SELECT pg_catalog."+k.fn+"($1::oid) FROM "+source+" ORDER BY seq",
			[]driver.NamedValue{{Ordinal: 1, Value: oid}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
	values := make([]driver.Value, 1)
	if err := cursor.Next(values); err != nil || values[0] != true {
		t.Fatal(values, err)
	}
	cancel()
	if err := cursor.Next(values); !errors.Is(err, context.Canceled) {
		t.Fatal("between-row cancellation", err)
	}
	rows, err := c.QueryContext(ctx, "SELECT pg_catalog."+k.fn+"($1)", oid)
	if rows != nil {
		rows.Close()
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal("pre-canceled query", err)
	}
}

func TestReferenceSearchVisibilityAtomicityAndCancellation(t *testing.T) {
	searchVisibilityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		searchVisibilitySetup(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("SET search_path=sv_a,sv_b", 0))
		for i, k := range searchVisibilityClasses {
			a := searchVisibilityDiscover(t, p, c, k, "sv_a", "v")
			source := fmt.Sprintf("sv_off.source_%d", i)
			destination := fmt.Sprintf("sv_off.destination_%d", i)
			run(fixture.Exec("CREATE TABLE "+source+"(seq integer,input text)", 0))
			run(fixture.Exec("INSERT INTO "+source+" VALUES(1,$1),(2,'bad')", 2, strconv.FormatInt(a, 10)))
			run(fixture.Exec("CREATE TABLE "+destination+"(visible boolean)", 0))
			run(fixture.Exec("INSERT INTO "+destination+" VALUES(FALSE)", 1))
			query := "INSERT INTO " + destination + " SELECT pg_catalog." + k.fn + "(input::oid) FROM " + source + " ORDER BY seq"
			failure := fixture.ImplicitError(query, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type oid: "bad"`})
			failure.ErrorStage = "execute"
			run(failure)
			run(fixture.Prepared(failure))
			run(fixture.Select("SELECT visible FROM "+destination, []string{"visible"}, []string{"BOOL"}, [][]any{{false}}))
			if i == 0 {
				failure.Query = "CREATE TABLE sv_off.failed_ctas AS SELECT pg_catalog." + k.fn + "(input::oid) AS visible FROM " + source + " ORDER BY seq"
				run(failure)
				run(fixture.Select("SELECT count(*) FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='sv_off' AND c.relname='failed_ctas'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			}
			if b.PG {
				query := fmt.Sprintf("SELECT pg_catalog.%s(%d::oid),pg_sleep(600)", k.fn, a)
				fixture.SessionCancelQuery(t, p.Ctx, c, false, query, true)
				fixture.SessionCancelQuery(t, p.Ctx, c, true, query, true)
			} else {
				searchVisibilityNativeCancel(t, p, c, k, source, a)
			}
			searchVisibilityRoutes(t, p, b, c, k, nil, a, true)
			run(fixture.Select("SELECT visible FROM "+destination, []string{"visible"}, []string{"BOOL"}, [][]any{{false}}))
		}
	})
}
