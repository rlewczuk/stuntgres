package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Independent witnesses: PostgreSQL REL_18_0 crypt-blowfish.out and
// https://github.com/openwall/crypt_blowfish/blob/master/wrapper.c tests table.
const pgcryptoPasswordSalt = "$2a$06$RQiOJ.3ELirrXwxIZY8q0O"
const pgcryptoPasswordEmpty = "$2a$06$RQiOJ.3ELirrXwxIZY8q0OlGbBEpDmx7IRZlNYvGJ1SHXwNi2cEKK"
const pgcryptoPasswordFoox = "$2a$06$RQiOJ.3ELirrXwxIZY8q0OR3CVJrAfda1z26CCHPnB6mmVZD8p0/C"

func pgcryptoPasswordText(t *testing.T, p *fixture.Pair, c fixture.Executor, r *fixture.Recorder, query string, want any, args ...any) {
	t.Helper()
	s := fixture.Select(query, []string{"value"}, []string{"TEXT"}, [][]any{{want}}, args...)
	fixture.RunStep(t, p.Ctx, c, r, s)
	fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(s))
}

func TestReferencePgcryptoPasswordVectors(t *testing.T) {
	pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		q := "SELECT crypt($1,$2) AS value"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		vectors := []struct{ password, salt, hash string }{
			{"", pgcryptoPasswordSalt, pgcryptoPasswordEmpty},
			{"foox", pgcryptoPasswordSalt, pgcryptoPasswordFoox},
			{"U*U", "$2a$05$CCCCCCCCCCCCCCCCCCCCC.", "$2a$05$CCCCCCCCCCCCCCCCCCCCC.E5YPO9kmyuRGyh0XouQYb4YMJKvyOeW"},
			{"U*U*", "$2a$05$CCCCCCCCCCCCCCCCCCCCC.", "$2a$05$CCCCCCCCCCCCCCCCCCCCC.VGOzA784oUp/Z0DY336zx7pLYAy0lwK"},
			{"U*U*U", "$2a$05$XXXXXXXXXXXXXXXXXXXXXO", "$2a$05$XXXXXXXXXXXXXXXXXXXXXOAcXxm9kjPGEMsLznoKqmqw7tc8WCx4a"},
			{"0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789", "$2a$05$abcdefghijklmnopqrstuu", "$2a$05$abcdefghijklmnopqrstuu5s2v8.iXieOjg/.AySBTTZIIVFJeBui"},
			{"0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789chars after 72 are ignored", "$2a$05$abcdefghijklmnopqrstuu", "$2a$05$abcdefghijklmnopqrstuu5s2v8.iXieOjg/.AySBTTZIIVFJeBui"},
			{"ё", "$2x$05$6bNw2HLQYeqHYyBfLMsv/O", "$2x$05$6bNw2HLQYeqHYyBfLMsv/OiwqTymGIGzFsA4hOTWebfehXHNprcAS"},
			// Pinned PostgreSQL 18.0 oracle observations (UTF8/C).
			{"ё", "$2a$05$6bNw2HLQYeqHYyBfLMsv/O", "$2a$05$6bNw2HLQYeqHYyBfLMsv/OUcZd0LKP39b87nBw3.S2tVZSqiQX6eu"},
			{strings.Repeat("a", 71), "$2a$05$6bNw2HLQYeqHYyBfLMsv/O", "$2a$05$6bNw2HLQYeqHYyBfLMsv/OG9hFigh6pPaJz1RpKoWaDOdj/xLzsw."},
			{strings.Repeat("a", 72), "$2a$05$6bNw2HLQYeqHYyBfLMsv/O", "$2a$05$6bNw2HLQYeqHYyBfLMsv/OGZ54Ep3QxFFOkY9vPONoxB/E/5Sh8qu"},
			{strings.Repeat("a", 71) + "é", "$2a$05$6bNw2HLQYeqHYyBfLMsv/O", "$2a$05$6bNw2HLQYeqHYyBfLMsv/OfBEsYqGKj/1aYz64jky1rImqm9uSq7q"},
			{strings.Repeat("a", 71) + "ê", "$2a$05$6bNw2HLQYeqHYyBfLMsv/O", "$2a$05$6bNw2HLQYeqHYyBfLMsv/OfBEsYqGKj/1aYz64jky1rImqm9uSq7q"},
		}
		for _, v := range vectors {
			pgcryptoPasswordText(t, p, c, r, q, v.hash, v.password, v.salt)
			fixture.RunStep(t, p.Ctx, held, r, fixture.Select(q, []string{"value"}, []string{"TEXT"}, [][]any{{v.hash}}, v.password, v.salt))
		}
		for _, salt := range []string{pgcryptoPasswordSalt, pgcryptoPasswordFoox, pgcryptoPasswordSalt + "arbitrary suffix", pgcryptoPasswordSalt[:28] + "P"} {
			pgcryptoPasswordText(t, p, c, r, q, pgcryptoPasswordFoox, "foox", salt)
		}
		pgcryptoPasswordText(t, p, c, r, q, strings.Replace(pgcryptoPasswordFoox, "$2a$", "$2x$", 1), "foox", strings.Replace(pgcryptoPasswordSalt, "$2a$", "$2x$", 1))
		for _, args := range [][]any{{nil, pgcryptoPasswordSalt}, {"foox", nil}, {nil, nil}, {nil, "invalid"}} {
			fixture.RunStep(t, p.Ctx, held, r, fixture.Select(q, []string{"value"}, []string{"TEXT"}, [][]any{{nil}}, args...))
		}
		pgcryptoPasswordText(t, p, c, r, q, pgcryptoPasswordFoox, "foox", pgcryptoPasswordSalt)
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT crypt(NULL::text,$1) AS value WHERE false", []string{"value"}, []string{"TEXT"}, [][]any{}, pgcryptoPasswordSalt))
		// Byte 72 changes matter, byte 73 changes do not, including a split UTF8
		// sequence: é and ê share their first byte, which is byte 72 here.
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT crypt($1,$5)<>crypt($2,$5) AS shorter,crypt($2,$5)=crypt($3,$5) AS truncated,crypt($6,$5)=crypt($7,$5) AS octets,crypt('wrong',$4)<>$4 AS wrong", []string{"shorter", "truncated", "octets", "wrong"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}, strings.Repeat("a", 71), strings.Repeat("a", 72), strings.Repeat("a", 72)+"z", pgcryptoPasswordFoox, "$2a$04$......................", strings.Repeat("a", 71)+"é", strings.Repeat("a", 71)+"ê"))
	})
}
