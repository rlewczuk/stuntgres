package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Literal known answers from REL_18_6 contrib/pgcrypto/expected/crypt-shacrypt.out
// and https://www.akkadia.org/drepper/SHA-crypt.txt, not native-generated values.
const pgcryptoSHA256Hello = "$5$saltstring$5B8vYYiY.CVt1RlTTf8KbXBH3hsxY/GNooZaBBGWEc5"
const pgcryptoSHA512Hello = "$6$saltstring$svn8UoSVapNtMuq1ukKS4tPQd8iKwSMHWjl/O817G3uBnIFNjnQJuesI68u4OTLiBFdcbYEdFCoEOfaS35inz1"

func TestReferencePgcryptoPasswordSHAVectors(t *testing.T) {
	pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		q := "SELECT crypt($1,$2) AS value"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		vectors := []struct{ password, setting, hash string }{
			{"Hello world!", "$5$saltstring", pgcryptoSHA256Hello},
			{"Hello world!", "$6$saltstring", pgcryptoSHA512Hello},
			{"Hello world!", "$5$rounds=10000$saltstringsaltstring", "$5$rounds=10000$saltstringsaltst$3xv.VbSHBb41AL9AvLeujZkZRBAwqFMz2.opqey6IcA"},
			{"Hello world!", "$6$rounds=10000$saltstringsaltstring", "$6$rounds=10000$saltstringsaltst$OW1/O6BYHV6BcXZu8QVeXbDWra3Oeqh0sbHbbMCVNSnCM/UrjmM0Dp8vOuZeHBy/YTBmSK6H9qs/y3RnOaw5v."},
			{"This is just a test", "$5$rounds=5000$toolongsaltstring", "$5$rounds=5000$toolongsaltstrin$Un/5jzAHMgOGZ5.mWJpuVolil07guHPvOW8mGRcvxa5"},
			{"This is just a test", "$6$rounds=5000$toolongsaltstring", "$6$rounds=5000$toolongsaltstrin$lQ8jolhgVRVhY4b5pZKaysCLi0QBxGoNeKQzQ3glMhwllF7oGDZxUhx1yxdYcz/e1JSbq3y6JMxxl8audkUEm0"},
			{"the minimum number is still observed", "$5$rounds=1000$roundstoolow", "$5$rounds=1000$roundstoolow$yfvwcWrQ8l/K0DAWyuPMDNHpIVlTQebY9l/gL972bIC"},
			{"the minimum number is still observed", "$6$rounds=1000$roundstoolow", "$6$rounds=1000$roundstoolow$kUMsbe306n21p9R.FRkW3IGn.S9NPN0x50YhH1xhLsPuWGsUSklZt58jaTfF4ZEQpyUNGc0dqbpBYYBaHHrsX."},
			{"", "$5$Szzz0yzz", "$5$Szzz0yzz$cA.ZFZKqblRYjdsbrWtVTYa/qSwPQnt2uh0LBtyYAAD"},
			{"", "$6$Szzz0yzz", "$6$Szzz0yzz$EGj.JLAovFyAtCJx3YD1DXD1yTXoO9gv4qgLyHBsJJ1lkpnLB8ZPHekm1qXjJCOBc/8thCuHpxNN8Y5xzRYU5."},
		}
		for i, v := range vectors {
			t.Run(fmt.Sprintf("vector_%d", i), func(t *testing.T) {
				pgcryptoPasswordText(t, p, c, r, q, v.hash, v.password, v.setting)
				fixture.RunStep(t, p.Ctx, held, r, fixture.Select(q, []string{"value"}, []string{"TEXT"}, [][]any{{v.hash}}, v.password, v.setting))
				pgcryptoPasswordText(t, p, c, r, q, v.hash, v.password, v.hash)
				s := fixture.Select("SELECT crypt($1,$2)=$2 AS correct,crypt('wrong',$2)=$2 AS wrong", []string{"correct", "wrong"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}}, v.password, v.hash)
				fixture.RunStep(t, p.Ctx, c, r, s)
				fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(s))
			})
		}
		for _, hash := range []string{pgcryptoSHA256Hello, pgcryptoSHA512Hello} {
			setting := hash[:3] + "rounds=5000$saltstring"
			want := hash[:3] + "rounds=5000$" + hash[3:]
			pgcryptoPasswordText(t, p, c, r, q, want, "Hello world!", setting)
		}
		for _, args := range [][]any{{nil, "$5$rounds=oops$!"}, {"p", nil}, {nil, nil}} {
			fixture.RunStep(t, p.Ctx, held, r, fixture.Select(q, []string{"value"}, []string{"TEXT"}, [][]any{{nil}}, args...))
		}
		fixture.RunStep(t, p.Ctx, held, r, fixture.Select(q, []string{"value"}, []string{"TEXT"}, [][]any{{pgcryptoSHA256Hello}}, "Hello world!", "$5$saltstring"))
	})
}

