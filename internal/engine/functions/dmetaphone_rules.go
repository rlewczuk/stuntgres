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

// Contextual consonant rules retain REL_18_0 ordering, including the five
// virtual trailing spaces used by French/Spanish end-of-word cases.
func (d *doubleMetaphoneState) c(i int) int {
	if i > 1 && !d.vowel(i-2) && d.is(i-1, "ACH") && d.at(i+2) != 'I' && (d.at(i+2) != 'E' || d.is(i-2, "BACHER", "MACHER")) {
		d.add("K", "K")
		return i + 2
	}
	if i == 0 && d.is(i, "CAESAR") {
		d.add("S", "S")
		return i + 2
	}
	if d.is(i, "CHIA") {
		d.add("K", "K")
		return i + 2
	}
	if d.is(i, "CH") {
		if i > 0 && d.is(i, "CHAE") {
			d.add("K", "X")
			return i + 2
		}
		if i == 0 && (d.is(i+1, "HARAC", "HARIS") || d.is(i+1, "HOR", "HYM", "HIA", "HEM")) && !d.is(0, "CHORE") {
			d.add("K", "K")
			return i + 2
		}
		if d.is(0, "VAN ", "VON ") || d.is(0, "SCH") || d.is(i-2, "ORCHES", "ARCHIT", "ORCHID") || d.is(i+2, "T", "S") || (d.is(i-1, "A", "O", "U", "E") || i == 0) && d.is(i+2, "L", "R", "N", "M", "B", "H", "F", "V", "W", " ") {
			d.add("K", "K")
		} else if i > 0 {
			if d.is(0, "MC") {
				d.add("K", "K")
			} else {
				d.add("X", "K")
			}
		} else {
			d.add("X", "X")
		}
		return i + 2
	}
	if d.is(i, "CZ") && !d.is(i-2, "WICZ") {
		d.add("S", "X")
		return i + 2
	}
	if d.is(i+1, "CIA") {
		d.add("X", "X")
		return i + 3
	}
	if d.is(i, "CC") && !(i == 1 && d.at(0) == 'M') {
		if d.is(i+2, "I", "E", "H") && !d.is(i+2, "HU") {
			if i == 1 && d.at(i-1) == 'A' || d.is(i-1, "UCCEE", "UCCES") {
				d.add("KS", "KS")
			} else {
				d.add("X", "X")
			}
			return i + 3
		}
		d.add("K", "K")
		return i + 2
	}
	if d.is(i, "CK", "CG", "CQ") {
		d.add("K", "K")
		return i + 2
	}
	if d.is(i, "CI", "CE", "CY") {
		if d.is(i, "CIO", "CIE", "CIA") {
			d.add("S", "X")
		} else {
			d.add("S", "S")
		}
		return i + 2
	}
	d.add("K", "K")
	if d.is(i+1, " C", " Q", " G") {
		return i + 3
	}
	if d.is(i+1, "C", "K", "Q") && !d.is(i+1, "CE", "CI") {
		return i + 2
	}
	return i + 1
}

func (d *doubleMetaphoneState) g(i int) int {
	if d.at(i+1) == 'H' {
		if i > 0 && !d.vowel(i-1) {
			d.add("K", "K")
			return i + 2
		}
		if i == 0 {
			if d.at(i+2) == 'I' {
				d.add("J", "J")
			} else {
				d.add("K", "K")
			}
			return i + 2
		}
		if i > 1 && d.is(i-2, "B", "H", "D") || i > 2 && d.is(i-3, "B", "H", "D") || i > 3 && d.is(i-4, "B", "H") {
			return i + 2
		}
		if i > 2 && d.at(i-1) == 'U' && d.is(i-3, "C", "G", "L", "R", "T") {
			d.add("F", "F")
		} else if i > 0 && d.at(i-1) != 'I' {
			d.add("K", "K")
		}
		return i + 2
	}
	if d.at(i+1) == 'N' {
		if i == 1 && d.vowel(0) && !d.slavo {
			d.add("KN", "N")
		} else if !d.is(i+2, "EY") && d.at(i+1) != 'Y' && !d.slavo {
			d.add("N", "KN")
		} else {
			d.add("KN", "KN")
		}
		return i + 2
	}
	if d.is(i+1, "LI") && !d.slavo {
		d.add("KL", "L")
		return i + 2
	}
	if i == 0 && (d.at(i+1) == 'Y' || d.is(i+1, "ES", "EP", "EB", "EL", "EY", "IB", "IL", "IN", "IE", "EI", "ER")) {
		d.add("K", "J")
		return i + 2
	}
	if (d.is(i+1, "ER") || d.at(i+1) == 'Y') && !d.is(0, "DANGER", "RANGER", "MANGER") && !d.is(i-1, "E", "I") && !d.is(i-1, "RGY", "OGY") {
		d.add("K", "J")
		return i + 2
	}
	if d.is(i+1, "E", "I", "Y") || d.is(i-1, "AGGI", "OGGI") {
		if d.is(0, "VAN ", "VON ") || d.is(0, "SCH") || d.is(i+1, "ET") {
			d.add("K", "K")
		} else if d.is(i+1, "IER ") {
			d.add("J", "J")
		} else {
			d.add("J", "K")
		}
		return i + 2
	}
	d.add("K", "K")
	if d.at(i+1) == 'G' {
		return i + 2
	}
	return i + 1
}

