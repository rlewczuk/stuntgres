//go:build !noservertests

package reference_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func serverCompletions(frames []serverFrame) []string {
	var out []string
	for _, f := range frames {
		if f.tag == 'C' {
			out = append(out, strings.TrimSuffix(string(f.body), "\x00"))
		}
	}
	return out
}
func serverWantCompletions(t *testing.T, frames []serverFrame, want ...string) {
	t.Helper()
	if got := serverCompletions(frames); !reflect.DeepEqual(got, want) {
		t.Fatalf("command tags %v want %v; %s", got, want, serverFrameSummary(frames))
	}
}
func serverWantReady(t *testing.T, frames []serverFrame, want byte) {
	t.Helper()
	if len(frames) == 0 || frames[len(frames)-1].tag != 'Z' || !bytes.Equal(frames[len(frames)-1].body, []byte{want}) {
		t.Fatalf("ReadyForQuery want %c: %v", want, frames)
	}
}
func serverWantValues(t *testing.T, frames []serverFrame, want [][]string) {
	t.Helper()
	if got := serverValues(t, frames); !reflect.DeepEqual(got, want) {
		t.Fatalf("wire values %v want %v", got, want)
	}
}

type serverColumn struct {
	name      string
	table     uint32
	attribute int16
	oid       uint32
	length    int16
	typmod    int32
	format    int16
}

func serverDescriptions(t *testing.T, frames []serverFrame) [][]serverColumn {
	t.Helper()
	var out [][]serverColumn
	for _, f := range frames {
		if f.tag != 'T' {
			continue
		}
		b := f.body
		if len(b) < 2 {
			t.Fatal("short RowDescription")
		}
		n := int(binary.BigEndian.Uint16(b))
		b = b[2:]
		cols := make([]serverColumn, n)
		for i := range cols {
			end := bytes.IndexByte(b, 0)
			if end < 0 || len(b) < end+19 {
				t.Fatal("malformed RowDescription")
			}
			c := &cols[i]
			c.name = string(b[:end])
			b = b[end+1:]
			c.table = binary.BigEndian.Uint32(b)
			c.attribute = int16(binary.BigEndian.Uint16(b[4:]))
			c.oid = binary.BigEndian.Uint32(b[6:])
			c.length = int16(binary.BigEndian.Uint16(b[10:]))
			c.typmod = int32(binary.BigEndian.Uint32(b[12:]))
			c.format = int16(binary.BigEndian.Uint16(b[16:]))
			b = b[18:]
		}
		if len(b) != 0 {
			t.Fatal("RowDescription trailing bytes")
		}
		out = append(out, cols)
	}
	return out
}

