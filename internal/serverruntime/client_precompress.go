package serverruntime

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/gox-lang/server/internal/logs"

	"github.com/gox-lang/gox/server"
)

func precompressClientAssets(projectRoot string, cfg server.CompressionConfig, policy *ClientAssetPolicy) {
	if strings.TrimSpace(projectRoot) == "" || !cfg.Enabled || policy == nil {
		return
	}

	jobs := collectClientPrecompressionJobs(projectRoot, cfg, policy)
	if len(jobs) == 0 {
		return
	}

	workers := runtime.NumCPU()
	if workers < 1 {
		workers = 1
	}
	if workers > len(jobs) {
		workers = len(jobs)
	}
	if workers > 4 {
		workers = 4
	}

	jobCh := make(chan clientPrecompressionJob)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobCh {
				if err := writeClientCompressedArtifact(job); err != nil {
					logs.Warm("client precompression failed: %s %s %s", job.sourcePath, job.encoding, err)
				}
			}
		}()
	}

	for _, job := range jobs {
		jobCh <- job
	}
	close(jobCh)
	wg.Wait()
}

type clientPrecompressionJob struct {
	sourcePath string
	artifact   string
	encoding   string
	level      int
}

func collectClientPrecompressionJobs(projectRoot string, cfg server.CompressionConfig, policy *ClientAssetPolicy) []clientPrecompressionJob {
	seen := make(map[string]struct{})
	jobs := make([]clientPrecompressionJob, 0)

	for _, meta := range policy.allMetas() {
		if meta.IsDev() || strings.TrimSpace(meta.File) == "" {
			continue
		}
		sourcePath, ok := cleanBuildClientPath(projectRoot, meta.File)
		if !ok {
			continue
		}
		info, err := os.Stat(sourcePath)
		if err != nil || info.IsDir() {
			continue
		}
		for _, configured := range cfg.OrderPriority {
			encoding := normalizeStaticEncoding(configured)
			if encoding == staticIdentityEncoding || !staticEncodingEnabled(cfg, encoding) || !staticEncodingImplemented(encoding) {
				continue
			}
			level := staticCompressionLevel(cfg, encoding)
			artifact := buildClientCompressedArtifactPath(sourcePath, encoding, level)
			key := sourcePath + "\x00" + artifact
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			jobs = append(jobs, clientPrecompressionJob{sourcePath: sourcePath, artifact: artifact, encoding: encoding, level: level})
		}
	}
	return jobs
}

func writeClientCompressedArtifact(job clientPrecompressionJob) error {
	if strings.TrimSpace(job.sourcePath) == "" || strings.TrimSpace(job.artifact) == "" || job.encoding == staticIdentityEncoding {
		return nil
	}

	sourceInfo, err := os.Stat(job.sourcePath)
	if err != nil || sourceInfo.IsDir() {
		return err
	}
	if artifactInfo, err := os.Stat(job.artifact); err == nil && !artifactInfo.IsDir() && !artifactInfo.ModTime().Before(sourceInfo.ModTime()) {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(job.artifact), 0o755); err != nil {
		return err
	}

	tmpPath := job.artifact + ".tmp"
	_ = os.Remove(tmpPath)

	src, err := os.Open(job.sourcePath)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	compressErr := compressStatic(job.encoding, dst, src, job.level)
	closeErr := dst.Close()
	if compressErr != nil {
		_ = os.Remove(tmpPath)
		return compressErr
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return closeErr
	}
	if err := os.Rename(tmpPath, job.artifact); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return os.Chtimes(job.artifact, sourceInfo.ModTime(), sourceInfo.ModTime())
}

func buildClientCompressedArtifactPath(sourcePath, encoding string, level int) string {
	return sourcePath + ".l" + strconv.Itoa(level) + "." + normalizeStaticEncoding(encoding)
}

func resolveClientCompressedArtifact(sourcePath, encoding string, level int) (string, os.FileInfo, error) {
	artifactPath := buildClientCompressedArtifactPath(sourcePath, encoding, level)
	info, err := os.Stat(artifactPath)
	if err != nil || info.IsDir() {
		return "", nil, os.ErrNotExist
	}
	return artifactPath, info, nil
}
