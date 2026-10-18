package serverruntime

// IsErrorKind reports whether err is a GoX runtime error with the provided kind.
func IsErrorKind(err error, kind string) bool {
	if err == nil || kind == "" {
		return false
	}
	if ge, ok := err.(*GoxError); ok {
		return ge.Kind == kind
	}
	return false
}
