package serverruntime

import (
	"compress/gzip"
	"compress/zlib"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"

	"github.com/gox-lang/gox"
	"github.com/gox-lang/gox/server"
)

const staticIdentityEncoding = "identity"

type StaticPolicy struct {
	rootDir           string
	staticDir         string
	streamMinFileSize int64
	compression       server.CompressionConfig
	headers           []compiledStaticHeaderRule
	ranges            []compiledStaticRangeRule
}

type compiledStaticHeaderRule struct {
	match   string
	rule    preRouteRule
	headers server.Headers
}

type compiledStaticRangeRule struct {
	match        string
	rule         preRouteRule
	enabled      bool
	minFileSize  int64
	maxChunkSize int64
}

type staticRangeSettings struct {
	enabled      bool
	minFileSize  int64
	maxChunkSize int64
}

func NewStaticPolicy(projectRoot string, config server.StaticConfig) (*StaticPolicy, error) {
	staticDir := strings.TrimSpace(config.Path)
	if staticDir == "" {
		staticDir = "public"
	}
	staticDir = strings.Trim(staticDir, "/")
	if staticDir == "" || staticDir == "." {
		staticDir = "public"
	}
	if strings.Contains(staticDir, "..") {
		return nil, newRuntimeConfigError("invalid-static-path", fmt.Sprintf("invalid static path: %s", config.Path), nil)
	}

	policy := &StaticPolicy{
		rootDir:           projectRoot,
		staticDir:         staticDir,
		streamMinFileSize: config.StreamMinFileSize,
		compression:       config.Compression,
		headers:           make([]compiledStaticHeaderRule, 0, len(config.Headers)),
		ranges:            make([]compiledStaticRangeRule, 0, len(config.Range)),
	}
	if policy.streamMinFileSize <= 0 {
		policy.streamMinFileSize = 512 * 1024
	}

	for _, rule := range config.Headers {
		match := strings.TrimSpace(rule.Match)
		if match == "" {
			continue
		}

		expanded, err := expandPreRouteChoices(match)
		if err != nil {
			return nil, newRuntimeConfigError("compile-static-header-match-failed", fmt.Sprintf("compile static header match %q failed", match), err)
		}

		for _, pattern := range expanded {
			compiled, err := compilePreRouteRule(pattern, pattern)
			if err != nil {
				return nil, newRuntimeConfigError("compile-static-header-match-failed", fmt.Sprintf("compile static header match %q failed", match), err)
			}
			policy.headers = append(policy.headers, compiledStaticHeaderRule{
				match:   pattern,
				rule:    compiled,
				headers: cloneHeaders(rule.Headers),
			})
		}
	}

	for _, rule := range config.Range {
		match := strings.TrimSpace(rule.Match)
		if match == "" {
			continue
		}

		expanded, err := expandPreRouteChoices(match)
		if err != nil {
			return nil, newRuntimeConfigError("compile-static-range-match-failed", fmt.Sprintf("compile static range match %q failed", match), err)
		}

		for _, pattern := range expanded {
			compiled, err := compilePreRouteRule(pattern, pattern)
			if err != nil {
				return nil, newRuntimeConfigError("compile-static-range-match-failed", fmt.Sprintf("compile static range match %q failed", match), err)
			}
			policy.ranges = append(policy.ranges, compiledStaticRangeRule{
				match:        pattern,
				rule:         compiled,
				enabled:      rule.Enabled,
				minFileSize:  rule.MinFileSize,
				maxChunkSize: rule.MaxChunkSize,
			})
		}
	}

	return policy, nil
}

