package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

// RFC2202/RFC4231 and independent Python hashlib/hmac constants supplied by
// the planner. Inputs are hex octets, not algorithm-generated expectations.
var pgcryptoHMACVectors = []struct{ algorithm, data, key, expected string }{
	{"md5", "7768617420646f2079612077616e7420666f72206e6f7468696e673f", "4a656665", "750c783e6ab0b503eaa86e310a5db738"},
	{"md5", "54657374205573696e67204c6172676572205468616e20426c6f636b2d53697a65204b6579202d2048617368204b6579204669727374", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "6b1ab7fe4bd7bf8f0b62e6ce61b9d0cd"},
	{"sha1", "7768617420646f2079612077616e7420666f72206e6f7468696e673f", "4a656665", "effcdf6ae5eb2fa2d27416d5f184df9c259a7c79"},
	{"sha1", "54657374205573696e67204c6172676572205468616e20426c6f636b2d53697a65204b6579202d2048617368204b6579204669727374", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "aa4ae5e15272d00e95705637ce8a3b55ed402112"},
	{"sha224", "7768617420646f2079612077616e7420666f72206e6f7468696e673f", "4a656665", "a30e01098bc6dbbf45690f3a7e9e6d0f8bbea2a39e6148008fd05e44"},
	{"sha224", "54657374205573696e67204c6172676572205468616e20426c6f636b2d53697a65204b6579202d2048617368204b6579204669727374", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "95e9a0db962095adaebe9b2d6f0dbce2d499f112f2d2b7273fa6870e"},
	{"sha256", "7768617420646f2079612077616e7420666f72206e6f7468696e673f", "4a656665", "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843"},
	{"sha256", "54657374205573696e67204c6172676572205468616e20426c6f636b2d53697a65204b6579202d2048617368204b6579204669727374", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "60e431591ee0b67f0d8a26aacbf5b77f8e0bc6213728c5140546040f0ee37f54"},
	{"sha384", "7768617420646f2079612077616e7420666f72206e6f7468696e673f", "4a656665", "af45d2e376484031617f78d2b58a6b1b9c7ef464f5a01b47e42ec3736322445e8e2240ca5e69e2c78b3239ecfab21649"},
	{"sha384", "54657374205573696e67204c6172676572205468616e20426c6f636b2d53697a65204b6579202d2048617368204b6579204669727374", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "4ece084485813e9088d2c63a041bc5b44f9ef1012a2b588f3cd11f05033ac4c60c2ef6ab4030fe8296248df163f44952"},
	{"sha512", "7768617420646f2079612077616e7420666f72206e6f7468696e673f", "4a656665", "164b7a7bfcf819e2e395fbe73b56e0a387bd64222e831fd610270cd7ea2505549758bf75c05a994a6d034f65f8f0e6fdcaeab1a34d4a6b4b636e070a38bce737"},
	{"sha512", "54657374205573696e67204c6172676572205468616e20426c6f636b2d53697a65204b6579202d2048617368204b6579204669727374", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "80b24263c7c1a3ebb71493c1dd7be8b49b46d1f41b4aeec1121b013783f8f3526b56d037e05f2598bd0fd2215d6a1e5295e64f73f63f0aec8b915a985d786598"},
	{"md5", "", "", "74e6f7298a9c2d168935f58c001bad88"},
	{"sha1", "", "", "fbdb1d1b18aa6c08324b7d64b71fb76370690e1d"},
	{"sha224", "", "", "5ce14f72894662213e2748d2a6ba234b74263910cedde2f5a9271524"},
	{"sha256", "", "", "b613679a0814d9ec772f95d778c35fc5ff1697c493715653c6c712144292c5ad"},
	{"sha384", "", "", "6c1f2ee938fad2e24bd91298474382ca218c75db3d83e114b3d4367776d14d3551289e75e8209cd4b792302840234adc"},
	{"sha512", "", "", "b936cee86c9f87aa5d3c6f2e84cb5a4239a5fe50480a6ec66b70ab5b1f4ac6730c6c515421b327ec1d69402e53dfb49ad7381eb067b338fd7b0cb22247225d47"},
	{"md5", "", "6b6579", "63530468a04e386459855da0063b6596"},
	{"sha1", "", "6b6579", "f42bb0eeb018ebbd4597ae7213711ec60760843f"},
	{"sha224", "", "6b6579", "5aa677c13ce1128eeb3a5c01cef7f16557cd0b76d18fd557d6ac3962"},
	{"sha256", "", "6b6579", "5d5d139563c95b5967b9bd9a8c9b233a9dedb45072794cd232dc1b74832607d0"},
	{"sha384", "", "6b6579", "99f44bb4e73c9d0ef26533596c8d8a32a5f8c10a9b997d30d89a7e35ba1ccf200b985f72431202b891fe350da410e43f"},
	{"sha512", "", "6b6579", "84fa5aa0279bbc473267d05a53ea03310a987cecc4c1535ff29b6d76b8f1444a728df3aadb89d4a9a6709e1998f373566e8f824a8ca93b1821f0b69bc2a2f65e"},
	{"md5", "c3a9f09f9982", "636cc3a9", "c5f610ec879473a516fa490b286628bb"},
	{"sha1", "c3a9f09f9982", "636cc3a9", "85b15c0ef87239b404cb8b99f2f0ec6fa43fd257"},
	{"sha224", "c3a9f09f9982", "636cc3a9", "d0e905b4ae8c6caf0a8de7346105f2d3a0d8e14659c6227f717a38e6"},
	{"sha256", "c3a9f09f9982", "636cc3a9", "1dac91ce35914b2e44bf743da5f1f4bf27d41bae2beabc2d25ea455f6fc4718e"},
	{"sha384", "c3a9f09f9982", "636cc3a9", "e63baf85440ad75fba31123d316b8e76582a120d7011ee118493a820f77e8293c865c98091daf245bc4039b547aee5fc"},
	{"sha512", "c3a9f09f9982", "636cc3a9", "a48de5dd7fbd639b90a63aa7ef52686d52d8ebb15977b462fa772e2afec38a74d806b59a3ed0c116c97d9078f61ccb347b6f8ba0472a7ea810d363164e3ad8af"},
	{"md5", "0080ff", "ff0080", "8b6fd99186cf475c9c38b1d47e0bcf67"},
	{"sha1", "0080ff", "ff0080", "91629a23a978f13f62c5ff18a0d992b94998a205"},
	{"sha224", "0080ff", "ff0080", "66bb4af588e9c498885b14043d568ffd0372ecb54e4e3e5cee1d2122"},
	{"sha256", "0080ff", "ff0080", "f085c3bd7fc8d8f90f1a475a943a7f03a97c496a81013f8d54d2e2d229ab562c"},
	{"sha384", "0080ff", "ff0080", "19ebd06bfa54c0e9cb3e85fe13f1b7402c68660bf1c84981726233c2629493473a13b7ee60c26b283aae730d19f467c8"},
	{"sha512", "0080ff", "ff0080", "ab312d732584b1b815682f92e17d7e87138d517d2e0939e8671fa3aa111203aac9b7e3095727110a8ba1794ad72235c551c1c8e98c44bae59df63f1e656deea8"},
	{"md5", "616263", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "81a6963c6f25e3002c2372247c99ecb1"},
	{"md5", "616263", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "f962fb84546b784c85ce2fd0d005514a"},
	{"sha1", "616263", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "c8ac140f19c8efb2881da4546329460847217eba"},
	{"sha1", "616263", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "10f3fcd6d8720e551b639cbc84f8436a17d0498c"},
	{"sha224", "616263", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "0f75fbe2675baf79b7e93a95daa79527774513d89acd3e7e5331a6ae"},
	{"sha224", "616263", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "e810c30c52e6afdfc8f7474368a190cf41aa9332a717de8afda4edab"},
	{"sha256", "616263", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "2f8cff867f2668ca93d3c5b03ba9f816746742eda349b3bc4bb35aa27816754c"},
	{"sha256", "616263", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "102ebc7ceb9dff09e5a77413a3cd67382908ece255487006eb0e1e1b801f9ce6"},
	{"sha384", "616263", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "b6b4e107425289d77b04f0857d531600348f725f48baeeb129718cc6b4e22012fb406ce85dc8c9f64765336e7d1b6afa"},
	{"sha384", "616263", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "1cd03e84204a37f498f949a0a5a1d36015c6f6685734003ea238d98c223d566c3b849da8f82ff808afb310cdf8de8386"},
	{"sha512", "616263", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "92cc1e875510916b8f80a8f09db8c48769e2c248c917eb7ed03b7df45fe61e48019e2d035fa4a910391b7f7387b535d350f9c16cb346ab578d199ab6e559f960"},
	{"sha512", "616263", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "91da74be2b02e8d40c63431a2ada5c236d8b9afc19bfb7823e2361dd12ecf452fca3a6983cbb53067afd75fb31d075755990913f8a08ee307fc713f142498bf8"},
	{"md5", "4869205468657265", "0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b", "9294727a3638bb1c13f48ef8158bfc9d"},
	{"sha1", "4869205468657265", "0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b", "b617318655057264e28bc0b6fb378c8ef146be00"},
}

