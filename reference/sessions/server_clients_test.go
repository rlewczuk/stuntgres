//go:build !noservertests

package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"net/url"
	"strings"
	"testing"

	"github.com/lib/pq"
	"stuntgres/reference/fixture"
)

func TestReferenceServerClients(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			out, err := f.PSQL(t, b, "SELECT 42;\n")
			if err != nil || out != "42\n" {
				t.Fatalf("actual psql scalar output %q: %v", out, err)
			}
			out, err = f.PSQL(t, b, "CREATE TABLE server_psql (id integer);\nINSERT INTO server_psql VALUES (7), (8);\nSELECT id FROM server_psql ORDER BY id;\n")
			if err != nil || out != "CREATE TABLE\nINSERT 0 2\n7\n8\n" {
				t.Fatalf("actual psql write output %q: %v", out, err)
			}
			var sum int
			if err := b.DB.QueryRowContext(f.Ctx, "SELECT sum(id) FROM server_psql").Scan(&sum); err != nil || sum != 15 {
				t.Fatalf("pq sees psql effects sum=%d: %v", sum, err)
			}
			out, err = f.PSQL(t, b, "BEGIN;\nINSERT INTO server_psql VALUES (99);\nROLLBACK;\nSELECT sum(id) FROM server_psql;\n")
			if err != nil || out != "BEGIN\nINSERT 0 1\nROLLBACK\n15\n" {
				t.Fatalf("actual psql rollback output %q: %v", out, err)
			}
			out, err = f.PSQL(t, b, "SELECT FROM WHERE;\n")
			if err == nil || !strings.Contains(out, "ERROR:") {
				t.Fatalf("psql syntax must fail, output %q: %v", out, err)
			}
			out, err = f.PSQL(t, b, "SELECT 42;\n")
			if err != nil || out != "42\n" {
				t.Fatalf("psql recovery %q: %v", out, err)
			}
			raw := f.RawPool(t, b)
			c, err := raw.Conn(f.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			var value int
			var failed int
			err = raw.QueryRowContext(f.Ctx, "SELECT 1/0").Scan(&failed)
			if err == nil {
				t.Fatal("division-by-zero unexpectedly succeeded")
			}
			fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22012", Message: "division by zero"})
			err = c.QueryRowContext(f.Ctx, "SELECT $1::int4 + 1", 41).Scan(&value)
			if err != nil || value != 42 {
				t.Fatalf("bound parameter value=%d: %v", value, err)
			}
			if err := c.QueryRowContext(f.Ctx, "SELECT 42").Scan(&value); err != nil || value != 42 {
				t.Fatalf("same connection after parameters value=%d: %v", value, err)
			}
			prepared, err := c.PrepareContext(f.Ctx, "SELECT 40 + 2")
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := prepared.QueryRowContext(f.Ctx).Scan(&value); err != nil || value != 42 {
					t.Fatalf("held Prepare value=%d: %v", value, err)
				}
			}
			if err := prepared.Close(); err != nil {
				t.Fatal(err)
			}
			binaryURL, err := url.Parse(f.DSNFor(b))
			if err != nil {
				t.Fatal(err)
			}
			binaryOptions := binaryURL.Query()
			binaryOptions.Set("binary_parameters", "yes")
			binaryURL.RawQuery = binaryOptions.Encode()
			binaryConnector, err := pq.NewConnector(binaryURL.String())
			if err != nil {
				t.Fatal(err)
			}
			binaryDB := sql.OpenDB(binaryConnector)
			defer binaryDB.Close()
			var binaryValue []byte
			if err := binaryDB.QueryRowContext(f.Ctx, "SELECT $1::bytea", []byte{0, 255}).Scan(&binaryValue); err != nil || !bytes.Equal(binaryValue, []byte{0, 255}) {
				t.Fatalf("actual pq binary parameter %x: %v", binaryValue, err)
			}
			if err := c.QueryRowContext(f.Ctx, "SELECT 42").Scan(&value); err != nil || value != 42 {
				t.Fatalf("same connection after Prepare value=%d: %v", value, err)
			}
			// pq's default prefer mode requests SSL and then uses cleartext after N.
			// Pinned container also has SSL disabled, so this is one shared client witness.
			u, err := url.Parse(f.DSNFor(b))
			if err != nil {
				t.Fatal(err)
			}
			q := u.Query()
			q.Set("sslmode", "prefer")
			u.RawQuery = q.Encode()
			ssl := serverDial(t, u.String())
			ssl.send(t, serverPut32(serverPut32(nil, 8), 80877103))
			var refusal [1]byte
			if _, err := ssl.Read(refusal[:]); err != nil || refusal[0] != 'N' {
				t.Fatalf("SSL prefer refusal %q: %v", refusal, err)
			}
			serverWantCode(t, ssl.login(t, u.String(), 196608), "")
			serverScalar(t, ssl, "SELECT 42", "42")
			prefer := *u
			prefer.User = url.User(f.UserFor(b))
			out, err = f.PSQL(t, b, "SELECT 42;\n", "-d", prefer.String())
			if err != nil || out != "42\n" {
				t.Fatalf("actual psql SSL-prefer fallback %q: %v", out, err)
			}
			// lib/pq implements require; verify an actual pq client cannot bypass refusal.
			q.Set("sslmode", "require")
			u.RawQuery = q.Encode()
			connector, err := pq.NewConnector(u.String())
			if err != nil {
				t.Fatal(err)
			}
			requireCtx, cancel := context.WithCancel(f.Ctx)
			conn, err := connector.Connect(requireCtx)
			cancel()
			if err == nil {
				conn.Close()
				t.Fatal("SSL require unexpectedly connected")
			}
			if !strings.Contains(err.Error(), "SSL is not enabled") {
				t.Fatalf("SSL require error %v", err)
			}
		})
	}
}
