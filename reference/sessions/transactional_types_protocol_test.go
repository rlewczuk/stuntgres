//go:build !noservertests

package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalTypesProtocol(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(fmt.Sprintf("held=%v", held), func(t *testing.T) {
			f := fixture.NewServerFixture(t)
			for _, b := range f.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					w := serverAuthenticated(t, f.DSNFor(b))
					peer := serverAuthenticated(t, f.DSNFor(b))
					defer func() { serverWantCode(t, w.query(t, "ROLLBACK"), "") }()
					serverWantCode(t, w.query(t, "BEGIN"), "")
					queries := []string{
						"CREATE DOMAIN tt_wire_d AS integer DEFAULT 7",
						"CREATE TYPE tt_wire_pair AS(id integer,note text)",
						"CREATE TABLE tt_wire_data(v tt_wire_d)",
						"CREATE TABLE tt_wire_typed OF tt_wire_pair",
						"ALTER DOMAIN tt_wire_d SET DEFAULT 9",
						"ALTER DOMAIN tt_wire_d ADD CONSTRAINT positive CHECK(VALUE>0) NOT VALID",
						"ALTER DOMAIN tt_wire_d VALIDATE CONSTRAINT positive",
						"ALTER TYPE tt_wire_pair ADD ATTRIBUTE extra integer CASCADE",
						"ALTER TYPE tt_wire_pair RENAME ATTRIBUTE extra TO renamed CASCADE",
						"ALTER TYPE tt_wire_pair DROP ATTRIBUTE renamed CASCADE",
						"ALTER TYPE tt_wire_pair ALTER ATTRIBUTE id TYPE bigint CASCADE",
					}
					for i, q := range queries {
						if held {
							name, portal := fmt.Sprintf("utility_%d", i), fmt.Sprintf("portal_%d", i)
							frames := serverCycle(t, w, serverParse(name, q), serverDescribe('S', name), serverBind(portal, name, nil, nil), serverDescribe('P', portal))
							serverWantTags(t, frames, "1tn2nZ")
							serverWantReady(t, frames, 'T')
							if i == 0 {
								serverScalar(t, w, "SELECT count(*) FROM pg_type WHERE typnamespace='public'::regnamespace AND typname='tt_wire_d'", "0")
							}
							frames = serverCycle(t, w, serverExecute(portal, 0))
							serverWantTags(t, frames, "CZ")
							serverWantReady(t, frames, 'T')
							transactionalTypesCommandTag(t, frames, strings.Join(strings.Fields(q)[:2], " "))
						} else {
							serverWantCode(t, w.query(t, q), "")
						}
					}
					serverScalar(t, peer, "SELECT count(*) FROM pg_type WHERE typnamespace='public'::regnamespace AND typname IN ('tt_wire_d','tt_wire_pair')", "0")
					frames := serverCycle(t, w, serverParse("insert", "INSERT INTO tt_wire_typed(id,note) VALUES($1,$2)"), serverDescribe('S', "insert"), serverBind("insert_portal", "insert", nil, [][]byte{[]byte("7"), []byte("x")}), serverExecute("insert_portal", 0))
					serverWantTags(t, frames, "1tn2CZ")
					serverWantReady(t, frames, 'T')
					transactionalTypesCommandTag(t, frames, "INSERT 0 1")
					serverWantCode(t, w.query(t, "INSERT INTO tt_wire_data DEFAULT VALUES"), "")
					serverScalar(t, w, "SELECT v FROM tt_wire_data", "9")
					serverScalar(t, w, "SELECT id FROM tt_wire_typed", "7")
					serverWantCode(t, w.query(t, "SAVEPOINT healthy"), "")
					frames = w.query(t, "INSERT INTO tt_wire_data VALUES(-1)")
					serverWantCode(t, frames, "23514")
					serverWantReady(t, frames, 'E')
					errors := 0
					for _, frame := range frames {
						if frame.tag != 'E' {
							continue
						}
						errors++
						fields := serverFields(frame.body)
						for key, want := range map[byte]string{'S': "ERROR", 'V': "ERROR", 'C': "23514", 'M': `value for domain tt_wire_d violates check constraint "positive"`, 'P': "", 'D': "", 'H': "", 'W': "", 'p': "", 'q': "", 's': "public", 't': "", 'c': "", 'd': "tt_wire_d", 'n': "positive"} {
							if fields[key] != want {
								t.Fatalf("diagnostic field %c = %q want %q: %v", key, fields[key], want, fields)
							}
						}
					}
					if errors != 1 {
						t.Fatalf("error count %d: %s", errors, serverFrameSummary(frames))
					}
					serverNotificationError(t, w.query(t, "SELECT 1"), "25P02", "current transaction is aborted, commands ignored until end of transaction block", "")
					serverWantCode(t, w.query(t, "ROLLBACK TO healthy"), "")
					for i, q := range []string{"ALTER DOMAIN tt_wire_d DROP CONSTRAINT positive", "ALTER DOMAIN tt_wire_d DROP DEFAULT", "ALTER DOMAIN tt_wire_d SET NOT NULL", "ALTER DOMAIN tt_wire_d DROP NOT NULL", "ALTER DOMAIN tt_wire_d RENAME TO tt_wire_renamed", "ALTER TYPE tt_wire_pair RENAME TO tt_wire_moved"} {
						if held {
							name := fmt.Sprintf("late_%d", i)
							frames := serverCycle(t, w, serverParse(name, q), serverDescribe('S', name), serverBind(name, name, nil, nil), serverExecute(name, 0))
							serverWantTags(t, frames, "1tn2CZ")
						} else {
							serverWantCode(t, w.query(t, q), "")
						}
					}
					serverWantCode(t, w.query(t, "COMMIT"), "")
					serverScalar(t, peer, "SELECT v FROM tt_wire_data", "9")
					serverWantCode(t, w.query(t, "BEGIN"), "")
					for i, q := range []string{"DROP DOMAIN tt_wire_renamed CASCADE", "DROP TYPE tt_wire_moved CASCADE"} {
						if held {
							name := fmt.Sprintf("drop_%d", i)
							frames := serverCycle(t, w, serverParse(name, q), serverDescribe('S', name), serverBind(name, name, nil, nil), serverExecute(name, 0))
							serverWantCode(t, frames, "")
							serverWantReady(t, frames, 'T')
						} else {
							serverWantCode(t, w.query(t, q), "")
						}
					}
					serverWantCode(t, w.query(t, "ROLLBACK"), "")
					serverScalar(t, w, "SELECT id FROM tt_wire_typed", "7")
				})
			}
		})
	}
}

func transactionalTypesCommandTag(t *testing.T, frames []serverFrame, want string) {
	t.Helper()
	count := 0
	for _, f := range frames {
		if f.tag == 'C' {
			count++
			if string(f.body) != want+"\x00" {
				t.Fatalf("command tag=%q want=%q", f.body, want)
			}
		}
	}
	if count != 1 {
		t.Fatalf("command completions=%d want=1", count)
	}
}
