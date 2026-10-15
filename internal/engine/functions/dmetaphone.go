package functions

// Ported from PostgreSQL REL_18_0 on 2026-10-15: Go byte reader, virtual
// padding, fixed output buffers, cached full-input classification and cancellation.
// Perl Artistic License alternative: see dmetaphone_LICENSE.txt. Modified source
// is distributed with this package; preserve source with redistributed binaries.
/***************************** COPYRIGHT NOTICES ***********************

Most of this code is directly from the Text::DoubleMetaphone perl module
version 0.05 available from https://www.cpan.org/.
It bears this copyright notice:

  Copyright 2000, Maurice Aubrey <maurice@hevanet.com>.
  All rights reserved.

  This code is based heavily on the C++ implementation by
  Lawrence Philips and incorporates several bug fixes courtesy
  of Kevin Atkinson <kevina@users.sourceforge.net>.

  This module is free software; you may redistribute it and/or
  modify it under the same terms as Perl itself.

The remaining code is authored by Andrew Dunstan <amdunstan@ncshp.org> and
<andrew@dunslane.net> and is covered this copyright:

  Copyright 2003, North Carolina State Highway Patrol.
  All rights reserved.

  Permission to use, copy, modify, and distribute this software and its
  documentation for any purpose, without fee, and without a written agreement
  is hereby granted, provided that the above copyright notice and this
  paragraph and the following two paragraphs appear in all copies.

  IN NO EVENT SHALL THE NORTH CAROLINA STATE HIGHWAY PATROL BE LIABLE TO ANY
  PARTY FOR DIRECT, INDIRECT, SPECIAL, INCIDENTAL, OR CONSEQUENTIAL DAMAGES,
  INCLUDING LOST PROFITS, ARISING OUT OF THE USE OF THIS SOFTWARE AND ITS
  DOCUMENTATION, EVEN IF THE NORTH CAROLINA STATE HIGHWAY PATROL HAS BEEN
  ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

  THE NORTH CAROLINA STATE HIGHWAY PATROL SPECIFICALLY DISCLAIMS ANY
  WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES OF
  MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE.  THE SOFTWARE PROVIDED
  HEREUNDER IS ON AN "AS IS" BASIS, AND THE NORTH CAROLINA STATE HIGHWAY PATROL
  HAS NO OBLIGATIONS TO PROVIDE MAINTENANCE, SUPPORT, UPDATES, ENHANCEMENTS, OR
  MODIFICATIONS.

***********************************************************************/

import "context"

type doubleMetaphoneState struct {
	source             string
	primary, alternate [4]byte
	pn, an             int
	slavo              bool
}

