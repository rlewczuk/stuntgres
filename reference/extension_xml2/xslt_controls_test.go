package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func xsltControlStyle() string {
	return xsltSmokeStyle(xsltTextOutput, `<xsl:param name="p" select="'2'"/>`, `<xsl:value-of select="/r"/><xsl:value-of select="$p"/>`)
}

func xsltControlSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	style := "$style$" + xsltControlStyle() + "$style$"
	for _, q := range []string{
		"CREATE DOMAIN xslt_result AS text",
		"CREATE TABLE xslt_saved(id integer,doc text,d2 text DEFAULT xslt_process(current_setting('application_name')," + style + "),d3 text DEFAULT xslt_process(current_setting('application_name')," + style + ",'p=''3'''),CONSTRAINT xslt_guard CHECK (xslt_process(doc," + style + ") <> 'reject2' AND xslt_process(doc," + style + ",'p=''3''') <> 'reject3'))",
		"CREATE VIEW xslt_values AS SELECT id,xslt_process(doc," + style + ") AS two,xslt_process(doc," + style + ",'p=''3''') AS three FROM xslt_saved",
		"CREATE VIEW xslt_typed AS SELECT id,xslt_process(doc," + style + ")::xslt_result AS two,xslt_process(doc," + style + ",'p=''3''')::xslt_result AS three FROM xslt_saved",
		"SET application_name TO '<r>D</r>'",
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO xslt_saved(id,doc) VALUES(1,'<r>A</r>')", 1))
}

func xsltControlValues(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	xml2Both(t, p, b, c, fixture.Select("SELECT id,two,three FROM xslt_values ORDER BY id", []string{"id", "two", "three"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "A2", "A3"}}))
	xml2Both(t, p, b, c, fixture.Select("SELECT d2,d3 FROM xslt_saved WHERE id=1", []string{"d2", "d3"}, []string{"TEXT", "TEXT"}, [][]any{{"D2", "D3"}}))
	xml2Both(t, p, b, c, fixture.Select("SELECT two::text,three::text FROM xslt_typed ORDER BY id", []string{"two", "three"}, []string{"TEXT", "TEXT"}, [][]any{{"A2", "A3"}}))
	xml2Both(t, p, b, c, fixture.Select("SELECT a.attname,a.atttypid=t.oid AS current_type,t.typbasetype::integer AS base FROM pg_attribute a JOIN pg_class r ON r.oid=a.attrelid JOIN pg_namespace n ON n.oid=r.relnamespace JOIN pg_type t ON t.typname='xslt_result' JOIN pg_namespace tn ON tn.oid=t.typnamespace WHERE n.nspname='public' AND tn.nspname='public' AND r.relname='xslt_typed' AND a.attnum IN (2,3) ORDER BY a.attnum", []string{"attname", "current_type", "base"}, []string{"NAME", "BOOL", "INT4"}, [][]any{{"two", true, int64(25)}, {"three", true, int64(25)}}))
}

