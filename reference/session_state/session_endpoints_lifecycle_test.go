package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceSessionEndpointsNativeControls(t *testing.T) {
	var emitted []byte
	endpointScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		first := endpointCapture(t, p, b, c)
		activityExec(t, p, c, "CREATE VIEW endpoint_runtime AS "+endpointSQL)
		viewSQL := "SELECT inet_client_addr,inet_client_port,inet_server_addr,inet_server_port FROM endpoint_runtime"
		check := func(conn *sql.Conn) {
			endpointSame(t, first, endpointQuery(t, p, b, conn, viewSQL, endpointNames, false))
			stmt, err := conn.PrepareContext(p.Ctx, viewSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			rows, err := stmt.QueryContext(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			endpointSame(t, first, endpointRead(t, rows, endpointNames, b.PG, false))
		}
		check(c)
		if b.PG {
			if len(emitted) == 0 {
				t.Fatal("missing actual native dump bytes for PostgreSQL replay")
			}
			activityExec(t, p, c, "DROP VIEW endpoint_runtime")
			activityExec(t, p, c, string(emitted))
			check(c)
			return
		}
		retained, err := c.QueryContext(p.Ctx, endpointSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		endpointMetadata(t, retained, endpointNames, false, true)
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		emitted = append([]byte(nil), dump.Bytes()...)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		endpointSame(t, first, endpointCapture(t, p, b, c))
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		endpointSame(t, first, endpointCapture(t, p, b, c))
		check(c)
		endpointSame(t, first, endpointRead(t, retained, endpointNames, false, true))
		driver := stuntgres.NewDriver()
		connector, err := driver.OpenConnector("endpoint-destination")
		if err != nil {
			t.Fatal(err)
		}
		destination := sql.OpenDB(connector)
		defer destination.Close()
		if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		target, err := destination.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer target.Close()
		endpointSame(t, first, endpointCapture(t, p, b, target))
		check(target)
	})
}

func TestReferenceSessionEndpointsFeatureSmoke(t *testing.T) {
	endpointScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		first := endpointCapture(t, p, b, c)
		stmt, err := c.PrepareContext(p.Ctx, endpointSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		rows, err := stmt.QueryContext(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		endpointSame(t, first, endpointRead(t, rows, endpointNames, b.PG, true))
		qualified := "SELECT pg_catalog.inet_client_addr() AS inet_client_addr,pg_catalog.inet_client_port() AS inet_client_port,pg_catalog.inet_server_addr() AS inet_server_addr,pg_catalog.inet_server_port() AS inet_server_port"
		endpointSame(t, first, endpointQuery(t, p, b, c, qualified, endpointNames, true))
		endpointConsumers(t, p, b, c, first)
		endpointError(t, p, b, c, "SELECT inet_client_addr(NULL)", endpointSignature("function inet_client_addr(unknown) does not exist", 8), first)
		activityExec(t, p, c, "CREATE VIEW endpoint_smoke AS "+endpointSQL)
		endpointSame(t, first, endpointQuery(t, p, b, c, "SELECT * FROM endpoint_smoke", endpointNames, false))
	})
}
