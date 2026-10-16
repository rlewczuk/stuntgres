//go:build !noservertests

package reference_test

import (
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceServerDomainMetadata(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			w := serverAuthenticated(t, f.DSNFor(b))
			serverWantCode(t, w.query(t, "CREATE DOMAIN server_wire_domain7 AS varchar(7); CREATE DOMAIN server_wire_domain_chain AS server_wire_domain7"), "")
			for _, domain := range []string{"server_wire_domain7", "server_wire_domain_chain"} {
				t.Run(domain, func(t *testing.T) {
					table := domain + "_rows"
					serverWantCode(t, w.query(t, "CREATE TABLE "+table+"(id integer,v "+domain+")"), "")
					identity := w.query(t, "SELECT '"+table+"'::regclass::oid")
					serverWantCode(t, identity, "")
					ids := serverValues(t, identity)
					if len(ids) != 1 || len(ids[0]) != 1 {
						t.Fatalf("relation identities %v", ids)
					}
					oid, err := strconv.ParseUint(ids[0][0], 10, 32)
					if err != nil || oid == 0 {
						t.Fatalf("relation identity %v: %v", ids, err)
					}
					domainOID := serverCodecTypeOID(t, w, domain)
					if domainOID == 1043 {
						t.Fatal("domain formal identity collapsed to base type")
					}
					for _, phase := range []struct {
						name, insert string
						rows         [][][]byte
					}{
						{name: "empty"},
						{name: "null", insert: "INSERT INTO " + table + " VALUES (1,NULL)", rows: [][][]byte{{nil}}},
						{name: "null_and_value", insert: "INSERT INTO " + table + " VALUES (2,'seven')", rows: [][][]byte{{nil}, {[]byte("seven")}}},
					} {
						t.Run(phase.name, func(t *testing.T) {
							if phase.insert != "" {
								serverWantCode(t, w.query(t, phase.insert), "")
							}
							sql := "SELECT v FROM " + table + " ORDER BY id"
							expected := serverColumn{"v", uint32(oid), 2, 1043, -1, 11, 0}
							frames := w.query(t, sql)
							serverWantTags(t, frames, "T"+strings.Repeat("D", len(phase.rows))+"CZ")
							serverWantRaw(t, frames, phase.rows)
							serverWantCompletions(t, frames, "SELECT "+strconv.Itoa(len(phase.rows)))
							serverWantReady(t, frames, 'I')
							columns := serverColumns(t, frames)
							if len(columns) != 1 || columns[0] != expected {
								t.Fatalf("simple domain metadata %+v want %+v", columns, expected)
							}
							frames = serverCycle(t, w, serverParse("", sql), serverDescribe('S', ""), serverBind("", "", nil, nil, 1), serverDescribe('P', ""), serverExecute("", 0))
							serverWantTags(t, frames, "1tT2T"+strings.Repeat("D", len(phase.rows))+"CZ")
							serverWantRaw(t, frames, phase.rows)
							serverWantCompletions(t, frames, "SELECT "+strconv.Itoa(len(phase.rows)))
							serverWantReady(t, frames, 'I')
							descriptions := serverDescriptions(t, frames)
							if len(descriptions) != 2 {
								t.Fatalf("domain descriptions %v", descriptions)
							}
							for i, columns := range descriptions {
								want := expected
								if i == 1 {
									want.format = 1
								}
								if len(columns) != 1 || columns[0] != want {
									t.Fatalf("domain description %d %+v want %+v", i, columns, want)
								}
							}
							for _, fr := range frames {
								if fr.tag == 't' && string(fr.body) != "\x00\x00" {
									t.Fatalf("table-query formals %x", fr.body)
								}
							}
						})
					}
					// ParameterDescription retains logical domain identity; RowDescription
					// independently advertises physical VARCHAR and the base varchar(7) typmod.
					frames := serverCycle(t, w, serverParse("", "SELECT $1 AS arg", domainOID), serverDescribe('S', ""), serverBind("", "", nil, [][]byte{nil}, 1), serverDescribe('P', ""), serverExecute("", 0))
					serverWantTags(t, frames, "1tT2TDCZ")
					serverWantRaw(t, frames, [][][]byte{{nil}})
					serverWantCompletions(t, frames, "SELECT 1")
					serverWantReady(t, frames, 'I')
					parameter := serverPut32(server16(nil, 1), domainOID)
					seen := 0
					for _, fr := range frames {
						if fr.tag == 't' {
							seen++
							if string(fr.body) != string(parameter) {
								t.Fatalf("domain formals %x want %x", fr.body, parameter)
							}
						}
					}
					if seen != 1 {
						t.Fatalf("ParameterDescription count %d", seen)
					}
					descriptions := serverDescriptions(t, frames)
					if len(descriptions) != 2 {
						t.Fatalf("formal result descriptions %v", descriptions)
					}
					for i, columns := range descriptions {
						want := serverColumn{"arg", 0, 0, 1043, -1, 11, int16(i)}
						if len(columns) != 1 || columns[0] != want {
							t.Fatalf("formal result metadata %+v want %+v", columns, want)
						}
					}
					serverScalar(t, w, "SELECT 42", "42")
				})
			}
		})
	}
}
