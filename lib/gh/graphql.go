// The GraphQL v4 client used for bulk reads: pull requests with nested
// comments, reviews, files, and linked issues come back in a handful of
// requests for the whole repo instead of thousands of REST calls.

package gh

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/katbyte/go-kt/chttp"
	"github.com/katbyte/go-kt/clog"
)

const endpoint = "https://api.github.com/graphql"

// ErrNotFound marks a 404 — a permanently missing thing, not a transient
// failure, so callers can skip retries. Test with errors.Is.
var ErrNotFound = errors.New("not found")

// requestThrottle keeps a gap between GraphQL requests: firing pages back-to-back
// trips GitHub's secondary rate limit (a 403 with a "wait a few minutes" body)
// long before the point budget runs out.
const requestThrottle = 2 * time.Second

// secondaryLimitWait is the backoff when a secondary-limit 403 still gets through.
const secondaryLimitWait = 90 * time.Second

type Client struct {
	token       string
	httpClient  *http.Client
	lastReq     time.Time
	throttleGap time.Duration
}

func NewClient(token string) *Client {
	gap := requestThrottle
	// PRAWN_GH_THROTTLE overrides the gap between GraphQL requests (e.g. "1s",
	// "500ms") for experimenting — going low risks the secondary rate limit's
	// 90s penalty, which quickly costs more than the gap saves.
	if v := os.Getenv("PRAWN_GH_THROTTLE"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d >= 0 {
			gap = d
		} else {
			clog.Log.Warnf("ignoring unparseable PRAWN_GH_THROTTLE %q", v)
		}
	}
	return &Client{token: token, httpClient: chttp.NewHTTPClient("GraphQL"), throttleGap: gap}
}

// repoVars builds the standard owner/name(/cursor) variable map.
func repoVars(owner, name, cursor string) map[string]any {
	v := map[string]any{"owner": owner, "name": name}
	if cursor != "" {
		v["cursor"] = cursor
	}
	return v
}

// searchVars builds the standard search-query(/cursor) variable map.
func searchVars(query, cursor string) map[string]any {
	v := map[string]any{"query": query}
	if cursor != "" {
		v["cursor"] = cursor
	}
	return v
}

type graphqlError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// throttle sleeps to keep at least throttleGap between requests.
func (c *Client) throttle() {
	if !c.lastReq.IsZero() {
		if wait := c.throttleGap - time.Since(c.lastReq); wait > 0 {
			time.Sleep(wait)
		}
	}
	c.lastReq = time.Now()
}

// Do runs a graphql query and decodes the "data" object into out. Requests are
// throttled, and rate-limit rejections are retried after a long backoff. Any
// graphql-level error fails the call.
func (c *Client) Do(query string, variables map[string]any, out any) error {
	return c.do(query, variables, out, false)
}

// DoTolerant is Do for queries whose individual nodes may legitimately fail —
// pullRequest lookups where the number is actually an issue (NOT_FOUND), or
// linked issues in repos the token can't read (FORBIDDEN). Those errors are
// ignored and the partial data decoded — the affected nodes come back null;
// anything else still fails.
func (c *Client) DoTolerant(query string, variables map[string]any, out any) error {
	return c.do(query, variables, out, true)
}

func (c *Client) do(query string, variables map[string]any, out any, tolerant bool) error {
	payload, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return fmt.Errorf("marshalling graphql request: %w", err)
	}

	const maxAttempts = 4
	for attempt := range maxAttempts {
		c.throttle()

		body, status, err := c.post(payload)
		if err != nil {
			return err
		}

		// secondary rate limit: back off hard and retry — the fetch is resumable,
		// but riding through here saves restarting the run
		if status == http.StatusForbidden && strings.Contains(string(body), "secondary rate limit") {
			if attempt == maxAttempts-1 {
				return fmt.Errorf("graphql secondary rate limit persisted after %d waits: %.200s", maxAttempts, string(body))
			}
			wait := secondaryLimitWait
			clog.Log.Warnf("graphql secondary rate limit hit, sleeping %s (attempt %d/%d)", wait, attempt+1, maxAttempts)
			time.Sleep(wait)
			continue
		}
		if status != http.StatusOK {
			return fmt.Errorf("graphql returned %d: %.400s", status, string(body))
		}

		var envelope struct {
			Data   json.RawMessage `json:"data"`
			Errors []graphqlError  `json:"errors"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			return fmt.Errorf("decoding graphql envelope: %w", err)
		}
		if len(envelope.Errors) > 0 {
			// RATE_LIMITED here is the point budget, not the secondary limit
			if envelope.Errors[0].Type == "RATE_LIMITED" && attempt < maxAttempts-1 {
				clog.Log.Warnf("graphql rate limited, sleeping %s (attempt %d/%d)", secondaryLimitWait, attempt+1, maxAttempts)
				time.Sleep(secondaryLimitWait)
				continue
			}
			fatal := envelope.Errors
			if tolerant && envelope.Data != nil {
				fatal = nil
				for _, e := range envelope.Errors {
					if e.Type == "NOT_FOUND" || e.Type == "FORBIDDEN" {
						clog.Log.Debugf("graphql: ignoring %s node error: %s", e.Type, e.Message)
						continue
					}
					fatal = append(fatal, e)
				}
			}
			if len(fatal) > 0 {
				return fmt.Errorf("graphql error (%s): %s", fatal[0].Type, fatal[0].Message)
			}
		}

		if err := json.Unmarshal(envelope.Data, out); err != nil {
			return fmt.Errorf("decoding graphql data: %w", err)
		}
		return nil
	}

	return fmt.Errorf("graphql request did not complete after %d attempts", maxAttempts) // unreachable
}

// post performs one HTTP round trip; the caller interprets the status code.
func (c *Client) post(payload []byte) (body []byte, status int, err error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, fmt.Errorf("building graphql request: %w", err)
	}
	// every query here is a read that travels as a POST, so the shared
	// client's retry-on-5xx is safe to keep
	req = chttp.MarkRetrySafe(req)
	req.Header.Set("Authorization", "bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("graphql request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err = io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("reading graphql response: %w", err)
	}
	return body, resp.StatusCode, nil
}

// ---- shared response shapes ----

type PageInfo struct {
	EndCursor   string `json:"endCursor"`
	HasNextPage bool   `json:"hasNextPage"`
}

type RateLimit struct {
	Cost      int       `json:"cost"`
	Remaining int       `json:"remaining"`
	ResetAt   time.Time `json:"resetAt"`
}

// WaitIfLow sleeps until the rate limit window resets when remaining is nearly spent.
func (r RateLimit) WaitIfLow() {
	if r.Remaining > 0 && r.Remaining < 100 {
		wait := time.Until(r.ResetAt) + 10*time.Second
		if wait > 0 {
			clog.Log.Warnf("graphql rate limit nearly exhausted (%d left), sleeping %s until reset", r.Remaining, wait.Round(time.Second))
			time.Sleep(wait)
		}
	}
}

type Actor struct {
	Login string `json:"login"`
}