func (r *Runtime) static(ctx *gox.Context, req *http.Request) bool {
	state := r.stateFor(ctx)
	policy := state.Static
	if policy == nil {
		return false
	}

	if !ctx.Request.Route.IsStatic() {
		return false
	}

	routePath := ctx.Request.Route.Target()
	if routePath == "" {
		routePath = ctx.Request.URL.Path()
	}
	matchPath := routePath
	if ctx.Request.Route.IsClientAsset() {
		matchPath = ctx.Request.Route.Pattern()
		if matchPath == "" {
			matchPath = ctx.Request.Route.Original()
		}
		if matchPath == "" {
			matchPath = ctx.Request.URL.Path()
		}
	}

	absPath, ok := policy.resolveRouteFilePath(ctx.Request.Route, routePath)
	if !ok {
		r.renderHTTPStatusError(ctx, req, nil, http.StatusNotFound, "page not found")
		return true
	}

	info, err := os.Stat(absPath)
	if err != nil || info.IsDir() {
		if r.staticCache != nil {
			r.staticCache.MarkDeleted(matchPath)
		}
		r.renderHTTPStatusError(ctx, req, nil, http.StatusNotFound, "page not found")
		return true
	}

	hasRange := strings.TrimSpace(req.Header.Get("Range")) != ""
	encoding := staticIdentityEncoding
	level := 0
	if !hasRange {
		encoding = selectStaticEncoding(policy.compression, req.Header.Get("Accept-Encoding"))
		level = staticCompressionLevel(policy.compression, encoding)
	}
	etag := buildStaticETag(info, encoding, level)
	lastModified := info.ModTime().UTC().Truncate(time.Second)

	contentType := staticContentType(absPath)
	policy.writeHeaders(ctx, matchPath)
	if cacheControl := clientStaticCacheControl(state.ClientAssets, ctx.Request.Route); cacheControl != "" {
		ctx.Response.Headers.Set("Cache-Control", cacheControl)
	}
	ctx.Response.Headers.Set("Content-Type", contentType)
	ctx.Response.Headers.Set("ETag", etag)
	ctx.Response.Headers.Set("Last-Modified", lastModified.Format(http.TimeFormat))
	ctx.Response.Headers.Set("Accept-Ranges", "bytes")
	ctx.Response.Headers.Set("Vary", "Accept-Encoding")

	if r.staticCache != nil {
		r.staticCache.SetSource(matchPath, info, buildStaticETag(info, staticIdentityEncoding, 0), contentType)
	}

	if staticNotModified(req, etag, lastModified) {
		writeResponseHeader(ctx, http.StatusNotModified)
		return true
	}

	servePath := absPath
	serveInfo := info
	if encoding != staticIdentityEncoding {
		var artifact string
		var artifactInfo os.FileInfo
		var err error

		if ctx.Request.Route.IsClientAsset() {
			artifact, artifactInfo, err = resolveClientCompressedArtifact(absPath, encoding, level)
		} else {
			artifact, artifactInfo, err = r.ensureStaticCompressedArtifact(matchPath, absPath, info, encoding, level)
		}

		if err == nil {
			servePath = artifact
			serveInfo = artifactInfo
			ctx.Response.Headers.Set("Content-Encoding", encoding)
		} else {
			encoding = staticIdentityEncoding
			level = 0
			etag = buildStaticETag(info, encoding, level)
			ctx.Response.Headers.Set("ETag", etag)
		}
	}

	if hasRange && staticIfRangeAllows(req, etag, lastModified) {
		rangeSettings := policy.matchRange(matchPath)
		if rangeSettings.enabled {
			return serveStaticRange(ctx, req, servePath, serveInfo, rangeSettings)
		}
	}

	return serveStaticWhole(ctx, req, servePath, serveInfo, policy.streamMinFileSize)
}

func (p *StaticPolicy) resolveRouteFilePath(route gox.Route, routePath string) (string, bool) {
	if route.IsClientAsset() {
		return cleanBuildClientPath(p.rootDir, routePath)
	}
	return p.resolveFilePath(routePath)
}

func (p *StaticPolicy) resolveFilePath(routePath string) (string, bool) {
	routePath = normalizePreRoutePath(routePath)
	if routePath == "" || routePath == "/" {
		return "", false
	}

	rel := strings.TrimPrefix(routePath, "/")
	cleanRel := filepath.Clean(filepath.FromSlash(rel))
	if cleanRel == "." || cleanRel == "" || strings.HasPrefix(cleanRel, "..") || filepath.IsAbs(cleanRel) {
		return "", false
	}

	base := filepath.Join(p.rootDir, filepath.FromSlash(p.staticDir))
	full := filepath.Join(base, cleanRel)

	cleanBase, err := filepath.Abs(base)
	if err != nil {
		return "", false
	}
	cleanFull, err := filepath.Abs(full)
	if err != nil {
		return "", false
	}

	if cleanFull != cleanBase && !strings.HasPrefix(cleanFull, cleanBase+string(os.PathSeparator)) {
		return "", false
	}

	return cleanFull, true
}

func (p *StaticPolicy) writeHeaders(ctx *gox.Context, routePath string) {
	path := normalizePreRoutePath(routePath)
	partCount := countPreRouteParts(path)

	for i := range p.headers {
		rule := &p.headers[i]
		if partCount < rule.rule.minParts {
			continue
		}
		if rule.rule.maxParts >= 0 && partCount > rule.rule.maxParts {
			continue
		}
		if _, ok := rule.rule.match(path, partCount); !ok {
			continue
		}
		for k, v := range rule.headers {
			ctx.Response.Headers.Set(k, v)
		}
		return
	}
}