// Every transition demands fresh transforms through the stored defaults, view,
// CHECK and direct/prepared writes; replaying saved constant cells cannot pass.
func xsltControlFresh(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET application_name TO '<r>F</r>'", 0))
		s := fixture.Exec("INSERT INTO xslt_saved(id,doc) VALUES(2,'<r>B</r>'),(3,NULL)", 2)
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		xml2Both(t, p, b, c, fixture.Select("SELECT s.id,s.d2,s.d3,v.two,v.three FROM xslt_saved s JOIN xslt_values v ON v.id=s.id WHERE s.id>1 ORDER BY s.id", []string{"id", "d2", "d3", "two", "three"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(2), "F2", "F3", "B2", "B3"}, {int64(3), "F2", "F3", nil, nil}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM xslt_saved WHERE id>1", 2))
		for _, tc := range []struct {
			query      string
			diagnostic fixture.Diagnostic
		}{
			{"INSERT INTO xslt_saved(id,doc) VALUES(2,'<r>B</r>'),(3,'<r>')", xsltDiagnostic("source", xsltOpenSource)},
			{"INSERT INTO xslt_saved(id,doc) VALUES(2,'<r>B</r>'),(3,'<r>reject</r>')", fixture.CheckViolation("xslt_saved", "xslt_guard", "3, <r>reject</r>, F2, F3")},
		} {
			d := tc.diagnostic
			d.Severity, d.SeverityUnlocalized = "ERROR", "ERROR"
			s := fixture.ErrorDiagnostic(tc.query, d)
			s.Prepared, s.ErrorStage = prepared, "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			xsltControlValues(t, p, b, c)
		}
	}
	q := "INSERT INTO xslt_saved(id,doc) VALUES($1::integer,$2::text)"
	xml2Held(t, p, b, c, fixture.Exec(q, 1, 4, `<r>H</r>`), fixture.Exec(q, 1, 5, `<r>I</r>`))
	xml2Both(t, p, b, c, fixture.Select("SELECT two,three FROM xslt_values WHERE id>1 ORDER BY id", []string{"two", "three"}, []string{"TEXT", "TEXT"}, [][]any{{"H2", "H3"}, {"I2", "I3"}}))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM xslt_saved WHERE id>1", 2))
}

func xsltRetainedRows(t *testing.T, rows *sql.Rows) {
	t.Helper()
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(cols, []string{"id", "two", "three"}) {
		t.Fatal("retained XSLT columns", cols, err)
	}
	meta, err := rows.ColumnTypes()
	if err != nil || len(meta) != 3 {
		t.Fatal("retained XSLT metadata", meta, err)
	}
	for i, typ := range []string{"INT4", "TEXT", "TEXT"} {
		if meta[i].DatabaseTypeName() != typ {
			t.Fatal("retained XSLT type", i, meta[i].DatabaseTypeName())
		}
	}
	if !rows.Next() {
		t.Fatal("retained XSLT row missing", rows.Err())
	}
	var id int64
	var two, three string
	if err := rows.Scan(&id, &two, &three); err != nil || id != 1 || two != "A2" || three != "A3" {
		t.Fatal("retained XSLT values", id, two, three, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("retained XSLT tail", rows.Err())
	}
}

func TestReferenceXML2XSLTControls(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "xslt_space")
	xml2Case(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xml2Install(t, p, b, c)
		xsltControlSetup(t, p, b, c)
		xsltControlValues(t, p, b, c)
		xsltControlFresh(t, p, b, c)
		q := "SELECT id,two,three FROM xslt_values ORDER BY id"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		h := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		want := fixture.Select(q, []string{"id", "two", "three"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "A2", "A3"}})
		fixture.RunStep(t, p.Ctx, h, b.Recorder, want)
		if b.PG {
			return
		} // Native control APIs have no PostgreSQL counterpart.
		old, err := c.QueryContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
		provider := strings.Index(dump, "CREATE EXTENSION")
		if provider < 0 || strings.Index(dump, "CREATE TABLE") <= provider || strings.Index(dump, "CREATE VIEW") <= provider {
			t.Fatal("XSLT provider must precede consumers")
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		xsltRetainedRows(t, old)
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		xsltControlValues(t, p, b, c)
		xsltControlFresh(t, p, b, c)
		fixture.RunStep(t, p.Ctx, h, b.Recorder, want)
		xsltRejectedRestore(t, p, b, c, h, want, dump)
		driver := stuntgres.NewDriver()
		connector, err := driver.OpenConnector("xslt_destination")
		if err != nil {
			t.Fatal(err)
		}
		control := sql.OpenDB(connector)
		defer control.Close()
		pool, recorder := fixture.RecordedNativePool(t, connector)
		dst, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer dst.Close()
		destination := fixture.Backend{Name: "destination", DB: pool, Recorder: recorder}
		if err := stuntgres.Restore(p.Ctx, control, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		xsltControlValues(t, p, destination, dst)
		xsltControlFresh(t, p, destination, dst)
		// Both overloads retain authentic provider origin without their extension
		// membership or original header, after current-name/schema transitions.
		xsltMoveDetach(t, p, b, c)
		xsltControlFresh(t, p, b, c)
		headerless := fixture.ExtensionDump(t, p.Ctx, p.Native)
		// A missing header is created in authentic scratch placement and retired,
		// just as for the incumbent XML2 survivors; absence is a final-state fact.
		const prefix = "CREATE EXTENSION \"xml2\" WITH SCHEMA "
		create, creation := "", ""
		for _, line := range strings.SplitAfter(headerless, "\n") {
			if strings.HasPrefix(line, prefix) {
				if create != "" || !strings.HasSuffix(line, " VERSION '1.2';\n") {
					t.Fatal("detached XSLT repeated or changed cohort creation", headerless)
				}
				create = line
				creation = strings.TrimSuffix(strings.TrimPrefix(line, prefix), " VERSION '1.2';\n")
			}
		}
		created := strings.Index(headerless, "CREATE SCHEMA "+creation+" AUTHORIZATION \"postgres\";\n")
		headerDrop := strings.Index(headerless, "DROP EXTENSION \"xml2\";\n")
		retire := "DROP SCHEMA " + creation + ";\n"
		if !strings.HasPrefix(creation, "\"__stuntgres_install_") || create == "" || created < 0 || created >= strings.Index(headerless, create) || headerDrop <= strings.Index(headerless, create) || strings.Index(headerless, retire) <= headerDrop || strings.Count(headerless, retire) != 1 || strings.Contains(headerless, "ALTER EXTENSION \"xml2\" ADD ") {
			t.Fatal("detached XSLT lost authentic scratch/header retirement", headerless)
		}
		for _, signature := range []string{"pg_catalog.text, pg_catalog.text", "pg_catalog.text, pg_catalog.text, pg_catalog.text"} {
			detach := strings.Index(headerless, "ALTER EXTENSION \"xml2\" DROP FUNCTION "+creation+".\"xslt_process\"("+signature+");\n")
			if detach <= strings.Index(headerless, create) || detach >= headerDrop {
				t.Fatal("detached XSLT lost ordered genuine declaration detach", signature, headerless)
			}
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(headerless)); err != nil {
			t.Fatal(err)
		}
		xsltControlValues(t, p, b, c)
		xsltControlFresh(t, p, b, c)
		xml2Both(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_extension WHERE extname='xml2'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		fixture.RunStep(t, p.Ctx, h, b.Recorder, want)
		if err := stuntgres.Restore(p.Ctx, control, strings.NewReader(headerless)); err != nil {
			t.Fatal(err)
		}
		xsltControlValues(t, p, destination, dst)
		xsltControlFresh(t, p, destination, dst)
		xml2Both(t, p, destination, dst, fixture.Select("SELECT count(*) AS n FROM pg_extension WHERE extname='xml2'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		xml2Both(t, p, destination, dst, fixture.Select("SELECT p.proname,p.provolatile::text AS v,p.prosrc,p.probin FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='xslt_space' ORDER BY p.proname", []string{"proname", "v", "prosrc", "probin"}, []string{"NAME", "TEXT", "TEXT", "TEXT"}, [][]any{{"xslt_three", "v", "xslt_process", "$libdir/pgxml"}, {"xslt_two", "i", "xslt_process", "$libdir/pgxml"}}))
	})
}

func xsltMoveDetach(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, q := range []string{
		"CREATE SCHEMA xslt_space",
		"ALTER FUNCTION xslt_process(text,text) RENAME TO xslt_two",
		"ALTER FUNCTION xslt_process(text,text,text) RENAME TO xslt_three",
		"ALTER FUNCTION xslt_two(text,text) SET SCHEMA xslt_space",
		"ALTER FUNCTION xslt_three(text,text,text) SET SCHEMA xslt_space",
		"ALTER EXTENSION xml2 DROP FUNCTION xslt_space.xslt_two(text,text)",
		"ALTER EXTENSION xml2 DROP FUNCTION xslt_space.xslt_three(text,text,text)",
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		xsltControlValues(t, p, b, c)
		xsltControlFresh(t, p, b, c)
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP EXTENSION xml2", 0))
	xml2Both(t, p, b, c, fixture.Select("SELECT xslt_space.xslt_two($1::text,$2::text) AS two,xslt_space.xslt_three($1::text,$2::text,$3::text) AS three", []string{"two", "three"}, []string{"TEXT", "TEXT"}, [][]any{{"Z2", "Z3"}}, `<r>Z</r>`, xsltControlStyle(), "p='3'"))
}

func xsltRejectedRestore(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, h fixture.HeldStatement, want fixture.Step, dump string) {
	t.Helper()
	before := fixture.ExtensionDump(t, p.Ctx, p.Native)
	ids := xml2IDs(t, p, c)
	readFailure := errors.New("XSLT replacement reader failure")
	for _, mode := range []string{"sql", "precanceled", "reader", "late-cancel"} {
		ctx, cancel := context.WithCancel(p.Ctx)
		var input io.Reader = strings.NewReader(dump + "\nINVALID SQL;")
		var ordinary error
		switch mode {
		case "precanceled":
			cancel()
			input, ordinary = strings.NewReader(dump), context.Canceled
		case "reader":
			input = io.MultiReader(strings.NewReader(dump), &fixture.ReferenceRangeErrorReader{Err: readFailure})
			ordinary = readFailure
		case "late-cancel":
			input = io.MultiReader(strings.NewReader(dump), &xml2CancelReader{Reader: strings.NewReader("\n"), cancel: cancel})
			ordinary = context.Canceled
		}
		err := stuntgres.Restore(ctx, p.Native, input)
		cancel()
		if err == nil || ordinary != nil && !errors.Is(err, ordinary) {
			t.Fatalf("rejected %s XSLT replacement=%v want=%v", mode, err, ordinary)
		}
		if after := fixture.ExtensionDump(t, p.Ctx, p.Native); after != before {
			t.Fatal("rejected XSLT replacement changed dump")
		}
		if after := xml2IDs(t, p, c); !reflect.DeepEqual(after, ids) {
			t.Fatal("rejected XSLT replacement changed identities", ids, after)
		}
		xsltControlValues(t, p, b, c)
		fixture.RunStep(t, p.Ctx, h, b.Recorder, want)
	}
}
