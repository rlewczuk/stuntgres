package reference_test

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// PostgreSQL independently owns the public witnesses. Native-only phases below
// exercise host-authorized persistence, not a public SQL enforcement exception.
func TestReferenceDomainDefaultNullReplay(t *testing.T) {
	domainDefaultScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, q := range []string{
			"CREATE DOMAIN domain_s.replay_nn AS integer DEFAULT 7 NOT NULL CONSTRAINT replay_positive CHECK(VALUE>0)",
			"CREATE DOMAIN domain_s.replay_bag AS domain_s.replay_nn[] CHECK(cardinality(VALUE)=4)",
			"CREATE TYPE domain_s.replay_carrier AS(n domain_s.replay_nn,a domain_s.replay_nn[])",
			"CREATE DOMAIN domain_s.replay_box AS domain_s.replay_carrier",
			"CREATE TABLE domain_s.replay_rows(id integer,v domain_s.replay_nn,a domain_s.replay_bag,c domain_s.replay_carrier)",
			"CREATE TABLE domain_s.replay_outer(id integer,ca domain_s.replay_box[])",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec(`INSERT INTO domain_s.replay_rows(id,v,a[-2:-1][4:5],c.n,c.a[7:8])
VALUES(1,(SELECT v FROM domain_s.replay_rows WHERE false),
ARRAY[ARRAY[(SELECT a[-2][4] FROM domain_s.replay_rows WHERE false),11::domain_s.replay_nn],
ARRAY[12::domain_s.replay_nn,(SELECT a[-1][5] FROM domain_s.replay_rows WHERE false)]],
(SELECT (c).n FROM domain_s.replay_rows WHERE false),
ARRAY[(SELECT ((c).a)[7] FROM domain_s.replay_rows WHERE false),13::domain_s.replay_nn])`, 1))
		run(fixture.Exec("INSERT INTO domain_s.replay_outer(id,ca[-3:-3]) VALUES(1,ARRAY[(SELECT c FROM domain_s.replay_rows WHERE id=1)::domain_s.replay_box])", 1))
		run(fixture.Exec("CREATE MATERIALIZED VIEW domain_s.replay_snapshot AS SELECT id,v,a,c FROM domain_s.replay_rows", 1))
		check := func(t *testing.T, pair *fixture.Pair, e fixture.Executor, rec *fixture.Recorder) {
			t.Helper()
			for _, prepared := range []bool{false, true} {
				for _, table := range []string{"replay_rows", "replay_snapshot"} {
					step := fixture.Select("SELECT id,v,array_dims(a) AS dims,a[-2][4] AS a1,a[-2][5] AS a2,a[-1][4] AS a3,a[-1][5] AS a4,(c).n AS n,array_dims((c).a) AS inner_dims,((c).a)[7] AS c1,((c).a)[8] AS c2 FROM domain_s."+table+" WHERE id=1", []string{"id", "v", "dims", "a1", "a2", "a3", "a4", "n", "inner_dims", "c1", "c2"}, []string{"INT4", "INT4", "TEXT", "INT4", "INT4", "INT4", "INT4", "INT4", "TEXT", "INT4", "INT4"}, [][]any{{int64(1), nil, "[-2:-1][4:5]", nil, int64(11), int64(12), nil, nil, "[7:8]", nil, int64(13)}})
					step.Prepared = prepared
					fixture.RunStep(t, pair.Ctx, e, rec, step)
				}
				step := fixture.Select("SELECT id,array_dims(ca) AS dims,(ca[-3]).n AS n,array_dims((ca[-3]).a) AS inner_dims,((ca[-3]).a)[7] AS c1,((ca[-3]).a)[8] AS c2 FROM domain_s.replay_outer", []string{"id", "dims", "n", "inner_dims", "c1", "c2"}, []string{"INT4", "TEXT", "INT4", "TEXT", "INT4", "INT4"}, [][]any{{int64(1), "[-3:-3]", nil, "[7:8]", nil, int64(13)}})
				step.Prepared = prepared
				fixture.RunStep(t, pair.Ctx, e, rec, step)
				fixture.RunStep(t, pair.Ctx, e, rec, domainDefaultFailure("INSERT INTO domain_s.replay_rows(id,v) VALUES(9,NULL)", domainNotNullDiagnostic("domain_s", "replay_nn"), prepared, false))
				fixture.RunStep(t, pair.Ctx, e, rec, domainDefaultFailure("INSERT INTO domain_s.replay_outer VALUES(9,ARRAY[ROW(NULL,ARRAY[1::domain_s.replay_nn])::domain_s.replay_carrier::domain_s.replay_box])", domainNotNullDiagnostic("domain_s", "replay_nn"), prepared, false))
			}
			fixture.RunStep(t, pair.Ctx, e, rec, fixture.Select("SELECT id FROM domain_s.replay_rows WHERE id=9 UNION ALL SELECT id FROM domain_s.replay_outer WHERE id=9", []string{"id"}, []string{"INT4"}, nil))
		}
		check(t, p, c, r)
		// Explicit native-only control phase. PostgreSQL has no Reset/Restore API.
		if b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		check(t, p, c, r)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		exercise := func(pair *fixture.Pair, e fixture.Executor, rec *fixture.Recorder) {
			check(t, pair, e, rec)
			fixture.RunStep(t, pair.Ctx, e, rec, fixture.Prepared(fixture.Exec("INSERT INTO domain_s.replay_rows(id) VALUES(2)", 1)))
			fixture.RunStep(t, pair.Ctx, e, rec, fixture.Select("SELECT id,v,a IS NULL AS empty_a,c IS NULL AS empty_c FROM domain_s.replay_rows WHERE id=2", []string{"id", "v", "empty_a", "empty_c"}, []string{"INT4", "INT4", "BOOL", "BOOL"}, [][]any{{int64(2), int64(7), true, true}}))
			check(t, pair, e, rec)
		}
		exercise(p, c, r)
		target := fixture.NewFixture(t, false, true)
		if err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		exercise(target, target.Memory, target.MemoryRecord)
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE public.replay_sentinel(v integer)", 0))
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("INSERT INTO public.replay_sentinel VALUES(42)", 1))
		prefix := "-- stuntgres-domain-null-v1 "
		start := strings.Index(dump.String(), prefix)
		if start < 0 {
			t.Fatal("native persistence omitted captured domain NULL provenance")
		}
		payloadStart := start + len(prefix)
		end := payloadStart + strings.IndexByte(dump.String()[payloadStart:], '\n')
		raw, err := base64.StdEncoding.DecodeString(dump.String()[payloadStart:end])
		if err != nil {
			t.Fatal(err)
		}
		var record map[string]any
		if err = json.Unmarshal(raw, &record); err != nil {
			t.Fatal(err)
		}
		replacePayload := func(payload []byte) string {
			return dump.String()[:payloadStart] + base64.StdEncoding.EncodeToString(payload) + dump.String()[end:]
		}
		corrupt := map[string]string{
			"unknown_field":         replacePayload(append([]byte(`{"unknown":true,`), raw[1:]...)),
			"duplicate_field":       replacePayload(append([]byte(`{"schema":"domain_s",`), raw[1:]...)),
			"json_null":             replacePayload([]byte(`{"schema":"domain_s","table":"replay_rows","cells":null}`)),
			"unknown_version":       strings.Replace(dump.String(), prefix, "-- stuntgres-domain-null-v2 ", 1),
			"invalid_base64":        dump.String()[:payloadStart] + "!" + dump.String()[end:],
			"unpaired":              dump.String() + "\n" + dump.String()[start:end] + "\n",
			"intervening_statement": dump.String()[:end+1] + "SELECT 1;\n" + dump.String()[end+1:],
		}
		mutate := func(name string, change func(map[string]any) bool) {
			var copy map[string]any
			if err := json.Unmarshal(raw, &copy); err != nil {
				t.Fatal(err)
			}
			if !change(copy) {
				t.Fatalf("missing independently selected corruption target %s", name)
			}
			payload, err := json.Marshal(copy)
			if err != nil {
				t.Fatal(err)
			}
			corrupt[name] = replacePayload(payload)
		}
		mutate("wrong_table", func(m map[string]any) bool { m["table"] = "missing_replay_table"; return true })
		mutate("wrong_column", func(m map[string]any) bool {
			m["cells"].([]any)[0].(map[string]any)["column"] = "missing_column"
			return true
		})
		var walk func(any, func(map[string]any) bool) bool
		walk = func(value any, visit func(map[string]any) bool) bool {
			switch x := value.(type) {
			case map[string]any:
				if visit(x) {
					return true
				}
				for _, child := range x {
					if walk(child, visit) {
						return true
					}
				}
			case []any:
				for _, child := range x {
					if walk(child, visit) {
						return true
					}
				}
			}
			return false
		}
		mutate("wrong_domain", func(m map[string]any) bool {
			return walk(m, func(n map[string]any) bool {
				if n["kind"] != "domain_null" {
					return false
				}
				n["type"] = []any{"pg_catalog", "int4"}
				return true
			})
		})
		mutate("wrong_member", func(m map[string]any) bool {
			return walk(m, func(n map[string]any) bool {
				if _, ok := n["members"]; !ok {
					return false
				}
				n["members"].([]any)[0].(map[string]any)["name"] = "wrong_member"
				return true
			})
		})
		mutate("invalid_bounds", func(m map[string]any) bool {
			return walk(m, func(n map[string]any) bool {
				dims, ok := n["dimensions"].([]any)
				if !ok || len(dims) == 0 {
					return false
				}
				dims[0].(map[string]any)["lower"] = int64(2147483647)
				dims[0].(map[string]any)["length"] = int64(2)
				return true
			})
		})
		mutate("non_null_check_enforced", func(m map[string]any) bool {
			return walk(m, func(n map[string]any) bool {
				if n["kind"] != "scalar" {
					return false
				}
				n["scalar"] = "-1"
				return true
			})
		})
		mutate("ordinary_null_not_privileged", func(m map[string]any) bool {
			return walk(m, func(n map[string]any) bool {
				if n["kind"] != "domain_null" {
					return false
				}
				delete(n, "type")
				n["kind"] = "null"
				return true
			})
		})
		for name, script := range corrupt {
			t.Run(name, func(t *testing.T) {
				if err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(script)); err == nil {
					t.Fatal("invalid native provenance accepted")
				}
				fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT v FROM public.replay_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
				fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT id,v FROM domain_s.replay_rows WHERE id=2", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(7)}}))
				check(t, target, target.Memory, target.MemoryRecord)
			})
		}
		// The real marker is inert ordinary SQL. A normal caller still supplies fresh
		// NULL, even when the comment describes an otherwise valid captured value.
		fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, domainDefaultFailure(dump.String()[start:end]+"\nINSERT INTO domain_s.replay_rows(id,v) VALUES(9,NULL)", domainNotNullDiagnostic("domain_s", "replay_nn"), false, false))
		check(t, target, target.Memory, target.MemoryRecord)
	})
}