func (p *StaticPolicy) matchRange(routePath string) staticRangeSettings {
	if len(p.ranges) == 0 {
		return staticRangeSettings{enabled: true}
	}

	path := normalizePreRoutePath(routePath)
	partCount := countPreRouteParts(path)

	for i := range p.ranges {
		rule := &p.ranges[i]
		if partCount < rule.rule.minParts {
			continue
		}
		if rule.rule.maxParts >= 0 && partCount > rule.rule.maxParts {
			continue
		}
		if _, ok := rule.rule.match(path, partCount); !ok {
			continue
		}
		return staticRangeSettings{
			enabled:      rule.enabled,
			minFileSize:  rule.minFileSize,
			maxChunkSize: rule.maxChunkSize,
		}
	}

	return staticRangeSettings{}
}

func cloneHeaders(headers server.Headers) server.Headers {
	if len(headers) == 0 {
		return nil
	}
	out := make(server.Headers, len(headers))
	for k, v := range headers {
		out[k] = v
	}
	return out
}

func staticContentType(filePath string) string {
	if ct := mime.TypeByExtension(filepath.Ext(filePath)); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

func staticNotModified(req *http.Request, etag string, lastModified time.Time) bool {
	if inm := strings.TrimSpace(req.Header.Get("If-None-Match")); inm != "" {
		for _, item := range strings.Split(inm, ",") {
			item = strings.TrimSpace(item)
			if item == "*" || item == etag || strings.TrimPrefix(item, "W/") == etag {
				return true
			}
		}
	}

	if ims := strings.TrimSpace(req.Header.Get("If-Modified-Since")); ims != "" {
		if t, err := time.Parse(http.TimeFormat, ims); err == nil {
			if !lastModified.After(t) {
				return true
			}
		}
	}

	return false
}

func buildStaticETag(info os.FileInfo, encoding string, level int) string {
	base := fmt.Sprintf("%d:%d:%s:%d", info.ModTime().UnixNano(), info.Size(), encoding, level)
	sum := sha1.Sum([]byte(base))
	return `"` + hex.EncodeToString(sum[:]) + `"`
}

func selectStaticEncoding(cfg server.CompressionConfig, accept string) string {
	if !cfg.Enabled {
		return staticIdentityEncoding
	}
	accepted := parseAcceptEncoding(accept)
	best := staticIdentityEncoding
	bestQ := -1.0

	for _, enc := range cfg.OrderPriority {
		canonical := normalizeStaticEncoding(enc)
		if canonical == staticIdentityEncoding || !staticEncodingEnabled(cfg, canonical) || !staticEncodingImplemented(canonical) {
			continue
		}

		q, ok := accepted.get(canonical)
		if !ok {
			if wildcardQ, hasWildcard := accepted.get("*"); hasWildcard {
				q = wildcardQ
				ok = true
			}
		}
		if !ok || q <= 0 {
			continue
		}
		if q > bestQ {
			bestQ = q
			best = canonical
		}
	}

	return best
}

type acceptedEncodingValue struct {
	q   float64
	set bool
}

type acceptedEncodings struct {
	brotli   acceptedEncodingValue
	gzip     acceptedEncodingValue
	zstd     acceptedEncodingValue
	deflate  acceptedEncodingValue
	wildcard acceptedEncodingValue
}

func (a *acceptedEncodings) put(name string, q float64) {
	value := acceptedEncodingValue{q: q, set: true}
	switch {
	case strings.EqualFold(name, "br"), strings.EqualFold(name, "brotli"):
		a.brotli = value
	case strings.EqualFold(name, "gzip"), strings.EqualFold(name, "gz"):
		a.gzip = value
	case strings.EqualFold(name, "zstd"), strings.EqualFold(name, "zst"):
		a.zstd = value
	case strings.EqualFold(name, "deflate"):
		a.deflate = value
	case name == "*":
		a.wildcard = value
	}
}

func (a acceptedEncodings) get(name string) (float64, bool) {
	var value acceptedEncodingValue
	switch name {
	case "br":
		value = a.brotli
	case "gzip":
		value = a.gzip
	case "zstd":
		value = a.zstd
	case "deflate":
		value = a.deflate
	case "*":
		value = a.wildcard
	}
	return value.q, value.set
}

func parseAcceptEncoding(header string) acceptedEncodings {
	var result acceptedEncodings

	for len(header) > 0 {
		comma := strings.IndexByte(header, ',')

		part := header
		if comma >= 0 {
			part = header[:comma]
			header = header[comma+1:]
		} else {
			header = ""
		}

		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		enc := part
		q := 1.0

		if semi := strings.IndexByte(part, ';'); semi >= 0 {
			enc = strings.TrimSpace(part[:semi])
			params := part[semi+1:]

			for len(params) > 0 {
				nextSemi := strings.IndexByte(params, ';')

				param := params
				if nextSemi >= 0 {
					param = params[:nextSemi]
					params = params[nextSemi+1:]
				} else {
					params = ""
				}

				param = strings.TrimSpace(param)
				if strings.HasPrefix(param, "q=") {
					if value, err := strconv.ParseFloat(param[2:], 64); err == nil {
						q = value
					}
				}
			}
		}

		result.put(enc, q)
	}

	return result
}

func normalizeStaticEncoding(enc string) string {
	switch strings.ToLower(strings.TrimSpace(enc)) {
	case "br", "brotli":
		return "br"
	case "gzip", "gz":
		return "gzip"
	case "zstd", "zst":
		return "zstd"
	case "deflate":
		return "deflate"
	case "identity":
		return staticIdentityEncoding
	default:
		return strings.ToLower(strings.TrimSpace(enc))
	}
}

func staticEncodingEnabled(cfg server.CompressionConfig, enc string) bool {
	switch enc {
	case "br":
		return cfg.Brotli.Enabled
	case "gzip":
		return cfg.Gzip.Enabled
	case "zstd":
		return cfg.Zstd.Enabled
	case "deflate":
		return cfg.Deflate.Enabled
	default:
		return false
	}
}

func staticEncodingImplemented(enc string) bool {
	switch enc {
	case "br", "gzip", "zstd", "deflate":
		return true
	default:
		return false
	}
}

func staticCompressionLevel(cfg server.CompressionConfig, enc string) int {
	switch enc {
	case "br":
		return cfg.Brotli.Level
	case "gzip":
		return cfg.Gzip.Level
	case "zstd":
		return cfg.Zstd.Level
	case "deflate":
		return cfg.Deflate.Level
	default:
		return 0
	}
}

func (r *Runtime) ensureStaticCompressedArtifact(routePath, sourcePath string, info os.FileInfo, encoding string, level int) (string, os.FileInfo, error) {
	if r.staticCache == nil || r.staticBuildGroup == nil {
		return "", nil, newRuntimeConfigError("static-cache-not-initialized", "static cache not initialized", nil)
	}

	if artifactPath, artifactInfo, ok := r.staticCache.GetEncoding(routePath, info, encoding, level); ok {
		return artifactPath, artifactInfo, nil
	}

	modTime := strconv.FormatInt(info.ModTime().UnixNano(), 10)
	artifactPath := buildStaticArtifactPath(r.ProjectRoot, routePath, modTime, filepath.Base(sourcePath), encoding, level)

	if artifactInfo, err := os.Stat(artifactPath); err == nil && !artifactInfo.IsDir() {
		r.staticCache.SetEncoding(routePath, info, encoding, level, artifactPath, artifactInfo, buildStaticETag(info, encoding, level))
		return artifactPath, artifactInfo, nil
	}

	_, err := r.staticBuildGroup.Do(artifactPath, func() (any, error) {
		if artifactInfo, err := os.Stat(artifactPath); err == nil && !artifactInfo.IsDir() {
			return nil, nil
		}

		if err := os.MkdirAll(filepath.Dir(artifactPath), 0o755); err != nil {
			return nil, err
		}

		tmpPath := artifactPath + ".tmp"
		_ = os.Remove(tmpPath)

		src, err := os.Open(sourcePath)
		if err != nil {
			return nil, err
		}
		defer src.Close()

		dst, err := os.Create(tmpPath)
		if err != nil {
			return nil, err
		}

		err = compressStatic(encoding, dst, src, level)
		closeErr := dst.Close()
		if err != nil {
			_ = os.Remove(tmpPath)
			return nil, err
		}
		if closeErr != nil {
			_ = os.Remove(tmpPath)
			return nil, closeErr
		}

		if err := os.Rename(tmpPath, artifactPath); err != nil {
			_ = os.Remove(tmpPath)
			return nil, err
		}

		return nil, nil
	})
	if err != nil {
		return "", nil, err
	}

	artifactInfo, err := os.Stat(artifactPath)
	if err != nil {
		return "", nil, err
	}
	r.staticCache.SetEncoding(routePath, info, encoding, level, artifactPath, artifactInfo, buildStaticETag(info, encoding, level))
	return artifactPath, artifactInfo, nil
}

func buildStaticArtifactPath(rootDir, routePath, modTime, filename, encoding string, level int) string {
	clean := strings.TrimPrefix(normalizePreRoutePath(routePath), "/")
	clean = filepath.Clean(filepath.FromSlash(clean))
	return filepath.Join(rootDir, ".build", "cache", "statics", clean, modTime, filename+".l"+strconv.Itoa(level)+"."+encoding)
}

func compressStatic(encoding string, dst io.Writer, src io.Reader, level int) error {
	switch encoding {
	case "br", "brotli":
		if level <= 0 {
			level = 6
		}
		writer := brotli.NewWriterLevel(dst, level)
		_, err := io.Copy(writer, src)
		if closeErr := writer.Close(); err == nil {
			err = closeErr
		}
		return err

	case "gzip":
		if level <= 0 {
			level = gzip.DefaultCompression
		}
		writer, err := gzip.NewWriterLevel(dst, level)
		if err != nil {
			return err
		}
		_, err = io.Copy(writer, src)
		if closeErr := writer.Close(); err == nil {
			err = closeErr
		}
		return err

	case "zstd":
		var opts []zstd.EOption
		if level > 0 {
			opts = append(opts, zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(level)))
		}
		writer, err := zstd.NewWriter(dst, opts...)
		if err != nil {
			return err
		}
		_, err = io.Copy(writer, src)
		if closeErr := writer.Close(); err == nil {
			err = closeErr
		}
		return err

	case "deflate":
		if level <= 0 {
			level = zlib.DefaultCompression
		}
		writer, err := zlib.NewWriterLevel(dst, level)
		if err != nil {
			return err
		}
		_, err = io.Copy(writer, src)
		if closeErr := writer.Close(); err == nil {
			err = closeErr
		}
		return err

	default:
		return newRuntimeConfigError("unsupported-static-encoding", fmt.Sprintf("unsupported static encoding: %s", encoding), nil)
	}
}

