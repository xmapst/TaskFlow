package runner

import (
	"cmp"
	"encoding/base64"
	"encoding/json/v2"
	"maps"
	"net/url"
	"slices"
	"strings"
	"sync"

	"github.com/xmapst/taskflow/internal/storage"
	"github.com/xmapst/taskflow/internal/storage/models"
)

const (
	// maskText replaces every secret found in the output.
	maskText = "***"
	// addMaskCommand is the GitHub Actions workflow command a script prints to
	// register a value as secret at run time: `::add-mask::<value>`.
	addMaskCommand = "::add-mask::"
)

// commandDataUnescaper undoes the escaping Actions applies to command data,
// which is what lets a multi-line value travel on one output line.
var commandDataUnescaper = strings.NewReplacer("%0D", "\r", "%0A", "\n", "%25", "%")

// secretMasker redacts secrets from step output the way the GitHub Actions
// runner does:
//
//   - a secret is registered in several forms (raw, JSON-, URL- and
//     base64-encoded), because scripts echo them re-encoded;
//   - a multi-line secret is registered line by line, because output is
//     masked line by line;
//   - the longest value wins, so a secret that contains another secret is
//     hidden completely;
//   - scripts can register more values while running through ::add-mask::.
//
// It is safe for concurrent use: stdout and stderr are masked concurrently.
type secretMasker struct {
	mu       sync.RWMutex
	values   map[string]struct{}
	replacer *strings.Replacer
}

func newSecretMasker() *secretMasker {
	return &secretMasker{values: make(map[string]struct{})}
}

// Add registers a secret. Blank values are ignored: they would match
// everywhere and wreck the output.
func (m *secretMasker) Add(secret string) {
	forms := secretForms(secret)
	if len(forms) == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	changed := false
	for _, f := range forms {
		if _, ok := m.values[f]; !ok {
			m.values[f] = struct{}{}
			changed = true
		}
	}
	if !changed {
		return
	}
	// strings.Replacer prefers the earlier pair when several match at the same
	// position, so order by length to let the longest secret win.
	ordered := slices.SortedFunc(maps.Keys(m.values), func(a, b string) int {
		return cmp.Or(cmp.Compare(len(b), len(a)), cmp.Compare(a, b))
	})
	pairs := make([]string, 0, len(ordered)*2)
	for _, v := range ordered {
		pairs = append(pairs, v, maskText)
	}
	m.replacer = strings.NewReplacer(pairs...)
}

// newStepMasker seeds a masker with the step's secret env: the task-wide env
// (including what earlier steps published) and the step's own.
func newStepMasker(stg *storage.StepStore) *secretMasker {
	m := newSecretMasker()
	m.AddEnvs(stg.GlobalEnv().List())
	m.AddEnvs(stg.Env().List())
	return m
}

// AddEnvs registers the values of env variables flagged as secret.
func (m *secretMasker) AddEnvs(envs models.SEnvs) {
	for _, env := range envs {
		if env != nil && env.Secret {
			m.Add(env.Value)
		}
	}
}

// Mask replaces every registered secret in s.
func (m *secretMasker) Mask(s string) string {
	m.mu.RLock()
	r := m.replacer
	m.mu.RUnlock()
	if r == nil {
		return s
	}
	return r.Replace(s)
}

// Line prepares one output line for the log. A ::add-mask:: command registers
// its value and is swallowed (ok is false), so the value never reaches the
// log; any other line comes back masked.
func (m *secretMasker) Line(line string) (masked string, ok bool) {
	if data, found := strings.CutPrefix(strings.TrimLeft(line, " \t"), addMaskCommand); found {
		m.Add(commandDataUnescaper.Replace(strings.TrimRight(data, "\r\n")))
		return "", false
	}
	return m.Mask(line), true
}

// secretForms expands a secret into every form worth masking.
func secretForms(secret string) []string {
	bases := []string{secret, strings.TrimSpace(secret)}
	for line := range strings.SplitSeq(secret, "\n") {
		bases = append(bases, strings.TrimSpace(line))
	}
	var forms []string
	for _, b := range bases {
		if strings.TrimSpace(b) == "" {
			continue
		}
		forms = append(forms, b, jsonEscape(b), url.QueryEscape(b), url.PathEscape(b),
			base64.StdEncoding.EncodeToString([]byte(b)))
	}
	return slices.Compact(slices.Sorted(slices.Values(forms)))
}

// jsonEscape returns s as it appears inside a JSON string literal.
func jsonEscape(s string) string {
	b, err := json.Marshal(s)
	if err != nil || len(b) < 2 {
		return s
	}
	return string(b[1 : len(b)-1])
}
