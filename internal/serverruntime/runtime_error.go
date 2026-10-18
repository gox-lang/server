package serverruntime

import (
	"github.com/gox-lang/server/internal/goxerrors"

	"github.com/gox-lang/gox"
)

// GoxError is the internal runtime error model. Public SDK access still goes
// through github.com/gox-lang/gox.Error.
type GoxError = goxerrors.GoxError

func newGoxErrorFromCore(core *goxerrors.GoxError) *GoxError {
	if core == nil {
		core = goxerrors.New(goxerrors.TypeRuntime, gox.GoxErrorDispatch, "", nil)
	}
	core.EnsureStatus()
	return core
}

func NewGoxError(stage gox.GoxErrorStage, message string, cause error) *GoxError {
	return newGoxErrorFromCore(goxerrors.NewFromStage(stage, message, cause))
}

func NewGoxPanicError(recovered any) *GoxError {
	return newGoxErrorFromCore(goxerrors.NewPanic(recovered))
}

var _ gox.Error = (*GoxError)(nil)