type staticSingleflightGroup struct {
	mu sync.Mutex
	m  map[string]*staticSingleflightCall
}

type staticSingleflightCall struct {
	wg  sync.WaitGroup
	val any
	err error
}

func newStaticSingleflightGroup() *staticSingleflightGroup {
	return &staticSingleflightGroup{m: make(map[string]*staticSingleflightCall)}
}

func (g *staticSingleflightGroup) Do(key string, fn func() (any, error)) (any, error) {
	g.mu.Lock()
	if call, ok := g.m[key]; ok {
		g.mu.Unlock()
		call.wg.Wait()
		return call.val, call.err
	}

	call := &staticSingleflightCall{}
	call.wg.Add(1)
	g.m[key] = call
	g.mu.Unlock()

	call.val, call.err = fn()
	call.wg.Done()

	g.mu.Lock()
	delete(g.m, key)
	g.mu.Unlock()

	return call.val, call.err
}

type staticRangeSpec struct {
	start   int64
	end     int64
	length  int64
	suffix  bool
	openEnd bool
}

func serveStaticWhole(ctx *gox.Context, req *http.Request, servePath string, serveInfo os.FileInfo, streamMinFileSize int64) bool {
	ctx.Response.Headers.Set("Content-Length", strconv.FormatInt(serveInfo.Size(), 10))
	writeResponseHeader(ctx, http.StatusOK)

	if req.Method == http.MethodHead {
		return true
	}

	file, err := os.Open(servePath)
	if err != nil {
		return true
	}
	defer file.Close()

	if serveInfo.Size() < streamMinFileSize {
		data, err := io.ReadAll(file)
		if err == nil {
			_, _ = ctx.Writer.Write(data)
		}
		return true
	}

	_, _ = io.Copy(ctx.Writer, file)
	return true
}