func TestReferenceServerQueries(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			ctx := context.WithValue(f.Ctx, fixture.RecorderKey{}, b.Recorder)
			for _, step := range []fixture.Step{
				{Query: "CREATE TABLE server_values (id integer, value text, unicode text)", Affected: 0},
				{Query: "INSERT INTO server_values VALUES (1, NULL, 'héλ'), (2, '', '東京')", Affected: 2},
				{Query: "SELECT id, value FROM server_values ORDER BY id", SelectRows: true, Columns: []string{"id", "value"}, Types: []string{"INT4", "TEXT"}, Rows: [][]any{{int64(1), nil}, {int64(2), ""}}},
				{Query: "SELECT unicode FROM server_values ORDER BY id", SelectRows: true, Columns: []string{"unicode"}, Types: []string{"TEXT"}, Rows: [][]any{{"héλ"}, {"東京"}}},
				{Query: "UPDATE server_values SET value = 'héλ' WHERE id = 2", Affected: 1},
				{Query: "DELETE FROM server_values WHERE id = 1", Affected: 1},
				{Query: "SELECT id, value FROM server_values ORDER BY id", SelectRows: true, Columns: []string{"id", "value"}, Types: []string{"INT4", "TEXT"}, Rows: [][]any{{int64(2), "héλ"}}},
			} {
				fixture.RunStep(t, ctx, b.DB, b.Recorder, step)
			}
			w := serverDial(t, f.DSNFor(b))
			serverWantCode(t, w.login(t, f.DSNFor(b), 196608), "")
			frames := w.query(t, "SELECT 7::int2 AS small, 42::int4 AS normal, 99::int8 AS big, true AS flag, DATE '2024-02-29' AS day, 'abc'::varchar(7) AS label WHERE false")
			serverWantCode(t, frames, "")
			serverWantCompletions(t, frames, "SELECT 0")
			serverWantReady(t, frames, 'I')
			want := []serverColumn{{"small", 0, 0, 21, 2, -1, 0}, {"normal", 0, 0, 23, 4, -1, 0}, {"big", 0, 0, 20, 8, -1, 0}, {"flag", 0, 0, 16, 1, -1, 0}, {"day", 0, 0, 1082, 4, -1, 0}, {"label", 0, 0, 1043, -1, 11, 0}}
			if got := serverDescriptions(t, frames); !reflect.DeepEqual(got, [][]serverColumn{want}) {
				t.Fatalf("empty metadata %v want %v", got, want)
			}
			frames = w.query(t, "SELECT 12.340::numeric(8,3) AS n, decode('00ff','hex') AS bytes, true AS yes, DATE '2024-02-29' AS day, ARRAY[1,NULL,3]::int4[] AS a, 'server_values'::regclass AS relation, 'integer'::regtype AS datatype")
			serverWantCode(t, frames, "")
			serverWantValues(t, frames, [][]string{{"12.340", "\\x00ff", "t", "2024-02-29", "{1,NULL,3}", "server_values", "integer"}})
			serverWantCompletions(t, frames, "SELECT 1")
			typed := serverDescriptions(t, frames)
			typedWant := []serverColumn{{"n", 0, 0, 1700, -1, 524295, 0}, {"bytes", 0, 0, 17, -1, -1, 0}, {"yes", 0, 0, 16, 1, -1, 0}, {"day", 0, 0, 1082, 4, -1, 0}, {"a", 0, 0, 1007, -1, -1, 0}, {"relation", 0, 0, 2205, 4, -1, 0}, {"datatype", 0, 0, 2206, 4, -1, 0}}
			if !reflect.DeepEqual(typed, [][]serverColumn{typedWant}) {
				t.Fatalf("typed metadata %v want %v", typed, typedWant)
			}
			frames = w.query(t, "SELECT id, value FROM server_values WHERE false")
			desc := serverDescriptions(t, frames)
			if len(desc) != 1 || len(desc[0]) != 2 || desc[0][0].table == 0 || desc[0][0].table != desc[0][1].table || desc[0][0].attribute != 1 || desc[0][1].attribute != 2 || desc[0][0].oid != 23 || desc[0][0].length != 4 || desc[0][1].oid != 25 || desc[0][1].length != -1 {
				t.Fatalf("origin metadata %v", desc)
			}
			serverScalar(t, w, "SELECT 'server_values'::regclass::oid::text", strconv.FormatUint(uint64(desc[0][0].table), 10))
			frames = w.query(t, "SELECT 1 AS first; CREATE TABLE server_middle (id int); INSERT INTO server_middle VALUES (5), (6); SELECT 'last'::text AS second")
			serverWantCode(t, frames, "")
			serverWantCompletions(t, frames, "SELECT 1", "CREATE TABLE", "INSERT 0 2", "SELECT 1")
			serverWantValues(t, frames, [][]string{{"1"}, {"last"}})
			if got := serverTags(frames); got != "TDCCCTDCZ" {
				t.Fatalf("ordered batch frames %q", got)
			}
			schemas := serverDescriptions(t, frames)
			if len(schemas) != 2 || schemas[0][0].name != "first" || schemas[1][0].name != "second" {
				t.Fatalf("batch schemas %v", schemas)
			}
			frames = w.query(t, "PREPARE server_sql(int) AS SELECT $1 + 1 AS answer; EXECUTE server_sql(41); DEALLOCATE server_sql")
			serverWantCode(t, frames, "")
			serverWantCompletions(t, frames, "PREPARE", "SELECT 1", "DEALLOCATE")
			serverWantValues(t, frames, [][]string{{"42"}})
			for _, tc := range []struct{ sql, tag string }{
				{"CREATE TABLE server_ctas AS SELECT 1 AS id WITH NO DATA", "CREATE TABLE AS"},
				{"CREATE TABLE server_ctas_data AS SELECT 1 AS id", "SELECT 1"},
				{"CREATE TABLE IF NOT EXISTS server_ctas_data AS SELECT 9 AS id", "CREATE TABLE AS"},
				{"CREATE MATERIALIZED VIEW server_mv AS SELECT 1 AS id WITH NO DATA", "CREATE MATERIALIZED VIEW"},
				{"CREATE MATERIALIZED VIEW server_mv_data AS SELECT 1 AS id", "SELECT 1"},
				{"CREATE MATERIALIZED VIEW IF NOT EXISTS server_mv_data AS SELECT 9 AS id", "CREATE MATERIALIZED VIEW"},
				{"SET application_name = 'tag-witness'", "SET"}, {"SHOW application_name", "SHOW"},
				{"CLOSE ALL", "CLOSE CURSOR ALL"}, {"DISCARD PLANS", "DISCARD PLANS"}, {"DISCARD SEQUENCES", "DISCARD SEQUENCES"}, {"DISCARD TEMP", "DISCARD TEMP"}, {"DISCARD ALL", "DISCARD ALL"},
			} {
				frames = w.query(t, tc.sql)
				serverWantCode(t, frames, "")
				serverWantCompletions(t, frames, tc.tag)
				serverWantReady(t, frames, 'I')
			}
			// Independent metadata values from PostgreSQL pg_type, not native display lengths.
			frames = w.query(t, "SELECT NULL::name AS name, NULL::uuid AS uuid, NULL::timetz AS timetz, NULL::interval AS interval, NULL::float4 AS real, NULL::float8 AS double, NULL::int4[] AS array, NULL::regclass AS regclass, NULL::regtype AS regtype")
			serverWantCode(t, frames, "")
			lengths := []int16{64, 16, 12, 16, 4, 8, -1, 4, 4}
			oids := []uint32{19, 2950, 1266, 1186, 700, 701, 1007, 2205, 2206}
			cols := serverDescriptions(t, frames)
			if len(cols) != 1 || len(cols[0]) != len(lengths) {
				t.Fatalf("type metadata %v", cols)
			}
			for i, c := range cols[0] {
				if c.oid != oids[i] || c.length != lengths[i] || c.format != 0 {
					t.Fatalf("type column %d: %+v want oid=%d length=%d", i, c, oids[i], lengths[i])
				}
			}
		})
	}
}