func (d *doubleMetaphoneState) at(i int) byte {
	if i < 0 {
		return 0
	}
	if i < len(d.source) {
		return phoneticUpper(d.source[i])
	}
	if i-len(d.source) < 5 {
		return ' '
	}
	return 0
}
func (d *doubleMetaphoneState) vowel(i int) bool {
	switch d.at(i) {
	case 'A', 'E', 'I', 'O', 'U', 'Y':
		return true
	}
	return false
}
func (d *doubleMetaphoneState) is(i int, patterns ...string) bool {
	if i < 0 || i-len(d.source) >= 5 {
		return false
	}
	for _, p := range patterns {
		match := true
		for j := range len(p) {
			if d.at(i+j) != p[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
func (d *doubleMetaphoneState) add(primary, alternate string) {
	for i := 0; i < len(primary) && d.pn < 4; i++ {
		d.primary[d.pn] = primary[i]
		d.pn++
	}
	for i := 0; i < len(alternate) && d.an < 4; i++ {
		d.alternate[d.an] = alternate[i]
		d.an++
	}
}

// DoubleMetaphone returns both independent, unpadded release codes. It borrows
// the input without building an input-sized uppercase or padded copy.
func DoubleMetaphone(ctx context.Context, source string) (string, string, error) {
	d, err := doubleMetaphone(ctx, source)
	if err != nil {
		return "", "", err
	}
	return string(d.primary[:d.pn]), string(d.alternate[:d.an]), nil
}

func doubleMetaphoneCode(ctx context.Context, source string, alternate bool) (string, error) {
	d, err := doubleMetaphone(ctx, source)
	if err != nil {
		return "", err
	}
	if alternate {
		return string(d.alternate[:d.an]), nil
	}
	return string(d.primary[:d.pn]), nil
}

func doubleMetaphone(ctx context.Context, source string) (doubleMetaphoneState, error) {
	if err := ctx.Err(); err != nil {
		return doubleMetaphoneState{}, err
	}
	d := doubleMetaphoneState{source: source}
	for i := range len(source) {
		if i&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return doubleMetaphoneState{}, err
			}
		}
		if d.at(i) == 'W' || d.at(i) == 'K' || d.is(i, "CZ") {
			d.slavo = true
			break
		}
	}
	i := 0
	if d.is(0, "GN", "KN", "PN", "WR", "PS") {
		i++
	}
	if d.at(0) == 'X' {
		d.add("S", "S")
		i++
	}
	for steps := 0; i < len(source) && (d.pn < 4 || d.an < 4); steps++ {
		if steps&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return doubleMetaphoneState{}, err
			}
		}
		switch d.at(i) {
		case 'A', 'E', 'I', 'O', 'U', 'Y':
			if i == 0 {
				d.add("A", "A")
			}
			i++
		case 'B':
			d.add("P", "P")
			if d.at(i+1) == 'B' {
				i += 2
			} else {
				i++
			}
		case 0xc7:
			d.add("S", "S")
			i++
		case 'C':
			i = d.c(i)
		case 'D':
			if d.is(i, "DG") {
				if d.is(i+2, "I", "E", "Y") {
					d.add("J", "J")
					i += 3
				} else {
					d.add("TK", "TK")
					i += 2
				}
			} else {
				d.add("T", "T")
				if d.is(i, "DT", "DD") {
					i += 2
				} else {
					i++
				}
			}
		case 'F':
			d.add("F", "F")
			if d.at(i+1) == 'F' {
				i += 2
			} else {
				i++
			}
		case 'G':
			i = d.g(i)
		case 'H':
			if (i == 0 || d.vowel(i-1)) && d.vowel(i+1) {
				d.add("H", "H")
				i += 2
			} else {
				i++
			}
		case 'J':
			i = d.j(i)
		case 'K':
			d.add("K", "K")
			if d.at(i+1) == 'K' {
				i += 2
			} else {
				i++
			}
		case 'L':
			i = d.l(i)
		case 'M':
			d.add("M", "M")
			if d.is(i-1, "UMB") && (i+1 == len(source)-1 || d.is(i+2, "ER")) || d.at(i+1) == 'M' {
				i += 2
			} else {
				i++
			}
		case 'N':
			d.add("N", "N")
			if d.at(i+1) == 'N' {
				i += 2
			} else {
				i++
			}
		case 0xd1:
			d.add("N", "N")
			i++
		case 'P':
			if d.at(i+1) == 'H' {
				d.add("F", "F")
				i += 2
			} else {
				d.add("P", "P")
				if d.is(i+1, "P", "B") {
					i += 2
				} else {
					i++
				}
			}
		case 'Q':
			d.add("K", "K")
			if d.at(i+1) == 'Q' {
				i += 2
			} else {
				i++
			}
		case 'R':
			if i == len(source)-1 && !d.slavo && d.is(i-2, "IE") && !d.is(i-4, "ME", "MA") {
				d.add("", "R")
			} else {
				d.add("R", "R")
			}
			if d.at(i+1) == 'R' {
				i += 2
			} else {
				i++
			}
		case 'S':
			i = d.s(i)
		case 'T':
			i = d.t(i)
		case 'V':
			d.add("F", "F")
			if d.at(i+1) == 'V' {
				i += 2
			} else {
				i++
			}
		case 'W':
			i = d.w(i)
		case 'X':
			if !(i == len(source)-1 && (d.is(i-3, "IAU", "EAU") || d.is(i-2, "AU", "OU"))) {
				d.add("KS", "KS")
			}
			if d.is(i+1, "C", "X") {
				i += 2
			} else {
				i++
			}
		case 'Z':
			if d.at(i+1) == 'H' {
				d.add("J", "J")
				i += 2
			} else {
				if d.is(i+1, "ZO", "ZI", "ZA") || d.slavo && i > 0 && d.at(i-1) != 'T' {
					d.add("S", "TS")
				} else {
					d.add("S", "S")
				}
				if d.at(i+1) == 'Z' {
					i += 2
				} else {
					i++
				}
			}
		default:
			i++
		}
	}
	return d, nil
}