func serveStaticRange(ctx *gox.Context, req *http.Request, servePath string, serveInfo os.FileInfo, settings staticRangeSettings) bool {
	fileSize := serveInfo.Size()
	rangeSpec, ok := parseStaticRange(req.Header.Get("Range"), fileSize)
	if !ok {
		ctx.Response.Headers.Set("Content-Range", "bytes */"+strconv.FormatInt(fileSize, 10))
		ctx.Response.Headers.Set("Content-Length", "0")
		writeResponseHeader(ctx, http.StatusRequestedRangeNotSatisfiable)
		return true
	}

	rangeSpec = capStaticRange(rangeSpec, fileSize, settings)

	ctx.Response.Headers.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", rangeSpec.start, rangeSpec.end, fileSize))
	ctx.Response.Headers.Set("Content-Length", strconv.FormatInt(rangeSpec.length, 10))
	writeResponseHeader(ctx, http.StatusPartialContent)

	if req.Method == http.MethodHead {
		return true
	}

	file, err := os.Open(servePath)
	if err != nil {
		return true
	}
	defer file.Close()

	if _, err := file.Seek(rangeSpec.start, io.SeekStart); err != nil {
		return true
	}

	_, _ = io.CopyN(ctx.Writer, file, rangeSpec.length)
	return true
}

