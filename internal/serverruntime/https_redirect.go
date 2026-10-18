package serverruntime

import (
	"crypto/x509"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Redirects use certificate identities and the public HTTPS port, never proxy
// host headers or the internal listener port.
func httpsRedirectHandler(next http.Handler, cert *x509.Certificate, policy *IPResolverPolicy) http.Handler {
	return httpsRedirectAllowedHandler(next, func(host string) bool { return cert.VerifyHostname(host) == nil }, policy)
}

func httpsRedirectAllowedHandler(next http.Handler, allowed func(string) bool, policy *IPResolverPolicy) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		host, ok := redirectHostname(req.Host)
		if !ok || !allowed(host) {
			http.Error(w, "invalid HTTPS host", http.StatusBadRequest)
			return
		}
		if requestScheme(req, policy) == "https" {
			next.ServeHTTP(w, req)
			return
		}
		authority := host
		if strings.Contains(host, ":") {
			authority = "[" + host + "]"
		}
		target := url.URL{Scheme: "https", Host: authority, Path: req.URL.Path, RawPath: req.URL.RawPath, RawQuery: req.URL.RawQuery, ForceQuery: req.URL.ForceQuery}
		// 308 preserves the method and body when redirecting POST and other methods.
		w.Header().Set("Location", target.String())
		w.WriteHeader(http.StatusPermanentRedirect)
	})
}

func redirectHostname(authority string) (string, bool) {
	if authority == "" || strings.ContainsAny(authority, " /\\?#@\t\r\n") {
		return "", false
	}
	host := authority
	if strings.HasPrefix(authority, "[") {
		end := strings.IndexByte(authority, ']')
		if end < 0 {
			return "", false
		}
		host = authority[1:end]
		if net.ParseIP(host) == nil {
			return "", false
		}
		tail := authority[end+1:]
		if tail != "" && (!strings.HasPrefix(tail, ":") || !validRedirectPort(tail[1:])) {
			return "", false
		}
	} else if strings.Contains(authority, ":") {
		var port string
		var err error
		host, port, err = net.SplitHostPort(authority)
		if err != nil || !validRedirectPort(port) {
			return "", false
		}
	}
	if host == "" {
		return "", false
	}
	return strings.ToLower(host), true
}

func validRedirectPort(value string) bool {
	if value == "" {
		return false
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return false
		}
	}
	n, err := strconv.Atoi(value)
	return err == nil && n > 0 && n <= 65535
}
