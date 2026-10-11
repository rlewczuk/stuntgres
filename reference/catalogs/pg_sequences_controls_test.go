package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGSequencesControls(t *testing.T) {
	p := fixture.NewScenario(t)
	native := p.Backends()[0]
	for _, q := range []string{"CREATE SEQUENCE pgsv_control CACHE 10", "CREATE SEQUENCE pgsv_uncalled"} {
		pgSequenceExec(t, p, native, native.DB, q)
	}
	pgSequencesScalar(t, p, native, native.DB, "SELECT nextval('pgsv_control')", "nextval", 1)
	owner := pgSequencesOwner(t, p, native.DB)
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "pg_sequences") || !strings.Contains(dump.String(), "setval") {
		t.Fatal("dump must serialize sequence state, not provider", dump.String())
	}
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			if b.PG {
				if _, err := b.DB.ExecContext(p.Ctx, dump.String()); err != nil {
					t.Fatal("PG rejected actual native dump", err)
				}
			}
			a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
			row := pgSequencesDefault(owner, "pgsv_control", int64(10))
			row[9] = int64(10)
			pgSequencesCheck(t, p, b, a, "pgsv_control", row)
			pgSequencesCheck(t, p, b, a, "pgsv_uncalled", pgSequencesDefault(owner, "pgsv_uncalled", nil))
			if b.PG {
				return
			}
			pgSequenceExec(t, p, b, a, "CREATE TEMP SEQUENCE pgsv_control_temp")
			pgSequencesScalar(t, p, b, a, "SELECT setval('pgsv_control_temp',9,true)", "setval", 9)
			var withTemp bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &withTemp); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(withTemp.String(), "pgsv_control_temp") {
				t.Fatal("dump leaked TEMP", withTemp.String())
			}
			old, err := a.QueryContext(p.Ctx, pgSequencesProbe, "pgsv_control")
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			want := fixture.ControlsReferenceDiagnostic("22023", "CACHE (0) must be greater than zero")
			fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+"CREATE SEQUENCE pgsv_bad CACHE 0;")), &want)
			pgSequencesCheck(t, p, b, peer, "pgsv_control", row)
			pgSequencesCheck(t, p, b, peer, "pgsv_bad", nil)
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			if err := stuntgres.Restore(canceled, p.Native, strings.NewReader(dump.String())); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			pgSequencesCheck(t, p, b, peer, "pgsv_control", nil)
			pgSequencesScalar(t, p, b, a, "SELECT currval('pgsv_control_temp')", "currval", 9)
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
				t.Fatal(err)
			}
			pgSequencesCheck(t, p, b, peer, "pgsv_control", row)
			pgSequencesScalar(t, p, b, a, "SELECT currval('pgsv_control_temp')", "currval", 9)
			pgSequencesMetadata(t, old, false)
			if !old.Next() {
				t.Fatal("retained row", old.Err())
			}
			var schema, name, role, typ string
			var start, min, max, inc, cache, last int64
			var cycle bool
			if err := old.Scan(&schema, &name, &role, &typ, &start, &min, &max, &inc, &cycle, &cache, &last); err != nil {
				t.Fatal(err)
			}
			if schema != "public" || name != "pgsv_control" || role != owner || typ != "bigint" || start != 1 || min != 1 || max != 9223372036854775807 || inc != 1 || cycle || cache != 10 || last != 10 {
				t.Fatal("retained values", schema, name, role, typ, start, min, max, inc, cycle, cache, last)
			}
			if old.Next() || old.Err() != nil {
				t.Fatal("retained cardinality", old.Err())
			}
		})
	}
}
