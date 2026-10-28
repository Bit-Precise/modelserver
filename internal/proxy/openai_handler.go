package proxy

import (
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// apiVersionSegment matches a trailing API version path segment such as "v1"
// or "v4" in an upstream base URL.
var apiVersionSegment = regexp.MustCompile(`^v\d+$`)

func directorSetOpenAIUpstream(req *http.Request, baseURL, apiKey string) {
	req.URL.Scheme = "https"
	if baseURL != "" {
		if target, err := url.Parse(baseURL); err == nil {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			if target.Path != "" && target.Path != "/" {
				reqPath := req.URL.Path
				// A base URL that already ends in an API version (OpenAI SDK
				// convention, e.g. https://open.bigmodel.cn/api/paas/v4) replaces
				// the incoming /v1 prefix instead of nesting it under the base.
				if apiVersionSegment.MatchString(path.Base(target.Path)) &&
					(reqPath == "/v1" || strings.HasPrefix(reqPath, "/v1/")) {
					reqPath = strings.TrimPrefix(reqPath, "/v1")
				}
				req.URL.Path = path.Join(target.Path, reqPath)
			}
		}
	}
	req.Host = req.URL.Host

	// Set all required headers from scratch — do not inherit from client.
	req.Header.Set("Authorization", "Bearer "+apiKey)
}
