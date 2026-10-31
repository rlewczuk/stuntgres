package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func romanianSmokeBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	stmt, err := c.PrepareContext(p.Ctx, s.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for range 2 {
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: s.Query}, b.Recorder, s)
	}
}

func romanianStockCleanup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) func() {
	t.Helper()
	if !b.PG {
		return func() {}
	}
	cleanupPair := *p
	restored := romanianDefinitionSnapshot(t, &cleanupPair, b, c, "romanian_stem", "romanian")
	var dictionaryOwner, configurationOwner string
	if err := c.QueryRowContext(p.Ctx, "SELECT dictowner::regrole::text FROM pg_ts_dict WHERE oid='romanian_stem'::regdictionary").Scan(&dictionaryOwner); err != nil {
		t.Fatal(err)
	}
	if err := c.QueryRowContext(p.Ctx, "SELECT cfgowner::regrole::text FROM pg_ts_config WHERE oid='romanian'::regconfig").Scan(&configurationOwner); err != nil {
		t.Fatal(err)
	}
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := c.ExecContext(ctx, "ALTER TEXT SEARCH DICTIONARY pg_catalog.romanian_stem (language=romanian,stopwords)"); err != nil {
			t.Error("restore stock Romanian options", err)
		}
		for _, item := range []struct{ kind, name, owner string }{
			{"DICTIONARY", "romanian_stem", dictionaryOwner},
			{"CONFIGURATION", "romanian", configurationOwner},
		} {
			if _, err := c.ExecContext(ctx, "ALTER TEXT SEARCH "+item.kind+" pg_catalog."+item.name+" OWNER TO \""+strings.ReplaceAll(item.owner, "\"", "\"\"")+"\""); err != nil {
				t.Error("restore actual stock owner", err)
			}
		}
		cleanupPair.Ctx = ctx
		restored()
	}
}

func romanianLexizeMetadata(t *testing.T, b fixture.Backend, rows *sql.Rows) {
	t.Helper()
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 {
		t.Fatal(types, err)
	}
	ct := types[0]
	scan := reflect.TypeFor[string]()
	if b.PG {
		scan = reflect.TypeFor[any]()
	}
	length, lk := ct.Length()
	precision, scale, dk := ct.DecimalSize()
	nullable, nk := ct.Nullable()
	if ct.DatabaseTypeName() != "_TEXT" || ct.ScanType() != scan || length != 0 || lk || precision != 0 || scale != 0 || dk || nullable || nk {
		t.Fatalf("lexize descriptors: %#v scan=%v length=%d/%t decimal=%d,%d/%t nullable=%t/%t", ct, ct.ScanType(), length, lk, precision, scale, dk, nullable, nk)
	}
}
