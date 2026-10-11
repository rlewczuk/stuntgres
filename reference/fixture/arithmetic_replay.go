package fixture

import (
	"bytes"
	"stuntgres"
	"testing"
)

func ArithmeticRoundtrip(t *testing.T, p *Pair, tables ...string) {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	var again bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), again.Bytes()) {
		t.Fatal("arithmetic native redump changed")
	}
	if p.Postgres != nil {
		for _, table := range tables {
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP TABLE "+table); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("owner-bearing PostgreSQL replay", err)
		}
	}
}
