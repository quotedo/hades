package config

import "fmt"

// PortRange is an inclusive range of TCP ports hades may allocate from.
type PortRange struct{ Lo, Hi int }

// ParsePortRange parses "LO-HI", e.g. "4100-4999".
func ParsePortRange(s string) (PortRange, error) {
	var r PortRange
	var rest string
	n, err := fmt.Sscanf(s, "%d-%d%s", &r.Lo, &r.Hi, &rest)
	if n != 2 {
		return PortRange{}, fmt.Errorf("invalid port range %q: want LO-HI, e.g. 4100-4999", s)
	}
	_ = err // n == 2 with a trailing-input error is the success path
	if r.Lo < 1 || r.Hi > 65535 || r.Lo > r.Hi {
		return PortRange{}, fmt.Errorf("invalid port range %q: need 1 <= lo <= hi <= 65535", s)
	}
	return r, nil
}
