package release

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Build describes a release to put together (`kicker release manifest`, run in CI).
type Build struct {
	Version    string // semver, "v" allowed
	MinVersion string
	Notes      string
	Date       time.Time
	BaseURL    string   // where the files are downloaded from, e.g. …/releases/download/v1.2.3
	Out        string   // folder for release.json and the firmware files
	Servers    []string // server binaries, named kicker-<os>-<arch>
	Firmware   string   // firmware/modules: each module's built .uf2 files are copied to Out
}

var serverName = regexp.MustCompile(`^kicker-([a-z0-9]+)-([a-z0-9]+)$`)

// Manifest writes the firmware files to b.Out and returns release.json's content.
func (b Build) Manifest() ([]byte, Manifest, error) {
	v, err := ParseVersion(b.Version)
	if err != nil {
		return nil, Manifest{}, fmt.Errorf("version %q: %w", b.Version, err)
	}
	base := strings.TrimRight(b.BaseURL, "/")
	if base == "" {
		return nil, Manifest{}, errors.New("no download URL")
	}
	m := Manifest{Format: Format, Version: v.String(), Date: b.Date.UTC().Truncate(time.Second), Notes: b.Notes,
		MinVersion: strings.TrimPrefix(b.MinVersion, "v"), Server: []Server{}, Firmware: []Firmware{}}
	for _, path := range b.Servers {
		name := filepath.Base(path)
		parts := serverName.FindStringSubmatch(name)
		if parts == nil {
			return nil, Manifest{}, fmt.Errorf("%s: server binaries are named kicker-<os>-<arch>", name)
		}
		a, err := asset(path, base)
		if err != nil {
			return nil, Manifest{}, err
		}
		m.Server = append(m.Server, Server{Asset: a, OS: parts[1], Arch: parts[2]})
	}
	if b.Firmware != "" {
		mods, err := FirmwareModules(b.Firmware)
		if err != nil {
			return nil, Manifest{}, err
		}
		for _, mod := range mods {
			for _, env := range mod.Envs {
				src := filepath.Join(mod.Dir, ".pio", "build", env.Name, "firmware.uf2")
				name := fmt.Sprintf("%s-%s-%s.uf2", mod.FW(), mod.Version, env.Board)
				dst := filepath.Join(b.Out, name)
				if err := copyFile(src, dst); err != nil {
					return nil, Manifest{}, fmt.Errorf("%s (env %s): %w – build it first (pio run -d %s)", mod.MF, env.Name, err, mod.Dir)
				}
				a, err := asset(dst, base)
				if err != nil {
					return nil, Manifest{}, err
				}
				m.Firmware = append(m.Firmware, Firmware{Asset: a, MF: mod.MF, HW: env.Board, FW: mod.FW(), FWVer: mod.Version, Upd: "uf2"})
			}
		}
	}
	if err := m.Validate(); err != nil {
		return nil, Manifest{}, err
	}
	data, err := marshal(m)
	return data, m, err
}

func asset(path, base string) (Asset, error) {
	f, err := os.Open(path)
	if err != nil {
		return Asset{}, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return Asset{}, err
	}
	name := filepath.Base(path)
	return Asset{Name: name, URL: base + "/" + name, SHA256: hex.EncodeToString(h.Sum(nil)), Size: n}, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	return errors.Join(err, out.Close())
}

// --- firmware modules ---------------------------------------------------------------------

// Module is a firmware module project (firmware/modules/<name>/platformio.ini): its manifest id
// and version from the [kicker] section, and one build per board ([env:…]).
type Module struct {
	Dir     string
	MF      string // kicker.manifest
	Version string // kicker.version
	Envs    []Env
}

// Env is a PlatformIO environment: the board is what the module reports as "hw".
type Env struct{ Name, Board string }

// FW is the firmware name the module announces: the manifest id with '/' → '-'.
func (m Module) FW() string { return strings.ReplaceAll(m.MF, "/", "-") }

// FirmwareModules reads every module project in dir.
func FirmwareModules(dir string) ([]Module, error) {
	ini, err := filepath.Glob(filepath.Join(dir, "*", "platformio.ini"))
	if err != nil {
		return nil, err
	}
	sort.Strings(ini)
	var mods []Module
	for _, path := range ini {
		sections, err := readINI(path)
		if err != nil {
			return nil, err
		}
		k := sections["kicker"]
		m := Module{Dir: filepath.Dir(path), MF: k["manifest"], Version: k["version"]}
		if m.MF == "" || !strings.Contains(m.MF, "/") {
			return nil, fmt.Errorf("%s: [kicker] manifest = vendor/name is missing", path)
		}
		if _, err := ParseVersion(m.Version); err != nil {
			return nil, fmt.Errorf("%s: [kicker] version %q: %w", path, m.Version, err)
		}
		var names []string
		for s := range sections {
			if strings.HasPrefix(s, "env:") {
				names = append(names, s)
			}
		}
		sort.Strings(names)
		for _, s := range names {
			board := sections[s]["board"]
			if board == "" {
				return nil, fmt.Errorf("%s: [%s] has no board", path, s)
			}
			m.Envs = append(m.Envs, Env{Name: strings.TrimPrefix(s, "env:"), Board: board})
		}
		if len(m.Envs) == 0 {
			return nil, fmt.Errorf("%s: no [env:…] sections", path)
		}
		mods = append(mods, m)
	}
	return mods, nil
}

// readINI reads the plain key = value pairs of a PlatformIO ini file (continuation lines and
// interpolation are not needed for the keys read here).
func readINI(path string) (map[string]map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]map[string]string{}
	section := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == ' ' || line[0] == '\t' { // continuation of a multi-line value
			continue
		}
		line = strings.TrimSpace(line)
		if line == "" || line[0] == ';' || line[0] == '#' {
			continue
		}
		if line[0] == '[' && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			if out[section] == nil {
				out[section] = map[string]string{}
			}
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok && section != "" {
			if i := strings.Index(v, " ;"); i >= 0 {
				v = v[:i]
			}
			out[section][strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out, sc.Err()
}
