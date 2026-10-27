package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceInfoTransformsControls(t *testing.T) {
	trScenario(t, func(c *trCase) {
		c.routes(trQuery, nil)
		c.routes(trNullQuery, trNullRow)
		c.exec("CREATE DOMAIN public.tr_control_domain AS integer", "CREATE FUNCTION public.tr_control_identity(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'", "CREATE TABLE public.tr_control_witness(v public.tr_control_domain)")
		c.run(c.conn, fixture.Exec("INSERT INTO public.tr_control_witness VALUES (17),(NULL)", 2))
		witness := fixture.Select("SELECT v FROM public.tr_control_witness ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(17)}, {nil}})
		c.run(c.conn, witness)
		c.inventory(c.conn)
		if c.b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(c.p.Ctx, c.p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		for _, text := range []string{"CREATE DOMAIN", "CREATE FUNCTION", "CREATE TABLE"} {
			if !strings.Contains(dump.String(), text) {
				t.Fatalf("dump missing witness %q: %s", text, dump.String())
			}
		}
		for _, text := range []string{"information_schema.transforms", "CREATE TRANSFORM", "pg_transform"} {
			if strings.Contains(dump.String(), text) {
				t.Fatalf("dump synthesized builtin/transform %q: %s", text, dump.String())
			}
		}
		retained, err := c.conn.QueryContext(c.p.Ctx, trQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		saved, err := retained.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(c.p.Ctx, c.p.Native); err != nil {
			t.Fatal(err)
		}
		c.routes(trQuery, nil)
		c.routes(trNullQuery, trNullRow)
		c.inventory(c.conn)
		c.run(c.conn, fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relname='tr_control_witness'", []string{"relname"}, []string{"NAME"}, nil))
		c.consume(retained, 0)
		trMetadata(t, saved, false)
		if err := stuntgres.Restore(c.p.Ctx, c.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		c.routes(trQuery, nil)
		c.routes(trNullQuery, trNullRow)
		c.inventory(c.conn)
		c.run(c.conn, witness)
		trMetadata(t, saved, false)
		connector, err := stuntgres.NewDriver().OpenConnector("transforms-independent-controls")
		if err != nil {
			t.Fatal(err)
		}
		fresh, recorder := fixture.InsertSelectRecordedPool(t, connector)
		freshNative := sql.OpenDB(connector)
		defer freshNative.Close()
		if err := stuntgres.Restore(c.p.Ctx, freshNative, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		independent := *c
		independent.b.Recorder = recorder
		independent.run(fresh, independent.step(trQuery, nil))
		independent.run(fresh, fixture.Prepared(independent.step(trQuery, nil)))
		independent.run(fresh, independent.step(trNullQuery, trNullRow))
		independent.inventory(fresh)
		independent.run(fresh, witness)
		trMetadata(t, saved, false)
	})
}
