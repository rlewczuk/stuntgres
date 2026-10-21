package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLargeObjectCreation(t *testing.T) {
	largeObjectScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer largeObjectCleanup(t, p, c, 910001, 910002)
		run(largeObjectValue("lo_create(910001::oid)", "OID", int64(910001)))
		run(largeObjectBytes(910001, ""))
		run(fixture.Select("SELECT oid,lomowner=(SELECT oid FROM pg_roles WHERE rolname=current_user) AS owned,lomacl FROM pg_largeobject_metadata WHERE oid=910001::oid", []string{"oid", "owned", "lomacl"}, []string{"OID", "BOOL", "_ACLITEM"}, [][]any{{int64(910001), true, nil}}))
		seen := map[uint32]bool{910001: true, 910002: true}
		for _, expression := range []string{"lo_create(0::oid)", "lo_creat(0)", "lo_creat(-1)"} {
			q := "SELECT " + expression
			var oid uint32
			if prepared {
				s, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				err = s.QueryRowContext(p.Ctx).Scan(&oid)
				if closeErr := s.Close(); closeErr != nil {
					t.Error(closeErr)
				}
				if err != nil {
					t.Fatal(err)
				}
			} else if err := c.QueryRowContext(p.Ctx, q).Scan(&oid); err != nil {
				t.Fatal(err)
			}
			if oid == 0 || seen[oid] {
				t.Fatalf("automatic OID %d is zero or reused", oid)
			}
			seen[oid] = true
			defer largeObjectCleanup(t, p, c, oid)
			run(largeObjectBytes(oid, ""))
		}
		run(largeObjectValue("lo_from_bytea(910002::oid,decode('00017f80ff','hex'))", "OID", int64(910002)))
		run(largeObjectBytes(910002, "00017f80ff"))
		for _, expression := range []string{"lo_create(910001::oid)", "lo_from_bytea(910001::oid,decode('ff','hex'))"} {
			s := fixture.ImplicitError("SELECT "+expression, fixture.Diagnostic{
				Code: "23505", Message: `duplicate key value violates unique constraint "pg_largeobject_metadata_oid_index"`,
				Detail: "Key (oid)=(910001) already exists.", Schema: "pg_catalog", Table: "pg_largeobject_metadata", Constraint: "pg_largeobject_metadata_oid_index",
			})
			s.SelectRows = true
			s.ErrorStage = "execute"
			run(s)
			run(largeObjectBytes(910001, ""))
		}
	})
}

func TestReferenceLargeObjectBytes(t *testing.T) {
	largeObjectScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer largeObjectCleanup(t, p, c, 910011, 910012)
		run(largeObjectValue("lo_from_bytea(910011::oid,decode('6162636465','hex'))", "OID", int64(910011)))
		run(largeObjectPut(910011, 1, "00ff"))
		run(largeObjectBytes(910011, "6100ff6465"))
		run(largeObjectPut(910011, 8, "7a"))
		run(largeObjectBytes(910011, "6100ff64650000007a"))
		for _, tc := range []struct {
			offset int64
			length int
			hex    string
		}{{4, 20, "650000007a"}, {9, 2, ""}, {20, 2, ""}, {0, 0, ""}} {
			run(largeObjectValue(fmt.Sprintf("encode(lo_get(910011::oid,%d::bigint,%d),'hex')", tc.offset, tc.length), "TEXT", tc.hex))
		}
		run(largeObjectPut(910011, 9000, ""))
		run(largeObjectBytes(910011, "6100ff64650000007a"))
		run(largeObjectPut(910011, 2047, "0080ff"))
		run(largeObjectValue("encode(lo_get(910011::oid,2044::bigint,10),'hex')", "TEXT", "0000000080ff"))
		payload := []byte{0, 1, 127, 128, 255}
		s := largeObjectValue("lo_from_bytea($1::oid,$2::bytea)", "OID", int64(910012), int64(910012), payload)
		run(s)
		payload[1] = 99
		for range 2 {
			s := largeObjectValue("lo_get(910012::oid)", "BYTEA", string([]byte{0, 1, 127, 128, 255}))
			s.CheckScan = func(t *testing.T, rows *sql.Rows, index int) {
				var raw []byte
				if err := rows.Scan(&raw); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(raw, []byte{0, 1, 127, 128, 255}) {
					t.Fatalf("raw bytes %x", raw)
				}
			}
			run(s)
		}
	})
}

func TestReferenceLargeObjectSparseLimits(t *testing.T) {
	largeObjectScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		const maximum int64 = 4398046509056 // REL_18_6 MAX_LARGE_OBJECT_SIZE, BLCKSZ/4=2048.
		defer largeObjectCleanup(t, p, c, 910021, 910022)
		run(largeObjectValue("lo_create(910021::oid)", "OID", int64(910021)))
		run(largeObjectPut(910021, 4294967296, "666f6f"))
		run(largeObjectValue("encode(lo_get(910021::oid,4294967280::bigint,19),'hex')", "TEXT", strings.Repeat("00", 16)+"666f6f"))
		run(largeObjectError("SELECT lo_get(910021::oid)", "54000", "large object read request is too large"))
		run(largeObjectError("SELECT lo_get(910021::oid,-1::bigint,2147483647)", "54000", "large object read request is too large"))
		run(largeObjectValue("lo_create(910022::oid)", "OID", int64(910022)))
		run(largeObjectPut(910022, maximum, ""))
		run(largeObjectBytes(910022, ""))
		for _, offset := range []int64{-1, maximum + 1} {
			message := fmt.Sprintf("invalid large object seek target: %d", offset)
			run(largeObjectError(fmt.Sprintf("SELECT lo_get(910022::oid,%d::bigint,0)", offset), "22023", message))
			run(largeObjectError(fmt.Sprintf("SELECT lo_put(910022::oid,%d::bigint,decode('','hex'))", offset), "22023", message))
		}
		run(largeObjectPut(910022, maximum-1, "7a"))
		run(largeObjectValue(fmt.Sprintf("encode(lo_get(910022::oid,%d::bigint,2),'hex')", maximum-2), "TEXT", "007a"))
		run(largeObjectError(fmt.Sprintf("SELECT lo_put(910022::oid,%d::bigint,decode('7a','hex'))", maximum), "22023", "invalid large object write request size: 1"))
		run(largeObjectValue(fmt.Sprintf("encode(lo_get(910022::oid,%d::bigint,2),'hex')", maximum-2), "TEXT", "007a"))
	})
}