func TestReferenceServerTransactions(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			w := serverDial(t, f.DSNFor(b))
			serverWantCode(t, w.login(t, f.DSNFor(b), 196608), "")
			frames := w.query(t, "CREATE TABLE server_atomic (id int)")
			serverWantCode(t, frames, "")
			frames = w.query(t, "INSERT INTO server_atomic VALUES (1); SELECT 1/0; INSERT INTO server_atomic VALUES (2)")
			serverWantCode(t, frames, "22012")
			serverWantCompletions(t, frames, "INSERT 0 1")
			serverWantReady(t, frames, 'I')
			serverScalar(t, w, "SELECT count(*) FROM server_atomic", "0")
			frames = w.query(t, "BEGIN; INSERT INTO server_atomic VALUES (3)")
			serverWantCode(t, frames, "")
			serverWantReady(t, frames, 'T')
			frames = w.query(t, "COMMIT; SELECT FROM WHERE")
			serverWantCode(t, frames, "42601")
			if len(serverCompletions(frames)) != 0 {
				t.Fatalf("parse failure executed prefix: %v", frames)
			}
			serverWantReady(t, frames, 'E')
			frames = w.query(t, "SELECT 1")
			serverWantCode(t, frames, "25P02")
			serverWantReady(t, frames, 'E')
			frames = w.query(t, "COMMIT")
			serverWantCode(t, frames, "")
			serverWantCompletions(t, frames, "ROLLBACK")
			serverWantReady(t, frames, 'I')
			serverScalar(t, w, "SELECT count(*) FROM server_atomic", "0")
			frames = w.query(t, "BEGIN")
			serverWantReady(t, frames, 'T')
			frames = w.query(t, "SELECT 1/0")
			serverWantCode(t, frames, "22012")
			serverWantReady(t, frames, 'E')
			frames = w.query(t, "SELECT 42")
			serverWantCode(t, frames, "25P02")
			serverWantReady(t, frames, 'E')
			frames = w.query(t, "COMMIT")
			serverWantCompletions(t, frames, "ROLLBACK")
			serverWantReady(t, frames, 'I')
			frames = w.query(t, "BEGIN; BEGIN; SELECT 9; COMMIT")
			serverWantCode(t, frames, "")
			serverWantCompletions(t, frames, "BEGIN", "BEGIN", "SELECT 1", "COMMIT")
			// NOTICE is attached to the second BEGIN, before its completion and the SELECT.
			if serverTags(frames) != "CNCTDCCZ" {
				t.Fatalf("notice ordering %q", serverTags(frames))
			}
			for _, fr := range frames {
				if fr.tag == 'N' {
					fields := serverFields(fr.body)
					if fields['C'] != "25001" || fields['M'] != "there is already a transaction in progress" {
						t.Fatalf("BEGIN notice %v", fields)
					}
				}
			}
			frames = w.query(t, "SET application_name='outside'; SET DateStyle='ISO, MDY'; SET standard_conforming_strings=on")
			serverWantCode(t, frames, "")
			serverWantStatus(t, frames, "application_name", "outside")
			frames = w.query(t, "BEGIN; SET application_name='inside'; SET DateStyle='SQL, DMY'; SET standard_conforming_strings=off")
			serverWantCode(t, frames, "")
			serverWantReady(t, frames, 'T')
			serverWantStatus(t, frames, "application_name", "inside")
			serverWantStatus(t, frames, "DateStyle", "SQL, DMY")
			serverWantStatus(t, frames, "standard_conforming_strings", "off")
			serverScalar(t, w, "SELECT DATE '2024-02-29'", "29/02/2024")
			serverScalar(t, w, `SELECT 'a\nb'`, "a\nb")
			frames = w.query(t, "ROLLBACK")
			serverWantCode(t, frames, "")
			serverWantReady(t, frames, 'I')
			serverWantStatus(t, frames, "application_name", "outside")
			serverWantStatus(t, frames, "DateStyle", "ISO, MDY")
			serverWantStatus(t, frames, "standard_conforming_strings", "on")
			serverScalar(t, w, "SELECT DATE '2024-02-29'", "2024-02-29")
			serverScalar(t, w, `SELECT 'a\nb'`, `a\nb`)
			frames = w.query(t, "RESET application_name")
			serverWantCode(t, frames, "")
			serverWantStatus(t, frames, "application_name", "")
			// A recovered wire connection is also independently visible through pq.
			var count int
			if err := b.DB.QueryRowContext(f.Ctx, "SELECT count(*) FROM server_atomic").Scan(&count); err != nil || count != 0 {
				t.Fatalf("pq rollback count %d: %v", count, err)
			}
		})
	}
}
func serverWantStatus(t *testing.T, frames []serverFrame, name, want string) {
	t.Helper()
	found := false
	for _, fr := range frames {
		if fr.tag == 'S' {
			parts := bytes.Split(fr.body, []byte{0})
			if len(parts) >= 2 && string(parts[0]) == name {
				found = true
				if string(parts[1]) != want {
					t.Fatalf("ParameterStatus %s=%q want %q", name, parts[1], want)
				}
			}
		}
	}
	if !found {
		t.Fatalf("missing ParameterStatus %s=%q (%s)", name, want, serverTags(frames))
	}
}
