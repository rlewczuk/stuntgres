package reference_test

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgcryptoPGPSymmetricCompression(t *testing.T) {
	symAnchor(t)
	for _, algorithm := range []byte{1, 2} {
		for _, level := range []int{1, 6, 9} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("algorithm_%d/level_%d/%s", algorithm, level, symRouteName(prepared)), func(t *testing.T) {
					pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
						input := []byte(strings.Repeat("compress π\n", 2000))
						profile := symDefault()
						profile.digest = 8
						profile.compression = algorithm
						options := symOptions(profile) + fmt.Sprintf(",compress-level=%d", level)
						packet := symEncrypt(t, p, c, prepared, false, input, "compression", &options, profile, 't', input, 1024)
						symDecrypt(t, p, c, r, packet, "compression", input, true, nil)
						inner := symCompress(pgpRFCLiteral(string(input), "partial", 't'), algorithm, level)
						symDecrypt(t, p, c, r, symBuild("compression", inner, profile, ""), "compression", input, true, nil)
					})
				})
			}
		}
	}
	for _, tc := range []struct {
		name, options string
		algorithm     byte
	}{
		{"zip_default_level", "compress-algo=1,s2k-count=1024", 1},
		{"zlib_default_level", "compress-algo=2,s2k-count=1024", 2},
		{"zip_level0", "compress-algo=1,compress-level=0,s2k-count=1024", 0},
		{"zlib_level0", "compress-algo=2,compress-level=0,s2k-count=1024", 0},
		{"bzip_level0", "compress-algo=3,compress-level=0,s2k-count=1024", 0},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(tc.name+"/"+symRouteName(prepared), func(t *testing.T) {
				pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
					profile := symDefault()
					profile.compression = tc.algorithm
					input := []byte("default compression level/bypass")
					packet := symEncrypt(t, p, c, prepared, false, input, "compression", &tc.options, profile, 't', input, 1024)
					symDecrypt(t, p, c, r, packet, "compression", input, true, nil)
				})
			})
		}
	}
	// Deterministic SHA256 counter blocks are incompressible fixture material,
	// not SQL-generated randomness; this is the upstream 16366-byte regression.
	for _, algorithm := range []byte{1, 2} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("incompressible_%d/%s", algorithm, symRouteName(prepared)), func(t *testing.T) {
				pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
					input := make([]byte, 0, 16366)
					for i := 0; len(input) < 16366; i++ {
						sum := sha256.Sum256([]byte(fmt.Sprintf("incompressible-%d", i)))
						input = append(input, sum[:min(len(sum), 16366-len(input))]...)
					}
					profile := symDefault()
					profile.compression = algorithm
					options := symOptions(profile)
					packet := symEncrypt(t, p, c, prepared, true, input, "compression", &options, profile, 'b', input, 1024)
					symDecrypt(t, p, c, r, packet, "compression", input, false, nil)
				})
			})
		}
	}
	literal := pgpRFCLiteral("compressed witness", "", 't')
	zip := symCompress(literal, 1, 6)
	zlib := symCompress(literal, 2, 6)
	zp, _, err := pgpRFCRead(zip, false)
	if err != nil {
		t.Fatal(err)
	}
	zl, _, err := pgpRFCRead(zlib, false)
	if err != nil {
		t.Fatal(err)
	}
	checksum := append([]byte(nil), zl.body...)
	checksum[len(checksum)-1] ^= 1
	for _, tc := range []struct {
		name          string
		inner         []byte
		want, message string
	}{
		{"official_zip", nil, "Secret message", ""},
		{"algorithm0", symCompress(literal, 0, 6), "compressed witness", ""},
		{"authored_sha256_zlib", zlib, "compressed witness", ""},
		{"compressed_then_literal", append(append([]byte(nil), zip...), pgpRFCLiteral(" tail", "", 'u')...), "compressed witness tail", ""},
		{"nested", symCompress(zip, 2, 6), "", "Wrong key or corrupt data"},
		{"compression_after_literal", append(append([]byte(nil), literal...), zip...), "", "Wrong key or corrupt data"},
		{"truncated_zip", pgpRFCFrame(8, zp.body[:len(zp.body)-2], ""), "", "Wrong key or corrupt data"},
		{"invalid_zip", pgpRFCFrame(8, []byte{1, 255, 255, 255}, ""), "", "Wrong key or corrupt data"},
		{"truncated_zlib", pgpRFCFrame(8, zl.body[:len(zl.body)-2], ""), "", "Wrong key or corrupt data"},
		{"zlib_checksum", pgpRFCFrame(8, checksum, ""), "", "Wrong key or corrupt data"},
		{"bzip2", pgpRFCFrame(8, []byte{3, 0}, ""), "", "Unsupported compression algorithm"},
		{"unknown", pgpRFCFrame(8, []byte{4, 0}, ""), "", "Wrong key or corrupt data"},
		// Release zlib can buffer past STREAM_END: this tiny suffix is already
		// consumed by its packet source. Do not invent universal suffix rejection.
		{"buffered_zip_suffix", pgpRFCFrame(8, append(append([]byte(nil), zp.body...), 0), ""), "compressed witness", ""},
		{"unread_zip_suffix", pgpRFCFrame(8, append(append([]byte(nil), zp.body...), make([]byte, 16384)...), ""), "", "Wrong key or corrupt data"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
				profile := symDefault()
				profile.digest = 8
				packet := symBuild("compression", tc.inner, profile, "")
				password := "compression"
				if tc.name == "official_zip" {
					packet = symBase64(t, symZIP64)
					password = "key"
				}
				if tc.message == "" {
					symDecrypt(t, p, c, r, packet, password, []byte(tc.want), true, nil)
				} else {
					for _, prepared := range []bool{false, true} {
						pgpNoLeak(t, p, c, prepared, "SELECT pgp_sym_decrypt($1::bytea,$2::text)", tc.message, packet, password)
					}
				}
				if tc.name != "official_zip" {
					for _, prepared := range []bool{false, true} {
						pgpNoLeak(t, p, c, prepared, "SELECT pgp_sym_decrypt($1::bytea,$2::text)", "Wrong key or corrupt data", symBuild(password, tc.inner, profile, "mdc"), password)
					}
				}
			})
		})
	}
	// Expanded aggregates are unbounded, while each declared expanded chunk
	// remains subject to the release's 16MiB rule. One large body per scenario.
	for _, over := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("expanded_overlimit_%v/%s", over, symRouteName(prepared)), func(t *testing.T) {
				pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
					input := strings.Repeat("L", 16*1024*1024+1)
					form := "partial"
					if over {
						form = "five"
					}
					inner := symCompress(pgpRFCLiteral(input, form, 't'), 2, 6)
					profile := symDefault()
					profile.digest = 8
					packet := symBuild("compression", inner, profile, "")
					d, err := symDecode(packet, "compression")
					if err != nil || string(d.data) != input {
						t.Fatal("RFC-valid expanded fixture", err)
					}
					if over {
						pgpNoLeak(t, p, c, prepared, "SELECT pgp_sym_decrypt($1::bytea,$2::text)", "Wrong key or corrupt data", packet, "compression")
					} else {
						s := pgpTextStep("SELECT pgp_sym_decrypt($1::bytea,$2::text)", input, packet, "compression")
						if prepared {
							s = fixture.Prepared(s)
						}
						fixture.RunStep(t, p.Ctx, c, r, s)
					}
				})
			})
		}
	}
}

