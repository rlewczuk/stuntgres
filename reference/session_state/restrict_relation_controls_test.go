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

func TestReferenceRestrictRelationKindControls(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		rkSeed(s)
		if !s.pg {
			// Native-only catalog controls must neither serialize nor reset a
			// live policy, nor mutate an already returned data/metadata image.
			rows, err := s.conn.QueryContext(s.p.Ctx, "SELECT id FROM rk_v ORDER BY id")
			if err != nil {
				s.t.Fatal(err)
			}
			defer rows.Close()
			types, err := rows.ColumnTypes()
			if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "INT4" {
				s.t.Fatal(types, err)
			}
			rkSet(s, "view")
			var dump bytes.Buffer
			if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &dump); err != nil {
				s.t.Fatal(err)
			}
			if strings.Contains(dump.String(), rkSetting) {
				s.t.Fatal("Dump serialized live restriction")
			}
			if err := stuntgres.Reset(s.p.Ctx, s.p.Native); err != nil {
				s.t.Fatal(err)
			}
			if err := stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				s.t.Fatal(err)
			}
			rkRead(s, true)
			for _, want := range []int64{1, 2} {
				var id int64
				if !rows.Next() {
					s.t.Fatal("captured view row missing", rows.Err())
				}
				if err := rows.Scan(&id); err != nil || id != want {
					s.t.Fatal(id, want, err)
				}
			}
			if rows.Next() || rows.Err() != nil {
				s.t.Fatal("captured rows changed", rows.Err())
			}
			rows.Close()
			s.run(fixture.Exec("RESET "+rkSetting, 0))
			rkRead(s, false)
		}
		// Physical replacement discards the session policy, not committed views.
		rkSet(s, "view")
		before := fixture.SettingsPhysical(s.t, s.conn)
		if err := s.conn.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
			s.t.Fatal(err)
		}
		s.conn.Close()
		pool := s.p.Memory
		if s.pg {
			pool = s.p.Postgres
		}
		var err error
		s.conn, err = pool.Conn(s.p.Ctx)
		if err != nil {
			s.t.Fatal(err)
		}
		defer s.conn.Close()
		if fixture.SettingsPhysical(s.t, s.conn) == before {
			s.t.Fatal("physical session was not replaced")
		}
		s.run(clientText("SHOW "+rkSetting, rkSetting, ""))
		rkRead(s, false)
	})
}
