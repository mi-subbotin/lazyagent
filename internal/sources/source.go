package sources

import (
	"context"

	"github.com/mi-subbotin/lazyagent/internal/model"
)

// Source is the contract every tool adapter implements. A Source returns a
// flat list of Items; grouping by Origin/Kind/Scope happens in the TUI layer.
//
// projectDir is the absolute path of the cwd-project root (or empty if the
// user did not invoke lazyagent inside a project). Adapters MUST NOT return
// project-local items when projectDir is empty.
type Source interface {
	Name() string
	List(ctx context.Context, projectDir string) ([]model.Item, error)
}
