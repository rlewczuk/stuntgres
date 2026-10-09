package reference_test

import (
	"bytes"
	"testing"

	"stuntgres"
)

func referenceArithmeticRoundtrip(t *testing.T, p *referencePair, tables ...string) {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.ctx, p.native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	var again bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &again); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), again.Bytes()) {
		t.Fatal("arithmetic native redump changed")
	}
	if p.postgres != nil {
		for _, table := range tables {
			if _, err := p.postgres.ExecContext(p.ctx, "DROP TABLE "+table); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
			t.Fatal("owner-bearing PostgreSQL replay", err)
		}
	}
}
