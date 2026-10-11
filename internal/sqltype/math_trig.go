package sqltype

import "math"

// The degree scaling and quadrant helpers follow PostgreSQL REL_18_0 float.c:
// https://github.com/postgres/postgres/blob/REL_18_0/src/backend/utils/adt/float.c
// They preserve exact notable angles and the release's subnormal operation order.
//
// PostgreSQL Database Management System
// (formerly known as Postgres, then as Postgres95)
//
// Portions Copyright (c) 1996-2025, PostgreSQL Global Development Group
// Portions Copyright (c) 1994, The Regents of the University of California
//
// Permission to use, copy, modify, and distribute this software and its
// documentation for any purpose, without fee, and without a written agreement
// is hereby granted, provided that the above copyright notice and this paragraph
// and the following two paragraphs appear in all copies.
//
// IN NO EVENT SHALL THE UNIVERSITY OF CALIFORNIA BE LIABLE TO ANY PARTY FOR
// DIRECT, INDIRECT, SPECIAL, INCIDENTAL, OR CONSEQUENTIAL DAMAGES, INCLUDING
// LOST PROFITS, ARISING OUT OF THE USE OF THIS SOFTWARE AND ITS DOCUMENTATION,
// EVEN IF THE UNIVERSITY OF CALIFORNIA HAS BEEN ADVISED OF THE POSSIBILITY OF
// SUCH DAMAGE.
//
// THE UNIVERSITY OF CALIFORNIA SPECIFICALLY DISCLAIMS ANY WARRANTIES, INCLUDING,
// BUT NOT LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
// A PARTICULAR PURPOSE. THE SOFTWARE PROVIDED HEREUNDER IS ON AN "AS IS" BASIS,
// AND THE UNIVERSITY OF CALIFORNIA HAS NO OBLIGATIONS TO PROVIDE MAINTENANCE,
// SUPPORT, UPDATES, ENHANCEMENTS, OR MODIFICATIONS.

var (
	inverseAsinHalf = math.Asin(0.5)
	inverseAcosHalf = math.Acos(0.5)
	inverseAtanOne  = math.Atan(1)
)

func inverseAsindQ1(x float64) float64 {
	if x <= 0.5 {
		return math.Asin(x) / inverseAsinHalf * 30
	}
	return 90 - math.Acos(x)/inverseAcosHalf*60
}

func inverseAcosdQ1(x float64) float64 {
	if x <= 0.5 {
		return 90 - math.Asin(x)/inverseAsinHalf*30
	}
	return math.Acos(x) / inverseAcosHalf * 60
}

// inverseTrig is called only for a validated FLOAT8 MathPlan. Binary arguments
// are y,x, not x,y; using the quotient would lose quadrants and IEEE zero signs.
func inverseTrig(name string, y, x float64) (float64, error) {
	switch name {
	case "acos", "acosd", "asin", "asind":
		// NaN comparisons are false, so NaN remains a successful NaN result.
		if y < -1 || y > 1 {
			return 0, failure("22003", "input is out of range")
		}
	case "atan2", "atan2d":
		if math.IsNaN(y) || math.IsNaN(x) {
			return math.NaN(), nil
		}
	}
	var result float64
	switch name {
	case "acos":
		result = math.Acos(y)
	case "asin":
		result = math.Asin(y)
	case "atan":
		result = math.Atan(y)
	case "atan2":
		result = math.Atan2(y, x)
	case "acosd":
		if y < 0 {
			result = 90 + inverseAsindQ1(-y)
		} else {
			result = inverseAcosdQ1(y)
		}
	case "asind":
		if y < 0 {
			result = -inverseAsindQ1(-y)
		} else {
			result = inverseAsindQ1(y)
		}
	case "atand":
		result = math.Atan(y) / inverseAtanOne * 45
	case "atan2d":
		result = math.Atan2(y, x) / inverseAtanOne * 45
	}
	if math.IsInf(result, 0) {
		return 0, failure("22003", "value out of range: overflow")
	}
	return result, nil
}

// Forward degree constants are evaluated once, independently of inverse scaling.
// Round the conversion factor to binary64 before constant angle products, just
// as runtime FLOAT8 multiplication does. Untyped constant products round only
// after multiplication and would give different normalization denominators.
const directRadiansPerDegree = float64(0.0174532925199432957692)

var (
	directSinThirty = math.Sin(30 * directRadiansPerDegree)
	directCosSixty  = 1 - math.Cos(60*directRadiansPerDegree)
	directTanOne    = directSindQ1(45) / directCosdQ1(45)
	directCotOne    = directCosdQ1(45) / directSindQ1(45)
)

func directSindQ1(x float64) float64 {
	if x > 30 {
		return directCosdQ1(90 - x)
	}
	return math.Sin(float64(x*directRadiansPerDegree)) / directSinThirty / 2
}

func directCosdQ1(x float64) float64 {
	if x > 60 {
		return directSindQ1(90 - x)
	}
	// Explicit rounding preserves the release's subtraction/division order,
	// including tiny inputs; do not contract this into a fused subtraction.
	scaled := float64(float64(1-math.Cos(float64(x*directRadiansPerDegree))) / directCosSixty)
	return 1 - float64(scaled/2)
}

// directTrig is called only for a validated unary FLOAT8 MathPlan. Radian
// evaluation uses Go math rather than host C errno; do not invent range cutoffs.
func directTrig(name string, x float64) (float64, error) {
	if math.IsNaN(x) {
		return math.NaN(), nil
	}
	if math.IsInf(x, 0) {
		return 0, failure("22003", "input is out of range")
	}
	var result float64
	switch name {
	case "cos":
		result = math.Cos(x)
	case "sin":
		result = math.Sin(x)
	case "tan":
		return math.Tan(x), nil
	case "cot":
		return 1 / math.Tan(x), nil
	default:
		// Reduce before conversion so large finite degree arguments retain
		// their remainder. The comparisons deliberately preserve negative zero.
		x = math.Mod(x, 360)
		sign := float64(1)
		if x < 0 {
			x = -x
			if name != "cosd" {
				sign = -sign
			}
		}
		if x > 180 {
			x = 360 - x
			if name != "cosd" {
				sign = -sign
			}
		}
		if x > 90 {
			x = 180 - x
			if name != "sind" {
				sign = -sign
			}
		}
		switch name {
		case "cosd":
			result = sign * directCosdQ1(x)
		case "sind":
			result = sign * directSindQ1(x)
		case "tand":
			result = sign * ((directSindQ1(x) / directCosdQ1(x)) / directTanOne)
		case "cotd":
			result = sign * ((directCosdQ1(x) / directSindQ1(x)) / directCotOne)
		}
		if name == "tand" || name == "cotd" {
			// Degree tangent/cotangent permit infinite poles and normalize
			// every zero result, unlike sine/cosine and radian tangent.
			if result == 0 {
				result = 0
			}
			return result, nil
		}
	}
	if math.IsInf(result, 0) {
		return 0, failure("22003", "value out of range: overflow")
	}
	return result, nil
}
