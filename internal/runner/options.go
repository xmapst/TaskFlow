package runner

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/tidwall/gjson"

	"github.com/xmapst/taskflow/api/iproto"
	"github.com/xmapst/taskflow/internal/storage"
)

// Step options are per-executor settings carried in iproto.SStepOptions as raw
// JSON. Each executor declares the keys it reads and reads only those, so an
// option that belongs to another executor never has to be understood here.

// stepOptions reads the step's options and rejects keys the executor does not
// know: a typo fails the step instead of being silently ignored.
func stepOptions(stg *storage.StepStore, kind string, allowed ...string) (gjson.Result, error) {
	raw, err := stg.Options()
	if err != nil {
		return gjson.Result{}, err
	}
	return parseStepOptions(kind, raw, allowed...)
}

// parseStepOptions is stepOptions without the store, so it can be tested on its
// own.
func parseStepOptions(kind string, raw iproto.SStepOptions, allowed ...string) (gjson.Result, error) {
	opts, err := raw.Object()
	if err != nil {
		return gjson.Result{}, err
	}
	var unknown []string
	opts.ForEach(func(key, _ gjson.Result) bool {
		if !slices.Contains(allowed, key.String()) {
			unknown = append(unknown, key.String())
		}
		return true
	})
	if len(unknown) > 0 {
		slices.Sort(unknown)
		return gjson.Result{}, fmt.Errorf("executor %s does not support option %s; supported: %s",
			kind, quoteAll(unknown), strings.Join(allowed, ", "))
	}
	return opts, nil
}

// optionString reads a plain string option. An absent option is empty.
func optionString(opts gjson.Result, key string) (string, error) {
	value := opts.Get(key)
	if !value.Exists() || value.Type == gjson.Null {
		return "", nil
	}
	if value.Type != gjson.String {
		return "", fmt.Errorf("option %q must be a string, got %s", key, value.Type)
	}
	return strings.TrimSpace(value.String()), nil
}

// optionCommand reads a command line written either as one string ("bash -e")
// or as a list (["bash", "-e"]). The first element is the program.
func optionCommand(opts gjson.Result, key string) ([]string, error) {
	value := opts.Get(key)
	if !value.Exists() || value.Type == gjson.Null {
		return nil, nil
	}
	switch {
	case value.Type == gjson.String:
		return splitCommandLine(value.String())
	case value.IsArray():
		items := value.Array()
		argv := make([]string, 0, len(items))
		for i, item := range items {
			if item.Type != gjson.String {
				return nil, fmt.Errorf("option %q[%d] must be a string, got %s", key, i, item.Type)
			}
			argv = append(argv, item.String())
		}
		return argv, nil
	default:
		return nil, fmt.Errorf("option %q must be a string or a list of strings, got %s", key, value.Type)
	}
}

// splitCommandLine splits a command line into words. Single quotes keep their
// text literally, double quotes allow \" and \\, and a backslash outside quotes
// escapes the next character. No shell runs here: the result is passed to exec
// as argv, so nothing is expanded.
func splitCommandLine(line string) ([]string, error) {
	var (
		args    []string
		current strings.Builder
		quote   rune
		escaped bool
		started bool
	)
	for _, r := range line {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped = false
		case r == '\\' && quote != '\'':
			escaped = true
			started = true
		case quote != 0:
			if r == quote {
				quote = 0
				continue
			}
			current.WriteRune(r)
			started = true
		case r == '\'' || r == '"':
			quote = r
			started = true
		case unicode.IsSpace(r):
			if started {
				args = append(args, current.String())
				current.Reset()
				started = false
			}
		default:
			current.WriteRune(r)
			started = true
		}
	}
	if escaped {
		return nil, errors.New("command line ends with a trailing backslash")
	}
	if quote != 0 {
		return nil, fmt.Errorf("command line has an unterminated %c quote", quote)
	}
	if started {
		args = append(args, current.String())
	}
	return args, nil
}

// quoteAll renders names for an error message.
func quoteAll(names []string) string {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = fmt.Sprintf("%q", name)
	}
	return strings.Join(quoted, ", ")
}
