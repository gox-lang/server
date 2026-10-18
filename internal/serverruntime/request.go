package serverruntime

import (
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/gox-lang/gox"
)

const defaultMultipartMemory = 32 << 20

func buildRequest(req *http.Request) gox.Request {
	return buildRequestWithBody(req, gox.NewBody(req.Body, req.Header.Get("Content-Type")))
}

func buildRequestWithBody(req *http.Request, body gox.Body, policies ...*IPResolverPolicy) gox.Request {
	protocol := requestScheme(req, policies...)
	contentType := req.Header.Get("Content-Type")
	if !gox.ResetRuntimeBody(body, req.Body, contentType) {
		body = gox.NewBody(req.Body, contentType)
	}

	form, files := buildFormAndFiles(req, contentType, &body)
	hostname, port := splitRequestHost(req.Host)

	var query gox.Collection[string]
	if req.URL != nil && req.URL.RawQuery != "" {
		query = gox.NewCollectionView[string](req.URL.Query())
	} else {
		query = gox.NewCollectionView[string](nil)
	}

	return gox.NewRequest(gox.RequestInput{
		Method:      req.Method,
		IP:          clientIP(req),
		Secure:      protocol == "https",
		ContentType: contentType,
		URL:         buildURLParts(req, hostname, port, protocol),
		Headers:     gox.NewCollectionView[string](req.Header),
		Cookies:     buildCookies(req),
		Query:       query,
		Form:        form,
		Files:       files,
		Route:       gox.NewRoute(gox.RouteInput{}),
		Body:        body,
	})
}

func buildURLParts(req *http.Request, hostname string, port string, protocol ...string) gox.URL {
	rawQuery := req.URL.RawQuery
	queryString := ""
	if rawQuery != "" {
		queryString = "?" + rawQuery
	}

	var urlScheme string
	if len(protocol) != 0 {
		urlScheme = protocol[0]
	} else {
		urlScheme = scheme(req)
	}
	urlHost := req.Host

	return gox.NewURL(gox.URLInput{
		Scheme:      urlScheme,
		Host:        urlHost,
		Hostname:    hostname,
		Port:        port,
		Path:        req.URL.Path,
		RawPath:     req.URL.EscapedPath(),
		QueryString: queryString,
	})
}

func splitRequestHost(host string) (hostname string, port string) {
	if host == "" {
		return "", ""
	}
	if host[0] == '[' {
		if end := strings.IndexByte(host, ']'); end >= 0 {
			hostname = host[1:end]
			if end+1 < len(host) && host[end+1] == ':' {
				port = host[end+2:]
			}
			return hostname, port
		}
	}
	idx := strings.LastIndexByte(host, ':')
	if idx < 0 || strings.IndexByte(host, ':') != idx {
		return host, ""
	}
	if idx == len(host)-1 {
		return host[:idx], ""
	}
	return host[:idx], host[idx+1:]
}

func buildFormAndFiles(req *http.Request, contentType string, body *gox.Body) (gox.Collection[string], gox.Collection[gox.File]) {
	contentType = strings.ToLower(contentType)
	if strings.Contains(contentType, "multipart/form-data") {
		if err := req.ParseMultipartForm(defaultMultipartMemory); err != nil || req.MultipartForm == nil {
			return gox.NewCollection[string](nil), gox.NewCollection[gox.File](nil)
		}
		return gox.NewCollection[string](req.MultipartForm.Value), buildFiles(req.MultipartForm.File)
	}

	if strings.Contains(contentType, "application/x-www-form-urlencoded") {
		if err := req.ParseForm(); err != nil {
			return gox.NewCollection[string](nil), gox.NewCollection[gox.File](nil)
		}
		return gox.NewCollection[string](req.PostForm), gox.NewCollection[gox.File](nil)
	}

	return gox.NewCollection[string](nil), gox.NewCollection[gox.File](nil)
}

func buildFiles(values map[string][]*multipart.FileHeader) gox.Collection[gox.File] {
	if len(values) == 0 {
		return gox.NewCollection[gox.File](nil)
	}
	out := make(map[string][]gox.File, len(values))
	for key, headers := range values {
		if len(headers) == 0 {
			continue
		}
		files := make([]gox.File, 0, len(headers))
		for _, header := range headers {
			if header == nil {
				continue
			}
			file, err := gox.NewFile(gox.FileConfig{
				Name: header.Filename,
				Type: header.Header.Get("Content-Type"),
				Size: header.Size,
				Open: func() (io.ReadCloser, error) {
					return header.Open()
				},
			})
			if err != nil {
				continue
			}
			files = append(files, file)
		}
		out[key] = files
	}
	return gox.NewCollection[gox.File](out)
}
func buildCookies(req *http.Request) gox.Collection[string] {
	if req == nil || req.Header.Get("Cookie") == "" {
		return gox.NewCollectionView[string](nil)
	}
	cookies := req.Cookies()
	if len(cookies) == 0 {
		return gox.NewCollection[string](nil)
	}

	values := make(map[string][]string, len(cookies))
	for _, cookie := range cookies {
		values[cookie.Name] = append(values[cookie.Name], cookie.Value)
	}

	return gox.NewCollection[string](values)
}

func isSecure(req *http.Request) bool {
	return scheme(req) == "https"
}

func scheme(req *http.Request) string { return requestScheme(req) }

func requestScheme(req *http.Request, policies ...*IPResolverPolicy) string {
	if req.TLS != nil {
		return "https"
	}

	if len(policies) > 0 {
		values := req.Header.Values("X-Forwarded-Proto")
		if len(values) == 1 && values[0] == "https" && policies[0].trustsRequest(req) {
			return "https"
		}
	}

	return "http"
}

func clientIP(req *http.Request) string {
	if req == nil {
		return ""
	}
	remote := req.RemoteAddr
	if remote == "" {
		return ""
	}
	if remote[0] == '[' {
		if end := strings.IndexByte(remote, ']'); end > 0 {
			return remote[1:end]
		}
	}
	idx := strings.LastIndexByte(remote, ':')
	if idx > 0 && strings.IndexByte(remote, ':') == idx {
		return remote[:idx]
	}
	return remote
}
