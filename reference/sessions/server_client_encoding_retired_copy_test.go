//go:build !noservertests

package reference_test

import (
	"bytes"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceClientEncodingRetiredCopyTo(t *testing.T) {
	t.Run("wire", func(t *testing.T) {
		f := fixture.NewServerFixture(t)
		for _, b := range f.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				dsn := f.DSNFor(b)
				pool := f.RawPool(t, b)
				w := clientEncodingLogin(t, dsn, "LATIN1")
				t.Run("outgoing", func(t *testing.T) {
					clientEncodingRetireSeed(t, f.Ctx, pool, "utf8_to_iso_8859_1", "ce_retired_copy_wire")
					for _, option := range []string{"", " (ENCODING 'LATIN1')"} {
						clientEncodingCopyBytes(t, w.query(t, `COPY (SELECT U&'\00E9') TO STDOUT`+option), []byte{0xe9, '\n'}, "COPY 1")
						serverScalar(t, w, "SHOW client_encoding", "LATIN1")
						serverScalar(t, w, "SELECT '\xe9'::text", "\xe9")
					}
					// Different file ID still resolves its own direction; the cached
					// LATIN1 outgoing callable must not retarget UTF8 output.
					clientEncodingCopyBytes(t, w.query(t, `COPY (SELECT U&'\00E9') TO STDOUT (ENCODING 'UTF8')`), []byte{0xc3, 0xa9, '\n'}, "COPY 1")
					serverScalar(t, w, "SELECT 42", "42")
				})
				clientEncodingCommand(t, w, "CREATE TABLE ce_retired_copy_in(v text)", "CREATE TABLE")
				restore := clientEncodingRetireSeed(t, f.Ctx, pool, "iso_8859_1_to_utf8", "ce_retired_copy_wire_in")
				peer := serverAuthenticated(t, dsn)
				for _, explicitTX := range []bool{false, true} {
					for _, option := range []string{"", " (ENCODING 'LATIN1')"} {
						ready := byte('I')
						if explicitTX {
							clientEncodingCommand(t, w, "BEGIN", "BEGIN")
							ready = 'E'
						}
						// Never send CopyData after pre-G rejection.
						frames := w.query(t, "COPY ce_retired_copy_in FROM STDIN"+option)
						serverWantTags(t, frames, "EZ")
						serverWantCompletions(t, frames)
						clientEncodingDiagnostic(t, frames, 'E', map[byte]string{'S': "ERROR", 'V': "ERROR", 'C': "42883", 'M': `default conversion function for encoding "LATIN1" to "UTF8" does not exist`})
						clientEncodingStatus(t, frames, "client_encoding")
						serverWantReady(t, frames, ready)
						serverScalar(t, peer, "SELECT count(*) FROM ce_retired_copy_in", "0")
						if explicitTX {
							aborted := w.query(t, "SELECT 42")
							serverWantTags(t, aborted, "EZ")
							clientEncodingDiagnostic(t, aborted, 'E', map[byte]string{'S': "ERROR", 'V': "ERROR", 'C': "25P02", 'M': "current transaction is aborted, commands ignored until end of transaction block"})
							serverWantReady(t, aborted, 'E')
							clientEncodingCommand(t, w, "ROLLBACK", "ROLLBACK")
						}
						serverScalar(t, w, "SELECT count(*) FROM ce_retired_copy_in", "0")
						serverScalar(t, w, "SHOW client_encoding", "LATIN1")
						serverScalar(t, w, "SELECT '\xe9'::text", "\xe9")
						serverScalar(t, w, "SELECT 42", "42")
					}
				}
				// Restore actual authority while the admitted owner stays open.
				restore()
				for _, option := range []string{"", " (ENCODING 'LATIN1')"} {
					clientEncodingCommand(t, w, "TRUNCATE ce_retired_copy_in", "TRUNCATE TABLE")
					serverCopyStart(t, w, "COPY ce_retired_copy_in FROM STDIN"+option, false, 0, 1)
					w.send(t, serverPacket('d', []byte{0xe9, '\n'}))
					frames := serverCopyFinish(t, w, false)
					serverWantTags(t, frames, "CZ")
					serverWantCompletions(t, frames, "COPY 1")
					serverWantReady(t, frames, 'I')
					clientEncodingStatus(t, frames, "client_encoding")
					serverScalar(t, peer, "SELECT count(*) FROM ce_retired_copy_in", "1")
					serverScalar(t, peer, "SELECT v FROM ce_retired_copy_in", "é")
					clientEncodingCopyBytes(t, w.query(t, "COPY ce_retired_copy_in TO STDOUT"+option), []byte{0xe9, '\n'}, "COPY 1")
					serverScalar(t, w, "SHOW client_encoding", "LATIN1")
					serverScalar(t, w, "SELECT 42", "42")
				}
			})
		}
	})
	t.Run("streams", func(t *testing.T) {
		p := fixture.NewScenario(t)
		for _, b := range p.Backends() {
			t.Run(b.Name, func(t *testing.T) {
				pool := p.Native
				if b.PG {
					pool = p.CopyPostgres
				}
				c, err := pool.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				if _, err := c.ExecContext(p.Ctx, "SET client_encoding='LATIN1'"); err != nil {
					t.Fatal(err)
				}
				authority := p.Native
				if b.PG {
					authority = p.Postgres
				}
				t.Run("outgoing", func(t *testing.T) {
					clientEncodingRetireSeed(t, p.Ctx, authority, "utf8_to_iso_8859_1", "ce_retired_copy_stream")
					for _, option := range []string{"", " (ENCODING 'LATIN1')", " (ENCODING 'UTF8')"} {
						var output bytes.Buffer
						result, err := clientEncodingStreamTo(p, c, b.PG, `COPY (SELECT U&'\00E9') TO STDOUT`+option, &output)
						want := []byte{0xe9, '\n'}
						if option == " (ENCODING 'UTF8')" {
							want = []byte{0xc3, 0xa9, '\n'}
						}
						if err != nil || result != (stuntgres.CopyResult{Count: 1, CommandTag: "COPY 1"}) || !bytes.Equal(output.Bytes(), want) {
							t.Fatalf("retired COPY option%q result%+v bytes%x want%x error%v", option, result, output.Bytes(), want, err)
						}
						var encoding string
						if err := c.QueryRowContext(p.Ctx, "SHOW client_encoding").Scan(&encoding); err != nil || encoding != "LATIN1" {
							t.Fatal("COPY changed session encoding", encoding, err)
						}
					}
				})
				if _, err := c.ExecContext(p.Ctx, "CREATE TABLE ce_retired_copy_stream_in(v text)"); err != nil {
					t.Fatal(err)
				}
				restore := clientEncodingRetireSeed(t, p.Ctx, authority, "iso_8859_1_to_utf8", "ce_retired_copy_stream_in")
				for _, explicitTX := range []bool{false, true} {
					for _, option := range []string{"", " (ENCODING 'LATIN1')"} {
						if explicitTX {
							if _, err := c.ExecContext(p.Ctx, "BEGIN"); err != nil {
								t.Fatal(err)
							}
						}
						input := bytes.NewReader([]byte("prefix\n\xe9\n"))
						result, err := fixture.CopyFrom(p.Ctx, c, b.PG, "COPY ce_retired_copy_stream_in FROM STDIN"+option, input)
						if result != (stuntgres.CopyResult{}) {
							t.Fatalf("rejected COPY FROM published result%+v", result)
						}
						want := fixture.ControlsReferenceDiagnostic("42883", `default conversion function for encoding "LATIN1" to "UTF8" does not exist`)
						fixture.AssertDiagnostic(t, err, &want)
						var count, healthy int
						var encoding string
						if err := authority.QueryRowContext(p.Ctx, "SELECT count(*) FROM ce_retired_copy_stream_in").Scan(&count); err != nil || count != 0 {
							t.Fatal("peer observed rejected COPY prefix", count, err)
						}
						if explicitTX {
							_, abortedErr := c.ExecContext(p.Ctx, "SELECT 42")
							aborted := fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")
							fixture.AssertDiagnostic(t, abortedErr, &aborted)
							if _, err := c.ExecContext(p.Ctx, "ROLLBACK"); err != nil {
								t.Fatal(err)
							}
						}
						if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM ce_retired_copy_stream_in").Scan(&count); err != nil || count != 0 {
							t.Fatal("rejected COPY FROM inserted prefix", count, err)
						}
						if err := c.QueryRowContext(p.Ctx, "SHOW client_encoding").Scan(&encoding); err != nil || encoding != "LATIN1" {
							t.Fatal("rejected COPY FROM changed encoding", encoding, err)
						}
						if err := c.QueryRowContext(p.Ctx, "SELECT 42").Scan(&healthy); err != nil || healthy != 42 {
							t.Fatal("COPY FROM recovery", healthy, err)
						}
					}
				}
				restore()
				for _, option := range []string{"", " (ENCODING 'LATIN1')"} {
					if _, err := c.ExecContext(p.Ctx, "TRUNCATE ce_retired_copy_stream_in"); err != nil {
						t.Fatal(err)
					}
					result, err := fixture.CopyFrom(p.Ctx, c, b.PG, "COPY ce_retired_copy_stream_in FROM STDIN"+option, bytes.NewReader([]byte{0xe9, '\n'}))
					if err != nil || result != (stuntgres.CopyResult{Count: 1, CommandTag: "COPY 1"}) {
						t.Fatalf("restored COPY FROM result%+v error%v", result, err)
					}
					var count, healthy int
					var stored, encoding string
					if err := authority.QueryRowContext(p.Ctx, "SELECT count(*) FROM ce_retired_copy_stream_in").Scan(&count); err != nil || count != 1 {
						t.Fatal("restored COPY row count", count, err)
					}
					if err := authority.QueryRowContext(p.Ctx, "SELECT v FROM ce_retired_copy_stream_in").Scan(&stored); err != nil || stored != "é" {
						t.Fatal("restored COPY Unicode storage", stored, err)
					}
					var output bytes.Buffer
					result, err = clientEncodingStreamTo(p, c, b.PG, "COPY ce_retired_copy_stream_in TO STDOUT"+option, &output)
					if err != nil || result != (stuntgres.CopyResult{Count: 1, CommandTag: "COPY 1"}) || !bytes.Equal(output.Bytes(), []byte{0xe9, '\n'}) {
						t.Fatalf("restored COPY TO result%+v bytes%x error%v", result, output.Bytes(), err)
					}
					if err := c.QueryRowContext(p.Ctx, "SHOW client_encoding").Scan(&encoding); err != nil || encoding != "LATIN1" {
						t.Fatal("restored COPY changed encoding", encoding, err)
					}
					if err := c.QueryRowContext(p.Ctx, "SELECT 42").Scan(&healthy); err != nil || healthy != 42 {
						t.Fatal("restored COPY recovery", healthy, err)
					}
				}
			})
		}
	})
}
