package sqltype

/*
 * ASCII collating-element names adapted from PostgreSQL REL_18_0 regc_locale.c.
 * This Go version only resolves names for syntax/range validation; it does not
 * construct character sets or implement locale-dependent matching.
 *
 * Copyright (c) 1998 by Scriptics Corporation.
 *
 * This software is copyrighted by the Regents of the University of
 * California, Sun Microsystems, Inc., Scriptics Corporation, ActiveState
 * Corporation and other parties. The following terms apply to all files
 * associated with the software unless explicitly disclaimed in
 * individual files.
 *
 * The authors hereby grant permission to use, copy, modify, distribute,
 * and license this software and its documentation for any purpose, provided
 * that existing copyright notices are retained in all copies and that this
 * notice is included verbatim in any distributions. No written agreement,
 * license, or royalty fee is required for any of the authorized uses.
 * Modifications to this software may be copyrighted by their authors
 * and need not follow the licensing terms described here, provided that
 * the new terms are clearly indicated on the first page of each file where
 * they apply.
 *
 * IN NO EVENT SHALL THE AUTHORS OR DISTRIBUTORS BE LIABLE TO ANY PARTY
 * FOR DIRECT, INDIRECT, SPECIAL, INCIDENTAL, OR CONSEQUENTIAL DAMAGES
 * ARISING OUT OF THE USE OF THIS SOFTWARE, ITS DOCUMENTATION, OR ANY
 * DERIVATIVES THEREOF, EVEN IF THE AUTHORS HAVE BEEN ADVISED OF THE
 * POSSIBILITY OF SUCH DAMAGE.
 *
 * THE AUTHORS AND DISTRIBUTORS SPECIFICALLY DISCLAIM ANY WARRANTIES,
 * INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE, AND NON-INFRINGEMENT. THIS SOFTWARE
 * IS PROVIDED ON AN "AS IS" BASIS, AND THE AUTHORS AND DISTRIBUTORS HAVE
 * NO OBLIGATION TO PROVIDE MAINTENANCE, SUPPORT, UPDATES, ENHANCEMENTS, OR
 * MODIFICATIONS.
 *
 * GOVERNMENT USE: If you are acquiring this software on behalf of the
 * U.S. government, the Government shall have only "Restricted Rights"
 * in the software and related documentation as defined in the Federal
 * Acquisition Regulations (FARs) in Clause 52.227.19 (c) (2). If you
 * are acquiring the software on behalf of the Department of Defense, the
 * software shall be classified as "Commercial Computer Software" and the
 * Government shall have only "Restricted Rights" as defined in Clause
 * 252.227-7013 (c) (1) of DFARs. Notwithstanding the foregoing, the
 * authors grant the U.S. Government and others acting in its behalf
 * permission to use and distribute the software in accordance with the
 * terms specified in this license.
 */
import "unicode/utf8"

func jsonpathRegexElement(name string) (uint32, bool) {
	if name == "" {
		return 0, false
	}
	r, n := utf8.DecodeRuneInString(name)
	if n == len(name) {
		return uint32(r), true
	}
	switch name {
	case "NUL":
		return 0, true
	case "SOH":
		return 1, true
	case "STX":
		return 2, true
	case "ETX":
		return 3, true
	case "EOT":
		return 4, true
	case "ENQ":
		return 5, true
	case "ACK":
		return 6, true
	case "BEL", "alert":
		return 7, true
	case "BS", "backspace":
		return 8, true
	case "HT", "tab":
		return 9, true
	case "LF", "newline":
		return 10, true
	case "VT", "vertical-tab":
		return 11, true
	case "FF", "form-feed":
		return 12, true
	case "CR", "carriage-return":
		return 13, true
	case "SO":
		return 14, true
	case "SI":
		return 15, true
	case "DLE":
		return 16, true
	case "DC1":
		return 17, true
	case "DC2":
		return 18, true
	case "DC3":
		return 19, true
	case "DC4":
		return 20, true
	case "NAK":
		return 21, true
	case "SYN":
		return 22, true
	case "ETB":
		return 23, true
	case "CAN":
		return 24, true
	case "EM":
		return 25, true
	case "SUB":
		return 26, true
	case "ESC":
		return 27, true
	case "IS4", "FS":
		return 28, true
	case "IS3", "GS":
		return 29, true
	case "IS2", "RS":
		return 30, true
	case "IS1", "US":
		return 31, true
	case "space":
		return ' ', true
	case "exclamation-mark":
		return '!', true
	case "quotation-mark":
		return '"', true
	case "number-sign":
		return '#', true
	case "dollar-sign":
		return '$', true
	case "percent-sign":
		return '%', true
	case "ampersand":
		return '&', true
	case "apostrophe":
		return '\'', true
	case "left-parenthesis":
		return '(', true
	case "right-parenthesis":
		return ')', true
	case "asterisk":
		return '*', true
	case "plus-sign":
		return '+', true
	case "comma":
		return ',', true
	case "hyphen", "hyphen-minus":
		return '-', true
	case "period", "full-stop":
		return '.', true
	case "slash", "solidus":
		return '/', true
	case "zero":
		return '0', true
	case "one":
		return '1', true
	case "two":
		return '2', true
	case "three":
		return '3', true
	case "four":
		return '4', true
	case "five":
		return '5', true
	case "six":
		return '6', true
	case "seven":
		return '7', true
	case "eight":
		return '8', true
	case "nine":
		return '9', true
	case "colon":
		return ':', true
	case "semicolon":
		return ';', true
	case "less-than-sign":
		return '<', true
	case "equals-sign":
		return '=', true
	case "greater-than-sign":
		return '>', true
	case "question-mark":
		return '?', true
	case "commercial-at":
		return '@', true
	case "left-square-bracket":
		return '[', true
	case "backslash", "reverse-solidus":
		return '\\', true
	case "right-square-bracket":
		return ']', true
	case "circumflex", "circumflex-accent":
		return '^', true
	case "underscore", "low-line":
		return '_', true
	case "grave-accent":
		return '`', true
	case "left-brace", "left-curly-bracket":
		return '{', true
	case "vertical-line":
		return '|', true
	case "right-brace", "right-curly-bracket":
		return '}', true
	case "tilde":
		return '~', true
	case "DEL":
		return 127, true
	}
	return 0, false
}
