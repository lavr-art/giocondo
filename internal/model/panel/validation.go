package panel

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"strings"
)

// ErrInvalid is returned for invalid input.
var ErrInvalid = errors.New("invalid input")

// DomainPattern matches valid domain names.
var DomainPattern = regexp.MustCompile(
	`^(?i:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)(?:\.(?i:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?))*$`,
)

// ValidHostname reports whether s is a valid hostname.
func ValidHostname(s string) bool {
	return net.ParseIP(s) != nil || (len(s) <= 253 && DomainPattern.MatchString(s))
}

// ParseIPv4 parses an IPv4 address.
func ParseIPv4(raw string) (string, error) {
	ip, err := netip.ParseAddr(strings.TrimSpace(raw))
	if err != nil || !ip.Is4() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLoopback() {
		return "", invalidAddress("a unicast non-loopback IPv4 address is required")
	}

	return ip.String(), nil
}

func invalidAddress(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}