func (d *doubleMetaphoneState) j(i int) int {
	if d.is(i, "JOSE") || d.is(0, "SAN ") {
		if i == 0 && d.at(i+4) == ' ' || d.is(0, "SAN ") {
			d.add("H", "H")
		} else {
			d.add("J", "H")
		}
		return i + 1
	}
	if i == 0 && !d.is(i, "JOSE") {
		d.add("J", "A")
	} else if d.vowel(i-1) && !d.slavo && (d.at(i+1) == 'A' || d.at(i+1) == 'O') {
		d.add("J", "H")
	} else if i == len(d.source)-1 {
		d.add("J", "")
	} else if !d.is(i+1, "L", "T", "K", "S", "N", "M", "B", "Z") && !d.is(i-1, "S", "K", "L") {
		d.add("J", "J")
	}
	if d.at(i+1) == 'J' {
		return i + 2
	}
	return i + 1
}

func (d *doubleMetaphoneState) l(i int) int {
	if d.at(i+1) == 'L' {
		last := len(d.source) - 1
		if i == len(d.source)-3 && d.is(i-1, "ILLO", "ILLA", "ALLE") || (d.is(last-1, "AS", "OS") || d.is(last, "A", "O")) && d.is(i-1, "ALLE") {
			d.add("L", "")
			return i + 2
		}
		d.add("L", "L")
		return i + 2
	}
	d.add("L", "L")
	return i + 1
}

func (d *doubleMetaphoneState) s(i int) int {
	if d.is(i-1, "ISL", "YSL") {
		return i + 1
	}
	if i == 0 && d.is(i, "SUGAR") {
		d.add("X", "S")
		return i + 1
	}
	if d.is(i, "SH") {
		if d.is(i+1, "HEIM", "HOEK", "HOLM", "HOLZ") {
			d.add("S", "S")
		} else {
			d.add("X", "X")
		}
		return i + 2
	}
	if d.is(i, "SIO", "SIA") || d.is(i, "SIAN") {
		if !d.slavo {
			d.add("S", "X")
		} else {
			d.add("S", "S")
		}
		return i + 3
	}
	if i == 0 && d.is(i+1, "M", "N", "L", "W") || d.is(i+1, "Z") {
		d.add("S", "X")
		if d.is(i+1, "Z") {
			return i + 2
		}
		return i + 1
	}
	if d.is(i, "SC") {
		if d.at(i+2) == 'H' {
			if d.is(i+3, "OO", "ER", "EN", "UY", "ED", "EM") {
				if d.is(i+3, "ER", "EN") {
					d.add("X", "SK")
				} else {
					d.add("SK", "SK")
				}
			} else if i == 0 && !d.vowel(3) && d.at(3) != 'W' {
				d.add("X", "S")
			} else {
				d.add("X", "X")
			}
			return i + 3
		}
		if d.is(i+2, "I", "E", "Y") {
			d.add("S", "S")
		} else {
			d.add("SK", "SK")
		}
		return i + 3
	}
	if i == len(d.source)-1 && d.is(i-2, "AI", "OI") {
		d.add("", "S")
	} else {
		d.add("S", "S")
	}
	if d.is(i+1, "S", "Z") {
		return i + 2
	}
	return i + 1
}

func (d *doubleMetaphoneState) t(i int) int {
	if d.is(i, "TION") || d.is(i, "TIA", "TCH") {
		d.add("X", "X")
		return i + 3
	}
	if d.is(i, "TH") || d.is(i, "TTH") {
		if d.is(i+2, "OM", "AM") || d.is(0, "VAN ", "VON ") || d.is(0, "SCH") {
			d.add("T", "T")
		} else {
			d.add("0", "T")
		}
		return i + 2
	}
	d.add("T", "T")
	if d.is(i+1, "T", "D") {
		return i + 2
	}
	return i + 1
}

func (d *doubleMetaphoneState) w(i int) int {
	if d.is(i, "WR") {
		d.add("R", "R")
		return i + 2
	}
	if i == 0 && (d.vowel(i+1) || d.is(i, "WH")) {
		if d.vowel(i + 1) {
			d.add("A", "F")
		} else {
			d.add("A", "A")
		}
	}
	if i == len(d.source)-1 && d.vowel(i-1) || d.is(i-1, "EWSKI", "EWSKY", "OWSKI", "OWSKY") || d.is(0, "SCH") {
		d.add("", "F")
		return i + 1
	}
	if d.is(i, "WICZ", "WITZ") {
		d.add("TS", "FX")
		return i + 4
	}
	return i + 1
}
