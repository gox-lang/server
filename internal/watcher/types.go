package watcher

import "time"

type Op uint8

const (
	OpUnknown Op = iota
	OpCreate
	OpWrite
	OpRemove
	OpRename
	OpChmod
)

func (op Op) String() string {
	switch op {
	case OpCreate:
		return "create"
	case OpWrite:
		return "write"
	case OpRemove:
		return "remove"
	case OpRename:
		return "rename"
	case OpChmod:
		return "chmod"
	default:
		return "unknown"
	}
}

type Event struct {
	Path   string
	Rel    string
	Op     Op
	Exists bool
	IsDir  bool
	Time   time.Time
}

type Handler func(event Event)
