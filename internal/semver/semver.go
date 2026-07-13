// Package semver implements the minimal semantic-version handling ubiqo
// needs for artifact releases (vMAJOR.MINOR.PATCH, no prerelease/build).
package semver

import (
	"fmt"
	"regexp"
	"strconv"
)

var re = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)

type Version struct{ Major, Minor, Patch int }

func (v Version) String() string { return fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch) }

func Parse(s string) (Version, error) {
	m := re.FindStringSubmatch(s)
	if m == nil {
		return Version{}, fmt.Errorf("invalid version %q (want vMAJOR.MINOR.PATCH)", s)
	}
	maj, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	pat, _ := strconv.Atoi(m[3])
	return Version{maj, min, pat}, nil
}

// Compare returns -1, 0, or 1.
func Compare(a, b Version) int {
	for _, d := range [3]int{a.Major - b.Major, a.Minor - b.Minor, a.Patch - b.Patch} {
		if d < 0 {
			return -1
		}
		if d > 0 {
			return 1
		}
	}
	return 0
}

// Bump returns the next version for kind "major", "minor", or "patch".
func Bump(cur Version, kind string) (Version, error) {
	switch kind {
	case "major":
		return Version{cur.Major + 1, 0, 0}, nil
	case "minor":
		return Version{cur.Major, cur.Minor + 1, 0}, nil
	case "patch":
		return Version{cur.Major, cur.Minor, cur.Patch + 1}, nil
	}
	return Version{}, fmt.Errorf("invalid bump kind %q (want major|minor|patch)", kind)
}

// Latest returns the highest version among tags of the form
// artifacts/<name>/vX.Y.Z, given just the vX.Y.Z suffixes. ok is false if
// none parse.
func Latest(versions []string) (Version, bool) {
	var best Version
	ok := false
	for _, s := range versions {
		v, err := Parse(s)
		if err != nil {
			continue
		}
		if !ok || Compare(v, best) > 0 {
			best, ok = v, true
		}
	}
	return best, ok
}