// Literal PostgreSQL18.6 UTF8/C observations from the root oracle checkpoint.
// Leading dollars affect hashing even when the displayed salt is identical.
func TestReferencePgcryptoPasswordSHAParserAndPasswordBoundaries(t *testing.T) {
	pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		q := "SELECT crypt($1,$2) AS value"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		check := func(t *testing.T, password, setting, hash string) {
			t.Helper()
			pgcryptoPasswordText(t, p, c, r, q, hash, password, setting)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, r, fixture.Select(q, []string{"value"}, []string{"TEXT"}, [][]any{{hash}}, password, setting))
		}
		for i, v := range []struct{ setting, hash string }{
			{"$5$", "$5$$mAwMsDaqjtxAtGqstEIf7OBR15rgcx.jSKGM94IKRj/"},
			{"$5$$salt", "$5$salt$E/ML6se8AJykqQpAjfpDBzcwOvbwtQf/ixq.pr8ytQ2"},
			{"$5$$$salt", "$5$salt$HJpm8yC0fBMxnw2h1BJ7UuxglsLOxiLUTBKUUTCZzQ5"},
			{"$5$$$$$salt", "$5$salt$OU4n5k2R42PnJeJQunmqiXVAoZpk54oWDlkLT7qkrI9"},
			{"$5$1234567890123456!", "$5$1234567890123456$Ow2AN9433LiMNzQC4VmE82MYxZPiMfUFbTqNXs9fRWC"},
			{"$5$123456789012345$salt", "$5$123456789012345$JlSfSEUi.C4pRxm1h4rC/SEIZ2Xg75tVWonDEAmdUs7"},
			{"$5$salt$ordinary", "$5$salt$WOnO0MHdXDEALf.twzZOIPNs3i4rCTA.NRrcK.sWAR8"},
			{"$6$", "$6$$.SKR9BCFmNlzTpsFbxLHKPVAMUdqxN8.85WISsmC.fRIPfZ78cePl/wQJcKzjcsDe8rRtdaVxJHS/E1LzWy3./"},
			{"$6$$salt", "$6$salt$xQMo6/dI.VKomSt.at84Sk96Pqwr6Ir9HQo/Gbu0zQHW4m/Ol8Rd5tXkVu8KK6PZo0YOcjq2mjdqw4sVVZNO11"},
			{"$6$$$salt", "$6$salt$mRXDTpSp7pvLR./7VYKLbBWgtS1/2rgEdQgv1SY4PYQPVSCUOGN.in8mvQ5CgZ9pjsprXeaOe1d9GA4ObHYmN/"},
			{"$6$$$$$salt", "$6$salt$qjofOZPaMeiChZCXd./osUQAe4m0/YZ0oOLHjjytbFcTkx2wzkKvKMpWOl8.t3RHRUSKoEG0WPJVYopWjeVgw."},
			{"$6$1234567890123456!", "$6$1234567890123456$RRqTRigvFNy2YNFhtweUvD34cSC93gOi9lVhViJFJB.dlOJLt938.htGGTdgo9ruSiBtrEJY.F/HqtyCLnS5W1"},
			{"$6$123456789012345$salt", "$6$123456789012345$eCL9kPrtLSZiTF77AuowGAoBOIm4zqrB9GWbzSYl3dH6gIY4Xx7JGowGnfpSIgPOEQMxd3/kD6Hm1YthDAc8g1"},
			{"$6$salt$ordinary", "$6$salt$F6mPVWtXQWa41OpigvMj06czJyMjPRUJ7/KTt0mPaEFiA1BVVgXkqZO9lAnZwubnctt3dL6saXIFdviUxvcS4."},
		} {
			t.Run(fmt.Sprintf("salt_%d", i), func(t *testing.T) { check(t, "Hello world!", v.setting, v.hash) })
		}
		for _, id := range []string{"5", "6"} {
			hash := "$5$rounds=1000$salt$0CdIiZ8n4cm3rRtK.jkSkdF4OXzMYyKHWnHCf8gBg/6"
			if id == "6" {
				hash = "$6$rounds=1000$salt$kIPj/KG/Fylavv3AQlFNF2d5e1UDUex4b33wZ8R8QlHiTn3d8V1c6Tsw.DiM/Hu.9SxM2FLmjaeqFlU4R/Bdx0"
			}
			for i, rounds := range []string{"+01000", " \t1000", "", "-1"} {
				t.Run(fmt.Sprintf("sha%s_normalized_%d", id, i), func(t *testing.T) { check(t, "Hello world!", "$"+id+"$rounds="+rounds+"$salt", hash) })
			}
		}
		for _, v := range []struct {
			n              int
			sha256, sha512 string
		}{
			{31, "PGwsyhw2ozetfwLjeUqRlmp4.vMtSWCXfgtVoXtjvTC", "74aKRrCpH3TuTKizmfBxyv3UM/c4LuBjS3mrbZlQKNgpDNgwu2peMp1G1Btf8Rl6vrCcJDwBYOJUd8066YLl7."},
			{32, "pbcv4vZMljJ95Q5n1D0h4X4FuV3PYeoKFuU39rmMkAA", "Aikdt.q70gfGCaMwK67hOyOD0rBk3DgFS3tuhPAMe7iJabOv7W/MoL.K1lqpQFNWN0YEEUfA6WpM8ozf1BQxz0"},
			{33, "sq3oKL8HCwF0Z2BPl3A1cnHlehE8gjrIbI1jqy.hkK/", "xwq1Jxn7n8RXI.dQxHbPpKnyu.CjkzTWbhI7zUGxfYOxCkAO4ZbGf9UNV0aOflewGFmMFDO6QonnBpG5q2APW1"},
			{63, "sn8QwOEFheh.Jvm5R00gxZX23IkRDpE3tMfg3xa/qS5", "0zApCcHQ.WxebKm2RtcCZ2hMpRboiKFS80vWWEu3tk5CLNI9Gn3PYSFIRmRTatySTO58.u./uZc6iAGF6WYL.0"},
			{64, "jTMVjWMPW7LYqjlDtOFBvor9vCIEBeiKFkHFRRgdLP.", "KsDeVR2dr6X8AtaDF2ZNMO8gghRA2ybZCqzZFSDds4tZW2X0nEeIuXN9z8Nxpy5b06YvmMAVStdmr9hZ3HFlC."},
			{65, "Z6h4pjdgqOmV2UiVrzoLCA0LMIDwQh3DjSJTQ1Dj6j8", "keaoijfblbgDhVdGAhQCKgH5SLd.6Qphv0Lcu5EEC9Us06CRVUIDOfhhzK985yEjEjjkBSYeo6fqxUxY68x5u1"},
			{72, "E5MYF6CR9/LLl6fL8AI6vBKCq7ie9o/heI8A.Wk6H08", "8oeBMpFoktjpBMrrdOD2yznkoCOY9yOu3mRQgWobAvQeda6QQkHMywxDK2S8zHKN06InqSEFsWWKFJr7Xm8P/."},
			{73, "QjApyF1ztCZtub.pjvfdZ5LycIaqHWZV24aUlrOPsgD", "VE/IXC3hrx4goU2MuAdizpqvzRTckySt86yM3MpQdaXAUY08tW9IwgpX5l8qq7IUuh6Xko9q423EP7WISHhLo/"},
			{127, "tl.P.6/jPz9892uhnHcgaltXNHFdjfQOww3ZKmy7Eq6", "fPas3/d7t6bxYaCF1paZS3rIW6CjVs27TWx2dXsfPdhbMl2UmnLjDoShpG171n1jbX64Y.6wFvwQSOGDzC8vd."},
			{128, "oqFmVDWR0jH9HKTqiyrF0ABsnMHlbyu/zsThOfpRpVC", "dG.ExbKKh71XNlkvZVd9sKgAETHVtbn7Mtopdl0m9IWK0zZqv8gsseXvAQ94zXu9XDXPuAMjHHomwtR0Rgxg61"},
			{129, "4VSCGNRsl627muwD4Rt7WvrAfp3zBLft/6ZnhMRoeg8", "/2FnmZmBmOsyneALrg/R3.mc7PX75Bfsh5z6e.JhQ093VPNDvFK8KXn019TrxXbDW.kyrqLb8HzhtLDJaLs0j1"},
		} {
			for i, digest := range []string{v.sha256, v.sha512} {
				t.Run(fmt.Sprintf("sha%d_bytes_%d", i+5, v.n), func(t *testing.T) {
					prefix := fmt.Sprintf("$%d$rounds=1000$salt", i+5)
					check(t, strings.Repeat("a", v.n), prefix, prefix+"$"+digest)
				})
			}
		}
		for _, v := range []struct{ password, sha256, sha512 string }{
			{"é界😀", "gK1JmQIdXq4Y1G6/fUK99rKCbrRRmCi6RIEhQ5pS6K/", "j/slkmRenyKSz3slMIhQmks9ctL8olxLcDopJ83Ddn6U6aPnjvjDoUxSQMd96IxwkTI/RXYRyqVWB7G.Nz6P7."},
			{strings.Repeat("a", 71) + "é", ".u2aLQuYn0Y7Mb5b2vVkziJPkUZxCDblI1Pc41HPAN5", "5lGMFixhxTTbAfZsX7N/RM8fDNbMHT2jXeWe6JrDJdhOABdMRNkDF4OJYt7NwnKrfhn7qdoUcL17mjiu3WyXv/"},
			{strings.Repeat("a", 71) + "ê", "rd4VdNYPLCmCsEd3v2a6rqfUhLD94kwhOOUm9ydQkaC", "MqxHUj8k40gr9Lr8yc9pr4qz6d0itfQ5XV77/XwEQWU7nWEfInNQEEMSIPE3DFUbyqUOCab3Tqf/K83QwNOqg1"},
		} {
			for i, digest := range []string{v.sha256, v.sha512} {
				t.Run(fmt.Sprintf("sha%d_utf8_%x", i+5, v.password), func(t *testing.T) {
					prefix := fmt.Sprintf("$%d$rounds=1000$salt", i+5)
					check(t, v.password, prefix, prefix+"$"+digest)
				})
			}
		}
	})
}
