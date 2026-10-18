package serverruntime

import (
	"fmt"

	"github.com/gox-lang/server/internal/goxerrors"

	"github.com/gox-lang/gox"
)

func newPreRouteError(kind string, message string, pattern string, cause error) *goxerrors.GoxError {
	err := goxerrors.New(goxerrors.TypeHTTP, gox.GoxErrorResolve, message, cause)
	err.Kind = kind
	err.Hint = preRouteErrorHint(kind)
	if pattern != "" {
		err.AddDetail("pattern", pattern)
	}
	return err
}

func newPreRouteErrorf(kind string, pattern string, format string, args ...any) *goxerrors.GoxError {
	return newPreRouteError(kind, fmt.Sprintf(format, args...), pattern, nil)
}

func wrapPreRouteError(kind string, pattern string, cause error) *goxerrors.GoxError {
	message := "invalid preroute pattern"
	if cause != nil && cause.Error() != "" {
		message = cause.Error()
	}
	return newPreRouteError(kind, message, pattern, cause)
}

func preRouteErrorHint(kind string) string {
	switch kind {
	case "route-wildcard-not-final":
		return "Move the wildcard segment to the end of the route pattern."
	case "route-wildcard-repeated":
		return "Use only one wildcard segment in a route pattern."
	case "route-wildcard-after-optional":
		return "Place wildcard segments before optional route segments or remove the optional segment."
	case "route-required-after-optional":
		return "Move required route segments before optional segments."
	case "route-literal-after-optional":
		return "Move literal route segments before optional segments."
	case "invalid-route-param", "invalid-optional-route-param":
		return "Use route params like :id or optional params like :?id."
	case "invalid-route-choice-segment":
		return "Use route choices like (a|b) within a single route segment."
	case "invalid-nested-route-choice-segment":
		return "Nested route choices are not supported. Flatten the choices into separate values."
	case "empty-route-choice-segment":
		return "Add at least one value inside the route choice."
	case "empty-route-choice-value":
		return "Remove empty route choice values or provide a value on both sides of |."
	default:
		return "Check the preroute pattern syntax."
	}
}
