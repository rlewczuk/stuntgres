package fixture

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"testing"
)

func JSONPathNavigateRawCheck(t *testing.T, p *Pair, db *sql.DB, s Step) {
	t.Helper()
	if !s.SelectRows {
		result, err := db.ExecContext(p.Ctx, s.Query, s.Args...)
		if err != nil {
			t.Fatal(err)
		}
		affected, err := result.RowsAffected()
		if err != nil || affected != s.Affected {
			t.Fatalf("affected=%d want=%d err=%v", affected, s.Affected, err)
		}
		return
	}
	rows, err := db.QueryContext(p.Ctx, s.Query, s.Args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if s.CheckMetadata != nil {
		s.CheckMetadata(t, rows)
	}
	StringCoreRetainedRows(t, rows, nil, s.Columns, s.Types, s.Rows, false)
}

// One native-control phase with real emitted SQL replay, shared by the bounded
// smoke and full controls scenario. The PG backend itself has no control API.
func JSONPathNavigateReplay(t *testing.T, p *Pair, b Backend, c *sql.Conn, check Step, future ...Step) []byte {
	t.Helper()
	if b.PG {
		return nil
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	old, err := c.QueryContext(p.Ctx, check.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	meta, err := old.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	RunStep(t, p.Ctx, c, b.Recorder, Exec(`CREATE TABLE jpn_replacement(v int)`, 0))
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if check.CheckMetadata != nil {
		check.CheckMetadata(t, old)
	}
	StringCoreRetainedRows(t, old, nil, check.Columns, check.Types, check.Rows, false)
	if len(meta) != len(check.Types) {
		t.Fatal(meta)
	}
	for i, ct := range meta {
		if ct.DatabaseTypeName() != check.Types[i] {
			t.Fatal(ct)
		}
	}
	JSONPathNavigateBoth(t, p, b, c, check)
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
		t.Fatalf("nondeterministic redump\nbefore:\n%s\nafter:\n%s", dump.String(), redump.String())
	}
	connector, err := stuntgres.NewDriver().OpenConnector("reference/" + t.Name() + "/fresh")
	if err != nil {
		t.Fatal(err)
	}
	fresh := sql.OpenDB(connector)
	defer fresh.Close()
	if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	JSONPathNavigateRawCheck(t, p, fresh, check)
	for _, s := range future {
		JSONPathNavigateRawCheck(t, p, fresh, s)
	}
	// The check and replay both retain the actual recipe, not only materialized rows.
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("actual emitted SQL replay", err)
		}
		pgCheck := check
		if check.CheckMetadata != nil {
			pgCheck = JSONPathNavigateViewMetadataStep(check, true)
		}
		RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, pgCheck)
		for _, s := range future {
			RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
			t.Fatal(err)
		}
	}
	return bytes.Clone(dump.Bytes())
}
