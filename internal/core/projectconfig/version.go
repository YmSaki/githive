// Package projectconfig owns the repository config.json version contract.
// An absent ref is a bootstrap state; an existing malformed config is not.
package projectconfig

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/ymsaki/githive/internal/core/event"
)

// SchemaVersion is the highest repository fold schema this build supports.
const SchemaVersion = 1

// InvalidError means an existing meta/config has no valid positive integer
// schema_version. Ref identifies the authoritative or remote-tracking input.
type InvalidError struct{ Ref, Reason string }

func (e *InvalidError) Error() string {
	return fmt.Sprintf("projectconfig: invalid config at %s: %s", e.Ref, e.Reason)
}

// UnsupportedError preserves the existing typed future-version failure.
// Found is populated for machine-sized versions; RawFound represents a valid
// positive integer too large for int without truncation or enormous expansion.
type UnsupportedError struct {
	Found, Supported int
	RawFound         string
}

func (e *UnsupportedError) Error() string {
	found := strconv.Itoa(e.Found)
	if e.RawFound != "" {
		found = e.RawFound
	}
	return fmt.Sprintf("projectconfig: schema_version %s is newer than the %d this build supports", found, e.Supported)
}

// Version decodes exactly the positive-integer schema_version contract. JSON
// integral forms such as 1.0 and 1e0 are accepted; strings and fractional values
// are not coerced. It never expands an unbounded decimal exponent.
func Version(ref string, raw []byte) (int, error) {
	invalid := func(reason string) (int, error) { return 0, &InvalidError{Ref: ref, Reason: reason} }
	value, err := event.DecodeGeneric(raw)
	if err != nil {
		return invalid("config.json is not valid JSON: " + err.Error())
	}
	object, ok := value.(map[string]any)
	if !ok {
		return invalid("config.json must be an object")
	}
	number, ok := object["schema_version"].(interface {
		String() string
		Int64() (int64, error)
	})
	if !ok {
		return invalid("schema_version must be a positive integer number")
	}
	text := number.String()
	if strings.HasPrefix(text, "-") {
		return invalid("schema_version must be positive")
	}
	mantissa, expText := text, "0"
	if i := strings.IndexAny(text, "eE"); i >= 0 {
		mantissa, expText = text[:i], text[i+1:]
	}
	fraction := 0
	if i := strings.IndexByte(mantissa, '.'); i >= 0 {
		fraction = len(mantissa) - i - 1
		mantissa = mantissa[:i] + mantissa[i+1:]
	}
	trimmed := strings.TrimRight(mantissa, "0")
	zeros := len(mantissa) - len(trimmed)
	digits := strings.TrimLeft(trimmed, "0")
	if digits == "" {
		return invalid("schema_version must be positive")
	}
	negative := strings.HasPrefix(expText, "-")
	exponentDigits := strings.TrimLeft(strings.TrimLeft(expText, "+-"), "0")
	if len(exponentDigits) > 18 {
		if negative {
			return invalid("schema_version must be an integer")
		}
		return 0, &UnsupportedError{Supported: SchemaVersion, RawFound: text}
	}
	exponent := int64(0)
	if exponentDigits != "" {
		exponent, _ = strconv.ParseInt(exponentDigits, 10, 64)
	} // <=18 decimal digits always fits
	if negative {
		exponent = -exponent
	}
	scale := exponent - int64(fraction) + int64(zeros)
	if scale < 0 {
		return invalid("schema_version must be an integer")
	}
	maxDigits := len(strconv.Itoa(int(^uint(0) >> 1)))
	if len(digits) > maxDigits || scale > int64(maxDigits-len(digits)) {
		return 0, &UnsupportedError{Supported: SchemaVersion, RawFound: text}
	}
	exact := digits + strings.Repeat("0", int(scale))
	version, err := strconv.Atoi(exact)
	if err != nil {
		return 0, &UnsupportedError{Supported: SchemaVersion, RawFound: text}
	}
	return version, nil
}

// Check requires a valid config at ref and rejects unsupported future versions.
func Check(ref string, raw []byte) error {
	version, err := Version(ref, raw)
	if err != nil {
		return err
	}
	if version > SchemaVersion {
		return &UnsupportedError{Found: version, Supported: SchemaVersion}
	}
	return nil
}
