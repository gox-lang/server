package toolchain

import (
	"fmt"
	"os/exec"
)

func ExtractTarXz(archivePath string, destinationDir string) error {
	progress := startProgressSpinner("extracting")
	cmd := exec.Command("tar", "-xJf", archivePath, "-C", destinationDir)
	out, err := cmd.CombinedOutput()
	progress.Finish()
	if err != nil {
		return fmt.Errorf("extract tar.xz failed: %w: %s", err, string(out))
	}
	return nil
}
