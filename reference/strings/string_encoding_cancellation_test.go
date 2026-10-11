package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStringEncodingCancellation(t *testing.T) {
	fixture.StringEncodingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE se_cancel(a int,c text,t text,u text,g bool)", 0))
		readSQL := `SELECT ascii('é') AS a,chr(128578) AS c,to_ascii('é','LATIN1') AS t,unistr($$\D83D\DE42$$) AS u,unicode_assigned(chr(129769)) AS g`
		writeSQL := `INSERT INTO se_cancel VALUES(ascii('é'),chr(128578),to_ascii('é','LATIN1'),unistr($$\D83D\DE42$$),unicode_assigned(chr(129769)))`
		read, err := b.DB.PrepareContext(p.Ctx, readSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer read.Close()
		write, err := b.DB.PrepareContext(p.Ctx, writeSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer write.Close()
		// This is the ordinary database/sql precanceled contract, not a claim about
		// PostgreSQL wire interruption during an executing provider scan.
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		for _, query := range []func() (*sql.Rows, error){
			func() (*sql.Rows, error) { return b.DB.QueryContext(ctx, readSQL) },
			func() (*sql.Rows, error) { return read.QueryContext(ctx) },
		} {
			rows, err := query()
			if rows != nil {
				rows.Close()
				t.Fatal("canceled query exposed rows")
			}
			fixture.LexicalOrdinary(t, err, context.Canceled)
		}
		for _, exec := range []func() (sql.Result, error){
			func() (sql.Result, error) { return b.DB.ExecContext(ctx, writeSQL) },
			func() (sql.Result, error) { return write.ExecContext(ctx) },
		} {
			result, err := exec()
			if result != nil {
				t.Fatal("canceled write exposed result")
			}
			fixture.LexicalOrdinary(t, err, context.Canceled)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT a,c,t,u,g FROM se_cancel", []string{"a", "c", "t", "u", "g"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "BOOL"}, nil))
		want := fixture.Select(readSQL, []string{"a", "c", "t", "u", "g"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "BOOL"}, [][]any{{int64(233), "🙂", "AC", "🙂", true}})
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: read, SQL: readSQL}, b.Recorder, want)
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: write, SQL: writeSQL}, b.Recorder, fixture.Exec(writeSQL, 1))
		fixture.StringEncodingBoth(t, p, b, c, fixture.Select("SELECT a,c,t,u,g FROM se_cancel", want.Columns, want.Types, want.Rows))
	})
}
