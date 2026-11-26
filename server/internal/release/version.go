package release

import (
	"errors"
	"strconv"
	"strings"
)

// Version is a semantic version (semver.org 2.0): major.minor.patch, an optional pre-release
// ("-rc.1") and build metadata ("+abc", ignored when comparing).
type Version struct {
	Major, Minor, Patch int
	Pre                 []string
}

// ErrVersion: not a semantic version.
var ErrVersion = errors.New("not a semantic version (major.minor.patch)")

// ParseVersion parses "1.2.3", "1.2.3-rc.1" or "v1.2.3".
func ParseVersion(s string) (Version, error) {
	s = strings.TrimPrefix(s, "v")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		if !validIdents(s[i+1:], false) {
			return Version{}, ErrVersion
		}
		s = s[:i]
	}
	var v Version
	if i := strings.IndexByte(s, '-'); i >= 0 {
		if !validIdents(s[i+1:], true) {
			return Version{}, ErrVersion
		}
		v.Pre = strings.Split(s[i+1:], ".")
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return Version{}, ErrVersion
	}
	var n [3]int
	for i, p := range parts {
		var ok bool
		if n[i], ok = number(p); !ok {
			return Version{}, ErrVersion
		}
	}
	v.Major, v.Minor, v.Patch = n[0], n[1], n[2]
	return v, nil
}

// number parses a numeric identifier: digits, no leading zero.
func number(s string) (int, bool) {
	if s == "" || (len(s) > 1 && s[0] == '0') {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 || strings.ContainsAny(s, "+-") {
		return 0, false
	}
	return n, true
}

func validIdents(s string, pre bool) bool {
	if s == "" {
		return false
	}
	for _, id := range strings.Split(s, ".") {
		if id == "" {
			return false
		}
		numeric := true
		for _, c := range id {
			switch {
			case c >= '0' && c <= '9':
			case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '-':
				numeric = false
			default:
				return false
			}
		}
		if pre && numeric && len(id) > 1 && id[0] == '0' {
			return false
		}
	}
	return true
}

// Prerelease reports whether v has a pre-release part (e.g. 1.2.0-rc.1).
func (v Version) Prerelease() bool { return len(v.Pre) > 0 }

// String formats v without build metadata.
func (v Version) String() string {
	s := strconv.Itoa(v.Major) + "." + strconv.Itoa(v.Minor) + "." + strconv.Itoa(v.Patch)
	if len(v.Pre) > 0 {
		s += "-" + strings.Join(v.Pre, ".")
	}
	return s
}

// Compare returns -1, 0 or +1 by semver precedence (a release ranks above its pre-releases).
func (v Version) Compare(w Version) int {
	for _, d := range [][2]int{{v.Major, w.Major}, {v.Minor, w.Minor}, {v.Patch, w.Patch}} {
		if d[0] != d[1] {
			return sign(d[0] - d[1])
		}
	}
	switch {
	case len(v.Pre) == 0 && len(w.Pre) == 0:
		return 0
	case len(v.Pre) == 0:
		return 1
	case len(w.Pre) == 0:
		return -1
	}
	for i := 0; i < len(v.Pre) && i < len(w.Pre); i++ {
		a, b := v.Pre[i], w.Pre[i]
		an, aNum := number(a)
		bn, bNum := number(b)
		switch {
		case aNum && bNum:
			if an != bn {
				return sign(an - bn)
			}
		case aNum:
			return -1 // numeric identifiers rank below alphanumeric ones
		case bNum:
			return 1
		case a != b:
			return strings.Compare(a, b)
		}
	}
	return sign(len(v.Pre) - len(w.Pre))
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}

// Newer reports whether version a is newer than b. Unparsable versions ("dev") are never newer,
// and nothing is newer than them – a development build doesn't update itself.
func Newer(a, b string) bool {
	va, err1 := ParseVersion(a)
	vb, err2 := ParseVersion(b)
	return err1 == nil && err2 == nil && va.Compare(vb) > 0
}
