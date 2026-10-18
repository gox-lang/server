package toolchain

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/gox-lang/server/internal/logs"
)

func Download(url string, destination string) error {
	tmp := destination + ".tmp"

	file, err := os.Create(tmp)
	if err != nil {
		return err
	}

	defer func() {
		_ = file.Close()
	}()

	client := http.Client{
		Timeout: 0,
	}

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}

	req.Header.Set("User-Agent", "gox-toolchain-installer")

	res, err := client.Do(req)
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}

	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		_ = os.Remove(tmp)
		return fmt.Errorf("failed to download %s: %s", url, res.Status)
	}

	if res.ContentLength > 0 {
		logs.Info("download size: %d bytes", res.ContentLength)
	} else {
		logs.Info("download size: unknown")
	}

	progress := newProgressReader(res.Body, "downloading", res.ContentLength)

	written, err := io.Copy(file, progress)
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}

	if err := file.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}

	now := time.Now()
	_ = os.Chtimes(tmp, now, now)

	if err := os.Rename(tmp, destination); err != nil {
		return err
	}

	logs.Info("download complete: %d bytes", written)

	return nil
}
