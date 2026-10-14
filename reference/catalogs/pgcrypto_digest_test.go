package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

// Fixed published digest constants; expectations never call crypto implementations.
var pgcryptoDigestVectors = []struct{ algorithm, abc, empty string }{
	{"md5", "900150983cd24fb0d6963f7d28e17f72", "d41d8cd98f00b204e9800998ecf8427e"},
	{"sha1", "a9993e364706816aba3e25717850c26c9cd0d89d", "da39a3ee5e6b4b0d3255bfef95601890afd80709"},
	{"sha224", "23097d223405d8228642a477bda255b32aadbce4bda0b3f7e36c9da7", "d14a028c2a3a2bc9476102bb288234c415a2b01f828ea62ac5b3e42f"},
	{"sha256", "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
	{"sha384", "cb00753f45a35e8bb5a03d699ac65007272c32ab0eded1631a8b605a43ff5bed8086072ba1e7cc2358baeca134c825a7", "38b060a751ac96384cd9327eb1b1e36a21fdb71114be07434c0cc7bf63f6e1da274edebfe76f65fbd51ad2f14898b95b"},
	{"sha512", "ddaf35a193617abacc417349ae20413112e6fa4e89a97ea20a9eeee64b55d39a2192992a274fc1a836ba3c23a3feebbd454d4423643ce80e2a9ac94fa54ca49f", "cf83e1357eefb8bdf1542850d66d8007d620e4050b5715dc83f4a921d36ce9ce47d0d13c5d85f2b0ff8318d2877eec2f63b931bd47417a81a538327af927da3e"},
}

func TestReferencePgcryptoDigestVectors(t *testing.T) {
	pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		for _, binary := range []bool{false, true} {
			cast := "text"
			if binary {
				cast = "bytea"
			}
			query := "SELECT digest($1::" + cast + ",$2)"
			stmt, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			for _, vector := range pgcryptoDigestVectors {
				for _, input := range []string{"abc", ""} {
					expected := vector.abc
					if input == "" {
						expected = vector.empty
					}
					var argument any = input
					if binary {
						argument = []byte(input)
					}
					pgcryptoResult(t, p, c, r, "digest", query, expected, argument, vector.algorithm)
					pgcryptoResult(t, p, c, r, "digest", fmt.Sprintf("SELECT digest('%s'::%s,'%s')", input, cast, vector.algorithm), expected)
					var got any
					if err := stmt.QueryRowContext(p.Ctx, argument, vector.algorithm).Scan(&got); err != nil {
						t.Fatal(err)
					}
					raw, ok := got.([]byte)
					if !ok || !bytes.Equal(raw, []byte(pgcryptoBytes(t, expected))) {
						t.Fatalf("raw digest %T %#v", got, got)
					}
				}
			}
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
		}
		// Independent offline hashlib supplements: UTF8 octets and NUL/high bits.
		binaryQuery := "SELECT digest($1::bytea,$2)"
		binaryStmt, err := c.PrepareContext(p.Ctx, binaryQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer binaryStmt.Close()
		textQuery := "SELECT digest($1::text,$2)"
		textStmt, err := c.PrepareContext(p.Ctx, textQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer textStmt.Close()
		for _, vector := range []struct {
			algorithm, input, expected string
			binary                     bool
		}{
			{"md5", "0080ff", "edaa0390b22ab88b20932fa7a5e019ff", true},
			{"sha1", "0080ff", "f883686921f6ae7b94c63937715256438dc15d16", true},
			{"sha224", "0080ff", "dbd8a40778a553071c0c875c4d059a4959dc2b727f03ab77c0f07355", true},
			{"sha256", "0080ff", "5240672d7b51756b829ad0ef8d9468b7a078afa2f410484fd3892dab47becb72", true},
			{"sha384", "0080ff", "6c7dc7a851e8aa9dd104ee2b23706bb2cd6f49bf1bcebeb255913a96cde07fc81d24803be8989d68e52e118176864060", true},
			{"sha512", "0080ff", "ab78f768987c9de19eae63b76c8a7eb3228f170dc2b8b56c95b6a3404a317612a8751c9cf93f568e3d19b42aeb1a40b7b732d52f4c9c5e6c96fe223b3622f7e0", true},
			{"md5", "c3a9f09f9982", "93aecbf32b30fc4b9444a1e26c48abe4", false},
			{"sha1", "c3a9f09f9982", "2bc6c3ebb92f5afa436e5f9b4312b2ad5e197d97", false},
			{"sha224", "c3a9f09f9982", "61810ad85cf9af49e39d593a90e9344466df43e601ad95163a3965a8", false},
			{"sha256", "c3a9f09f9982", "7382f537af6a53054b6a72792df00fa0be0f5d2e8214db9cd6ebbcc3d57d02b9", false},
			{"sha384", "c3a9f09f9982", "62aba307e73e79acb68b0e6f8b98cd071ba6468f9088e63d6ada3ce0d27f11563e37b82b91f86f8ed469d0c841b46fc9", false},
			{"sha512", "c3a9f09f9982", "5494c1d83f56e4972b690e25fe1de30df5ba07b9c1e2a5ed764b92f8a98595a152302c0c10faf5d133a5c0fc13c2c6daf090fa571bdcfe321159cf4121763928", false},
		} {
			input := pgcryptoBytes(t, vector.input)
			pgcryptoResult(t, p, c, r, "digest", "SELECT digest($1::bytea,$2)", vector.expected, []byte(input), vector.algorithm)
			pgcryptoResult(t, p, c, r, "digest", fmt.Sprintf("SELECT digest('\\x%s'::bytea,'%s')", vector.input, vector.algorithm), vector.expected)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Statement: binaryStmt, SQL: binaryQuery}, r, fixture.Select(binaryQuery, []string{"digest"}, []string{"BYTEA"}, [][]any{{pgcryptoBytes(t, vector.expected)}}, []byte(input), vector.algorithm))
			if !vector.binary {
				pgcryptoResult(t, p, c, r, "digest", textQuery, vector.expected, input, vector.algorithm)
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Statement: textStmt, SQL: textQuery}, r, fixture.Select(textQuery, []string{"digest"}, []string{"BYTEA"}, [][]any{{pgcryptoBytes(t, vector.expected)}}, input, vector.algorithm))
			}
		}
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT md5('abc')", []string{"md5"}, []string{"TEXT"}, [][]any{{"900150983cd24fb0d6963f7d28e17f72"}}))
	})
}
