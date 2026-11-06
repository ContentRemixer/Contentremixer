package scrape

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
)

// robotsCache enforces robots.txt (design: "respect robots/ToS"). It fetches
// and caches /robots.txt per origin and matches Disallow rules for the
// scraper's User-Agent. Enforcement is best-effort on fetch failure: a robots
// file that cannot be retrieved (network error / 5xx) does NOT block the scrape
// — only an explicit Disallow does. A 404 robots.txt means "allow all".
type robotsCache struct {
	fetch    *fetcher
	disabled bool

	mu    sync.Mutex
	rules map[string]*robotsRules // keyed by scheme://host
}

func newRobotsCache(f *fetcher, disabled bool) *robotsCache {
	return &robotsCache{fetch: f, disabled: disabled, rules: map[string]*robotsRules{}}
}

// allowed returns nil if the userAgent may fetch rawURL, or an error if
// robots.txt disallows it. Disabled caches always allow.
func (c *robotsCache) allowed(ctx context.Context, rawURL, userAgent string) error {
	if c == nil || c.disabled {
		return nil
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return fmt.Errorf("scrape: robots: invalid url %q", rawURL)
	}
	origin := u.Scheme + "://" + u.Host
	rules := c.rulesFor(ctx, origin)
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	if !rules.allows(userAgent, path) {
		return fmt.Errorf("scrape: robots.txt for %s disallows %q for user-agent %q", origin, path, userAgent)
	}
	return nil
}

func (c *robotsCache) rulesFor(ctx context.Context, origin string) *robotsRules {
	c.mu.Lock()
	if r, ok := c.rules[origin]; ok {
		c.mu.Unlock()
		return r
	}
	c.mu.Unlock()

	// Fetch outside the lock. On any failure, cache an allow-all so we neither
	// re-fetch on every call nor hard-fail the scrape on a transient robots
	// error.
	r := allowAll()
	body, _, err := c.fetch.get(ctx, origin+"/robots.txt")
	if err == nil {
		r = parseRobots(string(body))
	}
	c.mu.Lock()
	c.rules[origin] = r
	c.mu.Unlock()
	return r
}

// robotsRules holds the Disallow/Allow paths for each user-agent group plus the
// wildcard (*) group.
type robotsRules struct {
	groups map[string][]robotsRule // lowercased user-agent token → rules
}

type robotsRule struct {
	allow bool
	path  string
}

func allowAll() *robotsRules { return &robotsRules{groups: map[string][]robotsRule{}} }

// parseRobots parses the subset of the robots.txt grammar we honor:
// User-agent groups with Allow/Disallow path rules. Comments and unknown
// directives are ignored.
func parseRobots(body string) *robotsRules {
	r := &robotsRules{groups: map[string][]robotsRule{}}
	var current []string // user-agents the following rules apply to
	sawRuleSinceAgent := false

	for _, raw := range strings.Split(body, "\n") {
		line := raw
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, val, ok := splitDirective(line)
		if !ok {
			continue
		}
		switch key {
		case "user-agent":
			if sawRuleSinceAgent {
				// A new agent block starts after rules were seen.
				current = nil
				sawRuleSinceAgent = false
			}
			current = append(current, strings.ToLower(val))
		case "disallow", "allow":
			if len(current) == 0 {
				current = []string{"*"}
			}
			sawRuleSinceAgent = true
			rule := robotsRule{allow: key == "allow", path: val}
			for _, ua := range current {
				r.groups[ua] = append(r.groups[ua], rule)
			}
		}
	}
	return r
}

func splitDirective(line string) (key, val string, ok bool) {
	i := strings.IndexByte(line, ':')
	if i < 0 {
		return "", "", false
	}
	return strings.ToLower(strings.TrimSpace(line[:i])), strings.TrimSpace(line[i+1:]), true
}

// allows applies the longest-match Allow/Disallow rule for the best-matching
// user-agent group (exact token prefix, else the wildcard group).
func (r *robotsRules) allows(userAgent, path string) bool {
	rules := r.matchGroup(userAgent)
	if len(rules) == 0 {
		return true
	}
	var best robotsRule
	bestLen := -1
	for _, rule := range rules {
		if rule.path == "" {
			// "Disallow:" with empty value means allow all; skip as a match.
			continue
		}
		if robotsPathMatch(rule.path, path) && len(rule.path) > bestLen {
			best, bestLen = rule, len(rule.path)
		}
	}
	if bestLen < 0 {
		return true // no rule matched
	}
	return best.allow
}

// matchGroup picks the rules for the user-agent: a case-insensitive token
// prefix match wins over the wildcard "*" group.
func (r *robotsRules) matchGroup(userAgent string) []robotsRule {
	ua := strings.ToLower(userAgent)
	var best string
	for token := range r.groups {
		if token == "*" {
			continue
		}
		if strings.HasPrefix(ua, token) && len(token) > len(best) {
			best = token
		}
	}
	if best != "" {
		return r.groups[best]
	}
	return r.groups["*"]
}

// robotsPathMatch implements robots.txt path matching with '*' wildcards and a
// trailing '$' anchor; a plain prefix otherwise.
func robotsPathMatch(pattern, path string) bool {
	anchored := strings.HasSuffix(pattern, "$")
	pattern = strings.TrimSuffix(pattern, "$")
	segs := strings.Split(pattern, "*")

	pos := 0
	for i, seg := range segs {
		if seg == "" {
			continue
		}
		idx := strings.Index(path[pos:], seg)
		if idx < 0 {
			return false
		}
		if i == 0 && idx != 0 {
			return false // first literal segment must anchor at the start
		}
		pos += idx + len(seg)
	}
	if anchored {
		return pos == len(path)
	}
	return true
}
