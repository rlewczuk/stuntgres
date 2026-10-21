package sqltype

// Blowfish's fixed P/S state and Feistel primitive are shared by raw BF and
// PostgreSQL's bcrypt protocol. Derived from Solar Designer's public-domain
// crypt-blowfish.c (1998–2002); key packing belongs to each caller's protocol.
// https://raw.githubusercontent.com/postgres/postgres/REL_18_0/contrib/pgcrypto/crypt-blowfish.c
type pgcryptoBlowfishState struct {
	p [18]uint32
	s [4][256]uint32
}

func (s *pgcryptoBlowfishState) f(x uint32) uint32 {
	return ((s.s[0][byte(x>>24)] + s.s[1][byte(x>>16)]) ^ s.s[2][byte(x>>8)]) + s.s[3][byte(x)]
}

func (s *pgcryptoBlowfishState) encrypt(left, right uint32) (uint32, uint32) {
	left ^= s.p[0]
	for i := 1; i <= 16; i += 2 {
		right ^= s.f(left) ^ s.p[i]
		left ^= s.f(right) ^ s.p[i+1]
	}
	return right ^ s.p[17], left
}

func (s *pgcryptoBlowfishState) decrypt(left, right uint32) (uint32, uint32) {
	left ^= s.p[17]
	for i := 16; i >= 1; i -= 2 {
		right ^= s.f(left) ^ s.p[i]
		left ^= s.f(right) ^ s.p[i-1]
	}
	return right ^ s.p[0], left
}

// expand never allocates. Raw BF passes its packed key and nil salt once.
// Bcrypt mixes salt only during initial expansion; later expansions pass nil.
func (s *pgcryptoBlowfishState) expand(key []uint32, salt *[4]uint32) {
	for i := range s.p {
		s.p[i] ^= key[i%len(key)]
	}
	var left, right uint32
	position := 0
	for i := 0; i < len(s.p); i += 2 {
		if salt != nil {
			left ^= salt[position&3]
			right ^= salt[(position+1)&3]
			position += 2
		}
		left, right = s.encrypt(left, right)
		s.p[i], s.p[i+1] = left, right
	}
	for box := range s.s {
		for i := 0; i < len(s.s[box]); i += 2 {
			if salt != nil {
				left ^= salt[position&3]
				right ^= salt[(position+1)&3]
				position += 2
			}
			left, right = s.encrypt(left, right)
			s.s[box][i], s.s[box][i+1] = left, right
		}
	}
}