func parseStaticRange(header string, fileSize int64) (staticRangeSpec, bool) {
	header = strings.TrimSpace(header)
	if fileSize < 0 || !strings.HasPrefix(header, "bytes=") {
		return staticRangeSpec{}, false
	}

	spec := strings.TrimSpace(strings.TrimPrefix(header, "bytes="))
	if spec == "" || strings.Contains(spec, ",") {
		return staticRangeSpec{}, false
	}

	parts := strings.SplitN(spec, "-", 2)
	if len(parts) != 2 {
		return staticRangeSpec{}, false
	}

	startText := strings.TrimSpace(parts[0])
	endText := strings.TrimSpace(parts[1])
	if startText == "" && endText == "" {
		return staticRangeSpec{}, false
	}

	var start int64
	var end int64

	if startText == "" {
		suffixLength, err := strconv.ParseInt(endText, 10, 64)
		if err != nil || suffixLength <= 0 {
			return staticRangeSpec{}, false
		}
		if suffixLength > fileSize {
			suffixLength = fileSize
		}
		start = fileSize - suffixLength
		end = fileSize - 1
	} else {
		parsedStart, err := strconv.ParseInt(startText, 10, 64)
		if err != nil || parsedStart < 0 {
			return staticRangeSpec{}, false
		}
		start = parsedStart

		if endText == "" {
			end = fileSize - 1
		} else {
			parsedEnd, err := strconv.ParseInt(endText, 10, 64)
			if err != nil || parsedEnd < 0 {
				return staticRangeSpec{}, false
			}
			end = parsedEnd
		}
	}

	if fileSize == 0 || start >= fileSize || end < start {
		return staticRangeSpec{}, false
	}
	if end >= fileSize {
		end = fileSize - 1
	}

	return staticRangeSpec{start: start, end: end, length: end - start + 1, suffix: startText == "", openEnd: endText == ""}, true
}

func capStaticRange(spec staticRangeSpec, fileSize int64, settings staticRangeSettings) staticRangeSpec {
	if fileSize <= 0 || settings.maxChunkSize <= 0 {
		return spec
	}
	if settings.minFileSize > 0 && fileSize < settings.minFileSize {
		return spec
	}
	if spec.length <= settings.maxChunkSize {
		return spec
	}

	if spec.suffix {
		spec.start = spec.end - settings.maxChunkSize + 1
		if spec.start < 0 {
			spec.start = 0
		}
	} else {
		spec.end = spec.start + settings.maxChunkSize - 1
		if spec.end >= fileSize {
			spec.end = fileSize - 1
		}
	}

	spec.length = spec.end - spec.start + 1
	return spec
}

func staticIfRangeAllows(req *http.Request, etag string, lastModified time.Time) bool {
	ifRange := strings.TrimSpace(req.Header.Get("If-Range"))
	if ifRange == "" {
		return true
	}

	if strings.HasPrefix(ifRange, "\"") || strings.HasPrefix(ifRange, "W/\"") {
		return ifRange == etag || strings.TrimPrefix(ifRange, "W/") == etag
	}

	if t, err := time.Parse(http.TimeFormat, ifRange); err == nil {
		return !lastModified.After(t)
	}

	return false
}

func clientStaticCacheControl(policy *ClientAssetPolicy, route gox.Route) string {
	if !route.IsClientAsset() {
		return ""
	}
	url := route.Pattern()
	if url == "" {
		url = route.Original()
	}
	meta, ok := policy.Meta(url)
	if !ok {
		return "no-cache"
	}
	return meta.CacheControl()
}
