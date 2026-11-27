package execution

import (
	"fmt"
	"slices"
	"strings"

	"github.com/xmapst/taskflow/api"
	"github.com/xmapst/taskflow/internal/storage/models"
)

// Handler implements task and step HTTP endpoints.
type Handler struct{}

func (h *Handler) convertState(state models.State) api.Code {
	switch state {
	case models.StateStopped:
		return api.CodeSuccess
	case models.StateRunning:
		return api.CodeRunning
	case models.StatePending:
		return api.CodePending
	case models.StatePaused:
		return api.CodePaused
	case models.StateFailed:
		return api.CodeFailed
	case models.StateSkipped:
		return api.CodeSkipped
	default:
		return api.CodeNoData
	}
}

func (h *Handler) generateStateMessage(baseMessage string, groups map[models.State][]string) string {
	var keys []models.State
	for k := range groups {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	var messages []string
	if baseMessage != "" {
		messages = append(messages, baseMessage)
	}
	for _, key := range keys {
		count := len(groups[key])
		messages = append(messages, fmt.Sprintf("%d %s", count, models.StateMap[key]))
	}
	return strings.Join(messages, "; ")
}
