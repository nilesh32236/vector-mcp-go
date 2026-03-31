package util

// ClampInt clamps value into the inclusive [min, max] range.
// If min is greater than max, the bounds are swapped.
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
// If min is greater than max, the bounds are swapped.
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
// If min is greater than max, the bounds are swapped.
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
// For maxRunes <= 0, an empty string is returned.
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
