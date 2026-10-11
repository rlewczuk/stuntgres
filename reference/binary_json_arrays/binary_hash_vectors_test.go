package reference_test

import (
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

var binaryHashNames = []string{"crc32", "crc32c", "md5", "sha224", "sha256", "sha384", "sha512"}
var binaryHashTypes = []string{"INT8", "INT8", "TEXT", "BYTEA", "BYTEA", "BYTEA", "BYTEA"}

const binaryHashProjection = "crc32(v),crc32c(v),md5(v),sha224(v),sha256(v),sha384(v),sha512(v)"

// Independent MD5/SHA vectors: PostgreSQL manual/REL_18_0 regressions and
// scout OpenSSL observations for 00 and 0080ff. SHA hex describes raw bytes.
var binaryHashCrypto = [][]string{
	{"d41d8cd98f00b204e9800998ecf8427e", "d14a028c2a3a2bc9476102bb288234c415a2b01f828ea62ac5b3e42f", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", "38b060a751ac96384cd9327eb1b1e36a21fdb71114be07434c0cc7bf63f6e1da274edebfe76f65fbd51ad2f14898b95b", "cf83e1357eefb8bdf1542850d66d8007d620e4050b5715dc83f4a921d36ce9ce47d0d13c5d85f2b0ff8318d2877eec2f63b931bd47417a81a538327af927da3e"},
	{"900150983cd24fb0d6963f7d28e17f72", "23097d223405d8228642a477bda255b32aadbce4bda0b3f7e36c9da7", "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad", "cb00753f45a35e8bb5a03d699ac65007272c32ab0eded1631a8b605a43ff5bed8086072ba1e7cc2358baeca134c825a7", "ddaf35a193617abacc417349ae20413112e6fa4e89a97ea20a9eeee64b55d39a2192992a274fc1a836ba3c23a3feebbd454d4423643ce80e2a9ac94fa54ca49f"},
	{"93b885adfe0da089cdf634904fd59f71", "fff9292b4201617bdc4d3053fce02734166a683d7d858a7f5f59b073", "6e340b9cffb37a989ca544e6bb780a2c78901d3fb33738768511a30617afa01d", "bec021b4f368e3069134e012c2b4307083d3a9bdd206e24e5f0d86e13d6636655933ec2b413465966817a9c208a11717", "b8244d028981d693af7b456af8efa4cad63d282e19ff14942c246e50d9351d22704a802a71c3580b6370de4ceb293c324a8423342557d4e5c38438f0e36910ee"},
	{"edaa0390b22ab88b20932fa7a5e019ff", "dbd8a40778a553071c0c875c4d059a4959dc2b727f03ab77c0f07355", "5240672d7b51756b829ad0ef8d9468b7a078afa2f410484fd3892dab47becb72", "6c7dc7a851e8aa9dd104ee2b23706bb2cd6f49bf1bcebeb255913a96cde07fc81d24803be8989d68e52e118176864060", "ab78f768987c9de19eae63b76c8a7eb3228f170dc2b8b56c95b6a3404a317612a8751c9cf93f568e3d19b42aeb1a40b7b732d52f4c9c5e6c96fe223b3622f7e0"},
	// ASCII 123456789: pinned PostgreSQL 18.0 oracle, 2026-10-09.
	{"25f9e794323b453885f5181f1b624d0b", "9b3e61bf29f17c75572fae2e86e17809a4513d07c8a18152acf34521", "15e2b0d3c33891ebb0f1ef609ec419420c20e320ce94c65fbc8c3312448eb225", "eb455d56d2c1a69de64e832011f3393d45f3fa31d6842f21af92d2fe469c499da5e3179847334a18479c8d1dedea1be3", "d9e6762dd1c8eaf6d61b3c6192fc408d4d6d5f1176d0c29169bc24e71c3f274ad27fcd5811b313d681f7e55ec02d73d499c95455b6b5bb503acf574fba8ffe85"},
}

func binaryHashBytes(t *testing.T, s string) string {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
func binaryHashRow(t *testing.T, index int, crc, crcc int64) []any {
	t.Helper()
	v := binaryHashCrypto[index]
	row := []any{crc, crcc, v[0]}
	for _, s := range v[1:] {
		row = append(row, binaryHashBytes(t, s))
	}
	return row
}
func binaryHashSetup(t *testing.T, p *fixture.Pair, db fixture.Executor, r *fixture.Recorder) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, db, r, fixture.Exec("CREATE TABLE hash_input(id INTEGER,v BYTEA)", 0))
	fixture.RunStep(t, p.Ctx, db, r, fixture.Exec(`INSERT INTO hash_input VALUES(0,NULL),(1,''),(2,'abc'),(3,'\x00'),(4,'\x0080ff'),(5,'123456789')`, 6))
}

func binaryHashVectors(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			binaryHashSetup(t, p, backend.DB, backend.Recorder)
			// CRC NUL/high-byte values and all crypto cells validated by pinned PG18.0.
			for _, tc := range []struct {
				id, index int
				crc, crcc int64
			}{{1, 0, 0, 0}, {2, 1, 891568578, 910901175}, {3, 2, 3523407757, 1383945041}, {4, 3, 3921718996, 920259282}, {5, 4, 3421780262, 3808858755}} {
				s := fixture.Select(fmt.Sprintf("SELECT %s FROM hash_input WHERE id=%d", binaryHashProjection, tc.id), binaryHashNames, binaryHashTypes, [][]any{binaryHashRow(t, tc.index, tc.crc, tc.crcc)})
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(s))
			}
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT "+binaryHashProjection+" FROM hash_input WHERE id=0", binaryHashNames, binaryHashTypes, [][]any{{nil, nil, nil, nil, nil, nil, nil}}))
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(fixture.Select("SELECT "+binaryHashProjection+" FROM hash_input WHERE id=0", binaryHashNames, binaryHashTypes, [][]any{{nil, nil, nil, nil, nil, nil, nil}})))
			for _, tc := range []struct {
				n    int
				want int64
			}{{127, 291820082}, {128, 816091258}, {129, 4213642571}, {800, 3134039419}} {
				s := fixture.Select("SELECT crc32c($1)", []string{"crc32c"}, []string{"INT8"}, [][]any{{tc.want}}, []byte(strings.Repeat("A", tc.n)))
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(s))
			}
		})
	}
}
func TestReferenceBinaryHashVectors(t *testing.T) { binaryHashVectors(t) }
func TestReferenceBinaryHashSmoke(t *testing.T) {
	binaryHashVectors(t)
	t.Run("MD5 overloads", TestReferenceBinaryHashMD5Overloads)
}

// Assert raw driver types before the fixture's []byte normalization.
func binaryHashRawScan(t *testing.T, rows *sql.Rows, null bool) {
	t.Helper()
	values := make([]any, 7)
	dest := make([]any, 7)
	for i := range dest {
		dest[i] = &values[i]
	}
	if err := rows.Scan(dest...); err != nil {
		t.Fatal(err)
	}
	for i, v := range values {
		if null {
			if v != nil {
				t.Fatal(i, v)
			}
			continue
		}
		switch {
		case i < 2:
			if _, ok := v.(int64); !ok {
				t.Fatalf("CRC raw %T", v)
			}
		case i == 2:
			if s, ok := v.(string); !ok || len(s) != 32 {
				t.Fatalf("MD5 raw %T %v", v, v)
			}
		default:
			if b, ok := v.([]byte); !ok || len(b) != []int{28, 32, 48, 64}[i-3] {
				t.Fatalf("SHA raw %T %v", v, v)
			}
		}
	}
}