func TestReferencePgcryptoPGPSymmetricNoMDC(t *testing.T) {
	symAnchor(t)
	for _, id := range []byte{2, 3, 4, 7, 8, 9} {
		bs := 8
		if id >= 7 {
			bs = 16
		}
		for _, n := range []int{0, 1, bs - 1, bs, bs + 1, 16385} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("cipher_%d/length_%d/%s", id, n, symRouteName(prepared)), func(t *testing.T) {
					pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
						profile := symProfile{cipher: id, wrap: id, digest: 8, mode: 3, noMDC: true}
						input := bytes.Repeat([]byte{'N'}, n)
						options := symOptions(profile)
						packet := symEncrypt(t, p, c, prepared, true, input, "resync", &options, profile, 'b', input, 1024)
						symDecrypt(t, p, c, r, packet, "resync", input, false, nil)
						authored := symBuild("resync", pgpRFCLiteral(string(input), "partial", 'b'), profile, "")
						symDecrypt(t, p, c, r, authored, "resync", input, false, nil)
					})
				})
			}
		}
	}
	t.Run("failures_and_intentional_malleability", func(t *testing.T) {
		pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
			profile := symDefault()
			profile.noMDC = true
			input := []byte("no MDC explicitly permits undetected modification")
			inner := pgpRFCLiteral(string(input), "", 't')
			packet := symBuild("resync", inner, profile, "")
			for _, tc := range []struct {
				name     string
				packet   []byte
				password string
			}{
				{"wrong_password", packet, "incorrect"},
				{"mismatched_prefix_check", symBuild("resync", inner, profile, "prefix"), "resync"},
				{"invalid_inner_packet_header", symBuild("resync", []byte{0}, profile, ""), "resync"},
			} {
				for _, prepared := range []bool{false, true} {
					t.Run(tc.name+"/"+symRouteName(prepared), func(t *testing.T) {
						pgpNoLeak(t, p, c, prepared, "SELECT pgp_sym_decrypt($1::bytea,$2::text)", "Wrong key or corrupt data", tc.packet, tc.password)
					})
				}
			}
			// Bounded release EOF case: this short tag9 body is one definite
			// chunk containing one definite literal. Removing its final ciphertext
			// octet leaves both declared lengths unchanged, but PostgreSQL returns
			// the available literal payload. CFB leaves preceding octets unchanged;
			// there is no MDC or padding. This is not a universal framing waiver.
			for _, prepared := range []bool{false, true} {
				t.Run("definite_tag9_final_payload_eof/"+symRouteName(prepared), func(t *testing.T) {
					s := pgpTextStep("SELECT pgp_sym_decrypt($1::bytea,$2::text)", "no MDC explicitly permits undetected modificatio", packet[:len(packet)-1], "resync")
					if prepared {
						s = fixture.Prepared(s)
					}
					fixture.RunStep(t, p.Ctx, c, r, s)
				})
			}
			// Flip only the final payload octet: no subsequent CFB block and no
			// MDC exist, so literal framing and all earlier octets stay intact.
			tampered := append([]byte(nil), packet...)
			tampered[len(tampered)-1] ^= 1
			altered := append([]byte(nil), input...)
			altered[len(altered)-1] ^= 1
			symDecrypt(t, p, c, r, tampered, "resync", altered, true, nil)
			symDecrypt(t, p, c, r, symBase64(t, symNoMDC64), "foobar", []byte("Secret message."), true, nil)
		})
	})
	t.Run("bounded_eof_framing", func(t *testing.T) {
		pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
			profile := symDefault()
			profile.noMDC = true
			input := "no MDC explicitly permits undetected modification"
			inner := pgpRFCLiteral(input, "", 't')
			packet := symBuild("resync", inner, profile, "")
			keyPacket, encryptedFrame, err := pgpRFCRead(packet, false)
			if err != nil {
				t.Fatal(err)
			}
			encrypted, tail, err := pgpRFCRead(encryptedFrame, false)
			if err != nil || len(tail) != 0 {
				t.Fatal("complete tag9 fixture", err)
			}
			keyFrame := pgpRFCFrame(3, keyPacket.body, "")
			// AES prefix18 + definite literal framing2 + literal metadata6.
			// The advertised outer/literal lengths remain unchanged at either cut.
			metadataEnd := len(packet) - len(input)
			completeOuterShortLiteral := append(append([]byte(nil), keyFrame...), pgpRFCFrame(9, encrypted.body[:len(encrypted.body)-1], "")...)
			longPacket := symBuild("resync", pgpRFCLiteral(strings.Repeat("P", 600), "", 't'), profile, "")
			longKey, longEncryptedFrame, err := pgpRFCRead(longPacket, false)
			if err != nil {
				t.Fatal(err)
			}
			longEncrypted, longTail, err := pgpRFCRead(longEncryptedFrame, false)
			if err != nil || len(longTail) != 0 {
				t.Fatal("complete partial-source fixture", err)
			}
			// Author a 512-byte initial partial chunk directly, with no terminal
			// length. A 600-byte literal has framing3 and metadata6: its payload
			// starts at decrypted offset18+3+6=27. Supplying63 ciphertext octets
			// therefore supplies exactly36 P octets, inside the declared chunk.
			partialHeader := append(pgpRFCFrame(3, longKey.body, ""), byte(0xc9), byte(0xe9))
			insidePartial := append(append([]byte(nil), partialHeader...), longEncrypted.body[:63]...)
			exhaustedPartial := append(append([]byte(nil), partialHeader...), longEncrypted.body[:512]...)
			authenticatedProfile := profile
			authenticatedProfile.noMDC = false
			authenticated := symBuild("resync", inner, authenticatedProfile, "")
			for _, tc := range []struct {
				name          string
				packet        []byte
				want, message string
			}{
				{"definite_eof_after_literal_metadata", packet[:metadataEnd], "", ""},
				{"definite_eof_inside_literal_metadata", packet[:metadataEnd-1], "", "Wrong key or corrupt data"},
				{"partial_eof_inside_declared_chunk", insidePartial, strings.Repeat("P", 36), ""},
				{"partial_exhausted_missing_next_length", exhaustedPartial, "", "Wrong key or corrupt data"},
				{"complete_outer_short_definite_literal", completeOuterShortLiteral, "no MDC explicitly permits undetected modificatio", ""},
				{"tag18_final_ciphertext_eof", authenticated[:len(authenticated)-1], "", "Wrong key or corrupt data"},
			} {
				for _, prepared := range []bool{false, true} {
					t.Run(tc.name+"/"+symRouteName(prepared), func(t *testing.T) {
						// These distinguish release EOF handling from complete
						// metadata, partial continuation and authentication checks;
						// they do not relax any other packet/chunk witness.
						q := "SELECT pgp_sym_decrypt($1::bytea,$2::text)"
						if tc.message != "" {
							pgpNoLeak(t, p, c, prepared, q, tc.message, tc.packet, "resync")
							return
						}
						s := pgpTextStep(q, tc.want, tc.packet, "resync")
						if prepared {
							s = fixture.Prepared(s)
						}
						fixture.RunStep(t, p.Ctx, c, r, s)
					})
				}
			}
		})
	})
	// Declared-body EOF behavior is independent of the definite length encoding.
	// Keep the independent RFC reader strict: only complete source packets are
	// decoded here, before deliberately shortening their public SQL inputs.
	for _, outerForm := range []string{"old1", "old2", "old4", "five"} {
		for _, innerForm := range []string{"old1", "old2", "old4", "five"} {
			t.Run("definite_eof_forms/outer_"+outerForm+"/inner_"+innerForm, func(t *testing.T) {
				pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
					profile := symDefault()
					profile.noMDC = true
					input := "no MDC explicitly permits undetected modification"
					source := symBuild("resync", pgpRFCLiteral(input, innerForm, 't'), profile, "")
					key, encryptedFrame, err := pgpRFCRead(source, false)
					if err != nil {
						t.Fatal(err)
					}
					encrypted, tail, err := pgpRFCRead(encryptedFrame, false)
					if err != nil || len(tail) != 0 {
						t.Fatal("definite source packet", err)
					}
					keyFrame := pgpRFCFrame(3, key.body, "")
					packet := append(append([]byte(nil), keyFrame...), pgpRFCFrame(9, encrypted.body, outerForm)...)
					// Removing the known payload length leaves complete literal
					// metadata regardless of either framing header's octet count.
					metadataEnd := len(packet) - len(input)
					completeOuter := append(append([]byte(nil), keyFrame...), pgpRFCFrame(9, encrypted.body[:len(encrypted.body)-1], outerForm)...)
					for _, tc := range []struct {
						name          string
						packet        []byte
						want, message string
					}{
						{"outer_payload_eof", packet[:len(packet)-1], "no MDC explicitly permits undetected modificatio", ""},
						{"outer_eof_after_literal_metadata", packet[:metadataEnd], "", ""},
						{"outer_eof_inside_literal_metadata", packet[:metadataEnd-1], "", "Wrong key or corrupt data"},
						{"complete_outer_literal_payload_eof", completeOuter, "no MDC explicitly permits undetected modificatio", ""},
					} {
						for _, prepared := range []bool{false, true} {
							t.Run(tc.name+"/"+symRouteName(prepared), func(t *testing.T) {
								q := "SELECT pgp_sym_decrypt($1::bytea,$2::text)"
								if tc.message != "" {
									pgpNoLeak(t, p, c, prepared, q, tc.message, tc.packet, "resync")
									return
								}
								s := pgpTextStep(q, tc.want, tc.packet, "resync")
								if prepared {
									s = fixture.Prepared(s)
								}
								fixture.RunStep(t, p.Ctx, c, r, s)
							})
						}
					}
				})
			})
		}
	}
	t.Run("complete_outer_inner_partial_eof", func(t *testing.T) {
		pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
			profile := symDefault()
			profile.noMDC = true
			for _, tc := range []struct {
				name          string
				payload       int
				want, message string
			}{
				// The declared initial literal chunk is512. Its first6 octets
				// are complete metadata; the supplied body63 leaves57 P octets.
				{"outstanding_literal_chunk", 57, strings.Repeat("P", 57), ""},
				// Exactly512 body octets demand a following length field, even
				// when the containing tag9 frame is complete and well formed.
				{"exhausted_literal_chunk_missing_length", 506, "", "Wrong key or corrupt data"},
			} {
				body := append([]byte{'t', 0, 0, 0, 0, 0}, []byte(strings.Repeat("P", tc.payload))...)
				inner := append([]byte{0xcb, 0xe9}, body...)
				packet := symBuild("resync", inner, profile, "")
				for _, prepared := range []bool{false, true} {
					t.Run(tc.name+"/"+symRouteName(prepared), func(t *testing.T) {
						q := "SELECT pgp_sym_decrypt($1::bytea,$2::text)"
						if tc.message != "" {
							pgpNoLeak(t, p, c, prepared, q, tc.message, packet, "resync")
							return
						}
						s := pgpTextStep(q, tc.want, packet, "resync")
						if prepared {
							s = fixture.Prepared(s)
						}
						fixture.RunStep(t, p.Ctx, c, r, s)
					})
				}
			}
		})
	})
}
