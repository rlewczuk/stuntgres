package reference_test

import (
	"context"
	"database/sql"
	"testing"
)

func TestReferenceStringEncodingCancellation(t *testing.T) {
	stringEncodingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE se_cancel(a int,c text,t text,u text,g bool)", 0))
		readSQL := `SELECT ascii('é') AS a,chr(128578) AS c,to_ascii('é','LATIN1') AS t,unistr($$\D83D\DE42$$) AS u,unicode_assigned(chr(129769)) AS g`
		writeSQL := `INSERT INTO se_cancel VALUES(ascii('é'),chr(128578),to_ascii('é','LATIN1'),unistr($$\D83D\DE42$$),unicode_assigned(chr(129769)))`
		read, err := b.db.PrepareContext(p.ctx, readSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer read.Close()
		write, err := b.db.PrepareContext(p.ctx, writeSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer write.Close()
		// This is the ordinary database/sql precanceled contract, not a claim about
		// PostgreSQL wire interruption during an executing provider scan.
		ctx, cancel := context.WithCancel(p.ctx)
		cancel()
		for _, query := range []func() (*sql.Rows, error){
			func() (*sql.Rows, error) { return b.db.QueryContext(ctx, readSQL) },
			func() (*sql.Rows, error) { return read.QueryContext(ctx) },
		} {
			rows, err := query()
			if rows != nil {
				rows.Close()
				t.Fatal("canceled query exposed rows")
			}
			lexicalOrdinary(t, err, context.Canceled)
		}
		for _, exec := range []func() (sql.Result, error){
			func() (sql.Result, error) { return b.db.ExecContext(ctx, writeSQL) },
			func() (sql.Result, error) { return write.ExecContext(ctx) },
		} {
			result, err := exec()
			if result != nil {
				t.Fatal("canceled write exposed result")
			}
			lexicalOrdinary(t, err, context.Canceled)
		}
		referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT a,c,t,u,g FROM se_cancel", []string{"a", "c", "t", "u", "g"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "BOOL"}, nil))
		want := referenceSelect(readSQL, []string{"a", "c", "t", "u", "g"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "BOOL"}, [][]any{{int64(233), "🙂", "AC", "🙂", true}})
		referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: c, statement: read, query: readSQL}, b.recorder, want)
		referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: c, statement: write, query: writeSQL}, b.recorder, referenceExec(writeSQL, 1))
		stringEncodingBoth(t, p, b, c, referenceSelect("SELECT a,c,t,u,g FROM se_cancel", want.columns, want.types, want.rows))
	})
}
