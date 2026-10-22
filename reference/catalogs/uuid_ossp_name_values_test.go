package reference_test

import (
	"database/sql"
	"encoding/hex"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceUUIDOsspNameValues(t *testing.T) {
	uuidNameCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		uuidOsspNamespaceInstall(t, p, c, r)
		// Additional vectors independently calculated with Python stdlib uuid3/uuid5,
		// implementing RFC 9562 namespace network bytes + UTF8 name bytes.
		vectors := []struct{ ns, name, v3, v5 string }{
			{uuidNameDNS, "www.example.com", uuidNameFunctions[0].example, uuidNameFunctions[1].example},
			{uuidNameDNS, "www.widgets.com", uuidNameFunctions[0].widgets, uuidNameFunctions[1].widgets},
			{"6ba7b811-9dad-11d1-80b4-00c04fd430c8", "www.example.com", "a777199a-c522-31c4-8f4b-335feec7215b", "b63cdfa4-3df9-568e-97ae-006c5b8fd652"},
			{"00000000-0000-0000-0000-000000000000", "www.example.com", "e1dbb77c-40ca-39dd-a59b-3973a877b11b", "399fc331-be2b-5b56-b676-d6f38dae5aaa"},
			{"01234567-89ab-cdef-0123-456789abcdef", "www.example.com", "fa5f6a1c-66a0-3c2e-a172-0f1dc9d3600f", "12e96e3c-dbf6-514c-86ec-687a048d161f"},
			{uuidNameDNS, "", "c87ee674-4ddc-3efe-a74e-dfe25da5d7b3", "4ebd0208-8328-5d69-8c44-ec50939c0967"},
			{uuidNameDNS, "é", "3a356d77-46a8-3ec1-8205-b80596740632", "ebfe0af8-3997-5ade-b634-ba92cf69f557"},
			{uuidNameDNS, "e\u0301", "9f114bc3-8448-31ed-a79f-45fb09b6a200", "39004b7f-2a0b-588c-88f7-aa24552a7636"},
			{uuidNameDNS, "WWW.example.com", "8c9bfcab-5efa-37b0-b339-f80076314dbe", "ceb6936f-3062-5831-8f42-c7d000fbdb2d"},
			{uuidNameDNS, " www.example.com ", "73dcc10c-a06e-3081-a5a1-ec11145c2896", "cfaf1723-5e3a-5cd1-8f7a-f99b48b54939"},
			{uuidNameDNS, strings.Repeat("x", 8193), "b2d5dd72-88a7-3959-b7b0-20f229711a48", "76e95899-db57-5a28-8ab9-36c4d8ab2dd9"},
		}
		for fi, f := range uuidNameFunctions {
			q := "SELECT uuid_ossp_part." + f.name + "($1::uuid,$2::text) AS u"
			h := uuidNameHeld(t, p, c, q)
			defer h.Statement.Close()
			for vi, v := range vectors {
				t.Run(f.name+"/vector_"+fixture.Position(vi+1), func(t *testing.T) {
					want := v.v3
					if fi == 1 {
						want = v.v5
					}
					for _, prepared := range []bool{false, true} {
						s := uuidNameStep(q, want, v.ns, v.name)
						s.Prepared = prepared
						s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { uuidOsspColumns(t, rows, pg, 1, false) }
						s.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
							var raw any
							if err := rows.Scan(&raw); err != nil {
								t.Fatal(err)
							}
							var text string
							if pg {
								b, ok := raw.([]byte)
								if !ok {
									t.Fatalf("raw UUID %T", raw)
								}
								text = string(b)
							} else {
								var ok bool
								text, ok = raw.(string)
								if !ok {
									t.Fatalf("raw UUID %T", raw)
								}
							}
							b, err := hex.DecodeString(strings.ReplaceAll(text, "-", ""))
							if err != nil || len(b) != 16 {
								t.Fatalf("UUID %q: %v", text, err)
							}
							version := byte(3)
							if fi == 1 {
								version = 5
							}
							if b[6]>>4 != version || b[8]>>6 != 2 {
								t.Fatalf("UUID version/variant %q", text)
							}
						}
						fixture.RunStep(t, p.Ctx, c, r, s)
					}
					for range 2 {
						fixture.RunStep(t, p.Ctx, h, r, uuidNameStep(q, want, v.ns, v.name))
					}
				})
			}
			pool := p.Memory
			if pg {
				pool = p.Postgres
			}
			fresh, err := pool.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer fresh.Close()
				fixture.RunStep(t, p.Ctx, fresh, r, uuidNameStep(q, f.example, uuidNameDNS, "www.example.com"))
				fixture.RunStep(t, p.Ctx, fresh, r, fixture.Prepared(uuidNameStep(q, f.widgets, uuidNameDNS, "www.widgets.com")))
			}()
			s := fixture.Select(q+" WHERE FALSE", []string{"u"}, []string{"UUID"}, nil, uuidNameDNS, "www.example.com")
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { uuidOsspColumns(t, rows, pg, 1, false) }
			fixture.RunStep(t, p.Ctx, c, r, s)
		}
	})
}

func TestReferenceUUIDOsspNameBindings(t *testing.T) {
	uuidNameCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		uuidOsspNamespaceInstall(t, p, c, r)
		for _, f := range uuidNameFunctions {
			call := "uuid_ossp_part." + f.name
			for _, args := range []string{
				"'" + uuidNameDNS + "','www.example.com'",
				"'" + uuidNameDNS + "'::uuid,'www.example.com'::text",
				"namespace => '" + uuidNameDNS + "', name => 'www.example.com'",
				"name => 'www.example.com', namespace => '" + uuidNameDNS + "'",
				"'" + uuidNameDNS + "', name => 'www.example.com'",
			} {
				for _, prepared := range []bool{false, true} {
					s := uuidNameStep("SELECT "+call+"("+args+") AS u", f.example)
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, r, s)
				}
			}
			q := "SELECT " + call + "($1::uuid,$2::text) AS u"
			h := uuidNameHeld(t, p, c, q)
			for _, v := range []struct{ ns, name, want any }{
				{uuidNameDNS, "www.example.com", f.example}, {nil, "www.example.com", nil}, {uuidNameDNS, nil, nil}, {nil, nil, nil},
				{"{6BA7B8109DAD11D180B400C04FD430C8}", []byte("www.widgets.com"), f.widgets}, {uuidNameDNS, "www.example.com", f.example},
			} {
				for _, prepared := range []bool{false, true} {
					s := uuidNameStep(q, v.want, v.ns, v.name)
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, r, s)
				}
				fixture.RunStep(t, p.Ctx, h, r, uuidNameStep(q, v.want, v.ns, v.name))
			}
			h.Statement.Close()
		}
	})
}
