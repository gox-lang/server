package cli

import (
	"fmt"
	"strings"
)

// NormalizeOptions validates POSIX/GNU option spelling and converts long
// options to the form expected by Go's flag package. Short options use one
// dash (-v) and long options use two dashes (--run).
func NormalizeOptions(args []string, shortOptions, longOptions map[string]struct{}) ([]string, error) {
	normalized := append([]string(nil), args...)
	for index, argument := range normalized {
		if argument == "--" {
			break
		}
		if !strings.HasPrefix(argument, "-") || argument == "-" {
			continue
		}
		token := strings.SplitN(argument, "=", 2)[0]
		name := strings.TrimLeft(token, "-")
		if name == "" {
			continue
		}
		_, isShort := shortOptions[name]
		_, isLong := longOptions[name]
		if strings.HasPrefix(argument, "--") {
			if isShort && !isLong {
				return nil, fmt.Errorf("invalid option %s: short option must use one dash (-%s)", token, name)
			}
			if !isLong {
				return nil, fmt.Errorf("unknown option: %s", token)
			}
			normalized[index] = "-" + strings.TrimPrefix(argument, "--")
			continue
		}
		if isLong && !isShort {
			return nil, fmt.Errorf("invalid option %s: long option must use two dashes (--%s)", token, name)
		}
		if !isShort {
			return nil, fmt.Errorf("unknown option: %s", token)
		}
	}
	return normalized, nil
}
