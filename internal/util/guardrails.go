// Package util provides common utility functions used throughout the codebase.
package util

// ClampInt clamps value into the inclusive [min, max] range.
// If min is greater than max, the bounds are swapped.
func ClampInt(value, low, high int) int {
	if low > high {
		low, high = high, low
	}
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

// ClampInt64 clamps value into the inclusive [min, max] range.
// If min is greater than max, the bounds are swapped.
func ClampInt64(value, low, high int64) int64 {
	if low > high {
		low, high = high, low
	}
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

// ClampFloat64 clamps value into the inclusive [min, max] range.
// If min is greater than max, the bounds are swapped.
func ClampFloat64(value, low, high float64) float64 {
	if low > high {
		low, high = high, low
	}
	if value < low {
		return low
	}
	if value > high {
		return high
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
