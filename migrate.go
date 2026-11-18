package jseek

import "sync"

// Format version migration framework. Every sealed format (index JSEEKIX,
// store JSEEKST, NDJSON JSEEKNJ, field index JSEEKFI) carries a version word.
// This framework centralizes how versions are validated and upgraded so that
// future format changes have a single, well-defined path:
//
//   - checkVersion rejects anything newer than the build understands with
//     ErrUnsupportedVersion (rather than a generic bad-index error), and
//     rejects anything older than the minimum supported version.
//   - RegisterMigration installs an upgrade hook from one version to the next.
//     Migrate chains hooks to bring an old image up to the current version.
//
// Today all formats are at version 1 with no migrations registered; the
// scaffolding exists so that a v2 format can ship with an explicit, tested
// upgrade path instead of an ad-hoc break. Migrations operate on whole images
// and are intentionally not in the hot read path.

// FormatKind identifies a sealed format for version/migration purposes.
type FormatKind string

const (
	FormatIndex    FormatKind = "JSEEKIX"
	FormatStore    FormatKind = "JSEEKST"
	FormatNDJSON   FormatKind = "JSEEKNJ"
	FormatFieldIdx FormatKind = "JSEEKFI"
)

// currentVersions is the version this build writes/reads for each format.
var currentVersions = map[FormatKind]uint32{
	FormatIndex:    1,
	FormatStore:    1,
	FormatNDJSON:   1,
	FormatFieldIdx: 1,
}

// minVersions is the oldest version this build can still read (via migration).
var minVersions = map[FormatKind]uint32{
	FormatIndex:    1,
	FormatStore:    1,
	FormatNDJSON:   1,
	FormatFieldIdx: 1,
}

// CurrentVersion returns the version this build uses for a format.
func CurrentVersion(kind FormatKind) uint32 { return currentVersions[kind] }

// checkVersion validates a format version word. It returns nil for the current
// version, ErrUnsupportedVersion for a newer (unknown) version, and
// ErrUnsupportedVersion for a version older than the minimum supported.
func checkVersion(kind FormatKind, version uint32) error {
	cur := currentVersions[kind]
	if version == cur {
		return nil
	}
	if version > cur {
		return ErrUnsupportedVersion
	}
	if version < minVersions[kind] {
		return ErrUnsupportedVersion
	}
	return nil // older but migratable; caller runs Migrate
}

// Migration upgrades a whole sealed image from one version to the next. It must
// return a complete, valid image at version from+1.
type Migration func(image []byte) ([]byte, error)

var (
	migMu      sync.RWMutex
	migrations = map[FormatKind]map[uint32]Migration{}
)

// RegisterMigration installs an upgrade hook for kind from version `from` to
// `from+1`. It is intended to be called at init time by format-evolution code.
// Registering a duplicate (kind, from) panics to catch wiring mistakes early.
func RegisterMigration(kind FormatKind, from uint32, m Migration) {
	migMu.Lock()
	defer migMu.Unlock()
	if migrations[kind] == nil {
		migrations[kind] = map[uint32]Migration{}
	}
	if _, exists := migrations[kind][from]; exists {
		panic("jseek: duplicate migration registered")
	}
	migrations[kind][from] = m
}

// Migrate upgrades an image from its current version to the build's current
// version by chaining registered hooks. If the image is already current it is
// returned unchanged. If any step lacks a hook, ErrUnsupportedVersion is
// returned. The version word is read from the image header (bytes 8:12, the
// common layout shared by all sealed formats).
func Migrate(kind FormatKind, image []byte) ([]byte, error) {
	if len(image) < 12 {
		return nil, ErrBadIndex
	}
	version := leUint32(image[8:12])
	cur := currentVersions[kind]
	if version == cur {
		return image, nil
	}
	if version > cur || version < minVersions[kind] {
		return nil, ErrUnsupportedVersion
	}
	migMu.RLock()
	defer migMu.RUnlock()
	for v := version; v < cur; v++ {
		m, ok := migrations[kind][v]
		if !ok {
			return nil, ErrUnsupportedVersion
		}
		next, err := m(image)
		if err != nil {
			return nil, err
		}
		image = next
		if len(image) < 12 {
			return nil, ErrBadIndex
		}
		if got := leUint32(image[8:12]); got != v+1 {
			return nil, ErrBadIndex // migration did not bump the version
		}
	}
	return image, nil
}

func leUint32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}
