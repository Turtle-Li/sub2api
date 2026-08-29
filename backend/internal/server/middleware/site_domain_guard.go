package middleware

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"

	"github.com/gin-gonic/gin"
)

// SiteDomainGuard enforces the configured canonical site domain for panel APIs
// and frontend pages while leaving AI gateway routes untouched.
type SiteDomainGuard struct {
	target atomic.Pointer[string]
}

func NewSiteDomainGuard() *SiteDomainGuard {
	return &SiteDomainGuard{}
}

func (g *SiteDomainGuard) Set(domain string) error {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		g.target.Store(nil)
		return nil
	}
	parsed, err := url.Parse("//" + domain)
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
		return fmt.Errorf("invalid site domain")
	}
	target := strings.ToLower(parsed.Host)
	g.target.Store(&target)
	return nil
}

func (g *SiteDomainGuard) RequireHost() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !isPanelAPIPath(c.Request.URL.Path) {
			c.Next()
			return
		}
		target := g.target.Load()
		if target == nil || sameSiteHost(c.Request.Host, *target) {
			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"code":    http.StatusForbidden,
			"message": "request host does not match the configured site domain",
		})
	}
}

func isPanelAPIPath(path string) bool {
	return path == "/api/v1" || strings.HasPrefix(path, "/api/v1/")
}

// RedirectPage redirects a frontend page or asset request to the canonical
// origin. It returns true when it wrote and aborted the response.
func (g *SiteDomainGuard) RedirectPage(c *gin.Context) bool {
	target := g.target.Load()
	if target == nil || sameSiteHost(c.Request.Host, *target) {
		return false
	}
	location := "//" + *target + c.Request.URL.RequestURI()
	c.Redirect(http.StatusTemporaryRedirect, location)
	c.Abort()
	return true
}

func (g *SiteDomainGuard) RedirectHost() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !g.RedirectPage(c) {
			c.Next()
		}
	}
}

func sameSiteHost(requestHost, targetHost string) bool {
	return normalizeDomainHost(requestHost) == normalizeDomainHost(targetHost)
}

func normalizeDomainHost(host string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
}
