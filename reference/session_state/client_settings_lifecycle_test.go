package reference_test

import (
	"bytes"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceClientSettingsLifecycle(t *testing.T) {
	for _, tc := range clientSettings {
		if clientContext(tc.name) == "postmaster" || strings.HasPrefix(tc.name, "transaction_") && tc.kind != "integer" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			physicalCases(t, nil, func(s *physicalCase) {
				t := s.t
				pool := s.p.Memory
				if s.pg {
					pool = s.p.Postgres
				}
				peer, err := pool.Conn(s.p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer peer.Close()
				other := &physicalCase{t, s.p, peer, s.pg, s.recorder}
				initial := clientInitial(tc, s.pg)
				query := "SELECT current_setting(" + physicalLiteral(tc.name) + ")"
				held, err := s.conn.PrepareContext(s.p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				s.run(fixture.Exec("BEGIN", 0))
				s.run(fixture.Exec("SET LOCAL "+tc.name+"="+physicalLiteral(tc.alternate), 0))
				s.clientReads(tc, tc.changed, false)
				other.clientReads(tc, initial, false)
				s.run(fixture.Exec("COMMIT", 0))
				s.clientReads(tc, initial, false)
				s.run(fixture.Exec("BEGIN", 0))
				s.run(fixture.Exec("SET "+tc.name+"="+physicalLiteral(tc.alternate), 0))
				s.run(fixture.Exec("SAVEPOINT client_setting_save", 0))
				s.run(fixture.Exec("SET LOCAL "+tc.name+"=DEFAULT", 0))
				s.clientReads(tc, initial, false)
				s.run(fixture.Exec("ROLLBACK TO client_setting_save", 0))
				s.clientReads(tc, tc.changed, true)
				s.run(fixture.Exec("ROLLBACK", 0))
				s.clientReads(tc, initial, true)
				s.run(fixture.Exec("SET "+tc.name+"="+physicalLiteral(tc.alternate), 0))
				fixture.RunStep(t, s.p.Ctx, fixture.HeldStatement{Executor: s.conn, Statement: held, SQL: query}, s.recorder, clientText(query, "current_setting", tc.changed))
				other.clientReads(tc, initial, true)
				s.run(fixture.Exec("RESET ALL", 0))
				s.clientReads(tc, initial, true)
			})
		})
	}
	t.Run("physical-reuse-and-native-controls", clientSettingsControls)
	t.Run("xml-session-local-alias", func(t *testing.T) {
		physicalCases(t, nil, func(s *physicalCase) {
			s.run(fixture.Prepared(fixture.Exec("SET SESSION XML OPTION DOCUMENT", 0)))
			s.run(clientText("SHOW xmloption", "xmloption", "document"))
			s.run(fixture.Exec("BEGIN", 0))
			s.run(fixture.Prepared(fixture.Exec("SET LOCAL XML OPTION CONTENT", 0)))
			s.run(clientText("SHOW xmloption", "xmloption", "content"))
			s.run(fixture.Exec("COMMIT", 0))
			s.run(clientText("SHOW xmloption", "xmloption", "document"))
			s.run(fixture.Exec("RESET xmloption", 0))
			s.run(clientText("SHOW xmloption", "xmloption", "content"))
		})
	})
}

func clientSettingsControls(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		t := s.t
		pool := s.p.Memory
		if s.pg {
			pool = s.p.Postgres
		}
		pool.SetMaxOpenConns(1)
		pool.SetMaxIdleConns(1)
		s.run(fixture.Exec("CREATE TABLE client_lifecycle_sentinel(v integer)", 0))
		s.run(fixture.Exec("INSERT INTO client_lifecycle_sentinel VALUES(7)", 1))
		s.run(fixture.Exec("SET gin_pending_list_limit='8MB'", 0))
		before := fixture.SettingsPhysical(t, s.conn)
		if err := s.conn.Close(); err != nil {
			t.Fatal(err)
		}
		conn, err := pool.Conn(s.p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		s.conn = conn
		if fixture.SettingsPhysical(t, conn) != before {
			t.Fatal("physical connection was not reused")
		}
		s.run(clientText("SHOW gin_pending_list_limit", "gin_pending_list_limit", "8MB"))
		if !s.pg {
			// Native controls replace catalogs, never the live settings attachment.
			rows, err := conn.QueryContext(s.p.Ctx, "SELECT v FROM client_lifecycle_sentinel")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			metadata, err := conn.QueryContext(s.p.Ctx, "SELECT setting FROM pg_settings WHERE name='gin_pending_list_limit'")
			if err != nil {
				t.Fatal(err)
			}
			defer metadata.Close()
			var dump bytes.Buffer
			if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(dump.String(), "gin_pending_list_limit") {
				t.Fatal("Dump serialized session configuration")
			}
			if err := stuntgres.Reset(s.p.Ctx, s.p.Native); err != nil {
				t.Fatal(err)
			}
			s.run(clientText("SHOW gin_pending_list_limit", "gin_pending_list_limit", "8MB"))
			if err := stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader("CREATE TABLE broken(")); err == nil {
				t.Fatal("invalid Restore succeeded")
			}
			s.run(clientText("SHOW gin_pending_list_limit", "gin_pending_list_limit", "8MB"))
			if !rows.Next() {
				t.Fatal("retained sentinel missing")
			}
			var v int
			if err := rows.Scan(&v); err != nil || v != 7 {
				t.Fatal(v, err)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("retained sentinel cardinality", rows.Err())
			}
			rows.Close()
			if !metadata.Next() {
				t.Fatal("retained settings missing")
			}
			var raw string
			if err := metadata.Scan(&raw); err != nil || raw != "8192" {
				t.Fatal(raw, err)
			}
			if metadata.Next() || metadata.Err() != nil {
				t.Fatal("retained settings cardinality", metadata.Err())
			}
			metadata.Close()
		}
		s.run(fixture.Select("SELECT v FROM client_lifecycle_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		if err := conn.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
			t.Fatal(err)
		}
		conn.Close()
		replacement, err := pool.Conn(s.p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer replacement.Close()
		s.conn = replacement
		if fixture.SettingsPhysical(t, replacement) == before {
			t.Fatal("connection was not replaced")
		}
		s.run(clientText("SHOW gin_pending_list_limit", "gin_pending_list_limit", "4MB"))
		s.run(fixture.Select("SELECT v FROM client_lifecycle_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
	})
}
