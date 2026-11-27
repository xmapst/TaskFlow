package runner

import (
	"fmt"
	"sort"
	"strings"

	"github.com/xmapst/taskflow/internal/storage"
)

var runners = make(map[string]Factory)

// Factory builds the runner for one step. Everything a step configures lives
// in the step itself: the executor is chosen by kind and its private settings
// come from storage.StepStore.Options, so a new executor feature adds no
// factory argument.
type Factory func(stg *storage.StepStore, workspace, scriptDir string) (IRunner, error)

// Register makes a runner available under kind.
func Register(kind string, factory Factory) {
	runners[strings.ToLower(kind)] = factory
}

// ListAvailable returns the registered executor kinds in sorted order.
func ListAvailable() []string {
	kinds := make([]string, 0, len(runners))
	for kind := range runners {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return kinds
}

// Get returns the executor registered for kind. kind names an executor only;
// the interpreter comes from the step's shell field.
func Get(kind string) (Factory, error) {
	factory, ok := runners[strings.ToLower(kind)]
	if !ok {
		return nil, fmt.Errorf("unknown executor %q, want one of %s", kind, strings.Join(ListAvailable(), ", "))
	}
	return factory, nil
}

func init() {
	Register("xexec", newXExecRunner)
	Register("mkdir", newMkdirRunner)
	Register("touch", newTouchRunner)
	Register("container", newContainerRunner)
	Register("approval", newApprovalRunner)
}
