package scaffold

import (
	"fmt"
	"os"
	"strings"
	"unicode"
)

func ValidateProjectName(name string) error {
	if name == "" {
		return fmt.Errorf("project name is required")
	}

	if name == "." || name == ".." {
		return fmt.Errorf("project name must not be %q", name)
	}

	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("project name must not contain path separators")
	}

	if strings.Contains(name, "..") {
		return fmt.Errorf("project name must not contain '..'")
	}

	if strings.ContainsAny(name, " \t\n\r") {
		return fmt.Errorf("project name must not contain whitespace")
	}

	if strings.HasPrefix(name, ".") {
		return fmt.Errorf("project name must not start with '.'")
	}

	if strings.HasSuffix(name, ".") {
		return fmt.Errorf("project name must not end with '.'")
	}

	reserved := map[string]struct{}{
		".build":     {},
		"app":        {},
		"pages":      {},
		"components": {},
		"client":     {},
		"tests":      {},
		"public":     {},

		"con":  {},
		"prn":  {},
		"aux":  {},
		"nul":  {},
		"com1": {},
		"com2": {},
		"com3": {},
		"com4": {},
		"com5": {},
		"com6": {},
		"com7": {},
		"com8": {},
		"com9": {},
		"lpt1": {},
		"lpt2": {},
		"lpt3": {},
		"lpt4": {},
		"lpt5": {},
		"lpt6": {},
		"lpt7": {},
		"lpt8": {},
		"lpt9": {},
	}

	if _, ok := reserved[strings.ToLower(name)]; ok {
		return fmt.Errorf("project name %q is reserved", name)
	}

	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			continue
		}

		return fmt.Errorf("invalid character %q in project name", r)
	}

	return nil
}

func EnsureTargetDoesNotExist(path string) error {
	_, err := os.Lstat(path)
	if err == nil {
		return fmt.Errorf("target directory already exists: %s", path)
	}

	if os.IsNotExist(err) {
		return nil
	}

	return err
}