func TestReferencePgcryptoHMACVectors(t *testing.T) {
	pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		for _, cast := range []string{"bytea", "text"} {
			query := "SELECT hmac($1::" + cast + ",$2::" + cast + ",$3)"
			stmt, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			for i, vector := range pgcryptoHMACVectors {
				data, key := pgcryptoBytes(t, vector.data), pgcryptoBytes(t, vector.key)
				if cast == "text" && (!utf8.ValidString(data) || !utf8.ValidString(key) || bytes.ContainsRune([]byte(data), 0) || bytes.ContainsRune([]byte(key), 0)) {
					continue
				}
				t.Run(fmt.Sprintf("%s/%s/%d", cast, vector.algorithm, i), func(t *testing.T) {
					var d, k any = data, key
					if cast == "bytea" {
						d, k = []byte(data), []byte(key)
					}
					pgcryptoResult(t, p, c, r, "hmac", query, vector.expected, d, k, vector.algorithm)
					if cast == "bytea" {
						pgcryptoResult(t, p, c, r, "hmac", fmt.Sprintf("SELECT hmac('\\x%s'::bytea,'\\x%s'::bytea,'%s')", vector.data, vector.key, vector.algorithm), vector.expected)
					} else {
						pgcryptoResult(t, p, c, r, "hmac", fmt.Sprintf("SELECT hmac('%s'::text,'%s'::text,'%s')", data, key, vector.algorithm), vector.expected)
					}
					var got any
					if err := stmt.QueryRowContext(p.Ctx, d, k, vector.algorithm).Scan(&got); err != nil {
						t.Fatal(err)
					}
					raw, ok := got.([]byte)
					if !ok || !bytes.Equal(raw, []byte(pgcryptoBytes(t, vector.expected))) {
						t.Fatalf("held raw HMAC %T %#v", got, got)
					}
				})
			}
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
		}
	})
}
