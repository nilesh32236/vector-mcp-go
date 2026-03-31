package util

// ClampInt clamps value into the inclusive [min, max] range.
// ClampInt constrains value to the inclusive range [min, max].
// If min is greater than max, the bounds are swapped before constraining; when value lies outside the range the nearest bound is returned.
func ClampInt(value, min, max int) int {
	if min > max {
		min, max = max, min
	}
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

// ClampInt64 clamps value into the inclusive [min, max] range.
// ClampInt64 clamps value into the inclusive range [min, max].
// If min > max the bounds are swapped before clamping; values below the lower bound
// return the lower bound and values above the upper bound return the upper bound.
func ClampInt64(value, min, max int64) int64 {
	if min > max {
		min, max = max, min
	}
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

// ClampFloat64 clamps value into the inclusive [min, max] range.
// ClampFloat64 clamps value into the inclusive range [min, max].
// If min is greater than max, the bounds are swapped.
// It returns min when value is less than min, max when value is greater than max, or value otherwise.
func ClampFloat64(value, min, max float64) float64 {
	if min > max {
		min, max = max, min
	}
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

// TruncateRuneSafe truncates by rune count, preventing UTF-8 multi-byte corruption.
// TruncateRuneSafe truncates s to at most maxRunes Unicode code points (runes) without splitting multi-byte characters.
// If maxRunes <= 0 or s is empty it returns an empty string; if s already contains maxRunes or fewer runes it returns s unchanged.
func TruncateRuneSafe(s string, maxRunes int) string {
	if maxRunes <= 0 || len(s) == 0 {
		return ""
	}

	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes])
}
