package sqltype

import "context"

// The collector owns final mutable payload until all structural and semantic
// validation succeeds. CRLF state resets at each literal, not packet chunks.
type pgpLiteralCollector struct {
	output                                                       pgpScratch
	binary, convert, nonText, gotLiteral, unsupportedCompression bool
}

func (c *pgpLiteralCollector) read(ctx context.Context, input []byte, compressed, noMDC bool) error {
	remaining := input
	gotData := false
	for len(remaining) > 0 {
		framing := pgpDecryptedInner
		if noMDC {
			framing = pgpNoMDCInner
		}
		packet, tail, err := pgpReadPacket(ctx, pgpInput{data: remaining}, framing)
		if err != nil {
			return err
		}
		body := packet.body.data
		if packet.partial && packet.tag != pgpCompressed {
			body = remaining[:packet.size]
			if err = packet.copyTo(ctx, body); err != nil {
				return err
			}
		}
		if packet.tag == pgpCompressed {
			if compressed || gotData {
				return pgpCorrupt()
			}
			gotData = true
			if err = c.readCompressed(ctx, packet); err != nil {
				return err
			}
		} else {
			if packet.tag != pgpLiteral || len(body) < 6 {
				return pgpCorrupt()
			}
			start := 6 + int(body[1])
			if start > len(body) {
				return pgpCorrupt()
			}
			c.gotLiteral = true
			gotData = true
			c.nonText = c.nonText || body[0] != 't' && body[0] != 'u'
			if !c.convert || c.binary {
				if err = (pgpScratchWriter{ctx, &c.output}).writeAll(body[start:]); err != nil {
					return err
				}
			} else {
				// Compact this literal in place; it cannot overtake its unread payload.
				written := start
				for i := start; i < len(body); i++ {
					if i&4095 == 0 {
						if err = ctx.Err(); err != nil {
							return err
						}
					}
					if body[i] == '\r' && i+1 < len(body) && body[i+1] == '\n' {
						continue
					}
					body[written] = body[i]
					written++
				}
				if err = (pgpScratchWriter{ctx, &c.output}).writeAll(body[start:written]); err != nil {
					return err
				}
			}
		}
		remaining = tail.data
	}
	if !gotData {
		return pgpCorrupt()
	}
	return ctx.Err()
}

func (c *pgpLiteralCollector) readCompressed(ctx context.Context, packet pgpPacket) error {
	if packet.size == 0 {
		return pgpCorrupt()
	}
	body := packet.body.data
	if packet.partial {
		body = make([]byte, packet.size)
		defer clear(body)
		if err := packet.copyTo(ctx, body); err != nil {
			return err
		}
	}
	if body[0] == 0 {
		return c.read(ctx, body[1:], true, false)
	}
	if body[0] == 3 {
		c.unsupportedCompression = true
		return ctx.Err()
	}
	var expanded pgpScratch
	defer expanded.clear()
	if err := pgpDecompress(ctx, &expanded, body[1:], body[0], &packet); err != nil {
		return err
	}
	return c.read(ctx, expanded.data, true, false)
}
func pgpReadInner(ctx context.Context, data []byte, binary, convert, noMDC bool) (string, error) {
	collector := pgpLiteralCollector{binary: binary, convert: convert}
	defer collector.output.clear()
	if err := collector.read(ctx, data, false, noMDC); err != nil {
		return "", err
	}
	if collector.unsupportedCompression {
		return "", pgpError("Unsupported compression algorithm")
	}
	if !collector.gotLiteral {
		return "", pgpCorrupt()
	}
	if !binary && collector.nonText {
		return "", pgpError("Not text data")
	}
	if !binary {
		if err := validateTextBytes(ctx, collector.output.data); err != nil {
			return "", err
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return string(collector.output.data), nil
}
