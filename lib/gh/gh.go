// Package gh talks to GitHub both ways: a minimal REST client (this file) for
// the handful of mutations prawn performs (comment, close, reopen, label) plus
// the pre-mutation staleness check, and a GraphQL v4 client (the graphql*.go
// files) for the bulk reads everything else rides on.
package gh

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/katbyte/go-kt/chttp"
	"github.com/katbyte/go-kt/clog"
)

type Repo struct {
	Owner string
	Name  string

	token      string
	httpClient *http.Client
}

func NewRepo(owner, name, token string) Repo {
	clog.Log.Debugf("new gh: %s/%s (%s)", owner, name, maskToken(token))
	return Repo{Owner: owner, Name: name, token: token, httpClient: chttp.NewHTTPClient("GitHub")}
}

func maskToken(t string) string {
	if len(t) < 8 {
		return "****"
	}
	return t[:4] + "****"
}

// do performs a JSON request; callers interpret the status code.
func (r Repo) do(method, path string, payload any) (statusCode int, respBody []byte, err error) {
	return r.doAccept(method, path, payload, "application/vnd.github+json")
}

// doAccept is do with the response media type chosen by the caller — the diff
// of a pull request comes back as text, not JSON.
func (r Repo) doAccept(method, path string, payload any, accept string) (statusCode int, respBody []byte, err error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s%s", r.Owner, r.Name, path)

	var body io.Reader = http.NoBody
	if payload != nil {
		b, merr := json.Marshal(payload)
		if merr != nil {
			return 0, nil, fmt.Errorf("marshalling request body: %w", merr)
		}
		body = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(context.Background(), method, url, body)
	if err != nil {
		return 0, nil, fmt.Errorf("building request for %s: %w", url, err)
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	req.Header.Set("Accept", accept)
	req.Header.Set("X-Github-Api-Version", "2022-11-28") // canonical form; github matches case-insensitively
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("request to %s failed: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err = io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("reading response from %s: %w", url, err)
	}
	return resp.StatusCode, respBody, nil
}

// PullState is the subset of PR fields the staleness guard needs. PRs are
// issues to most of the REST API, but state lives on the pulls endpoint so
// merged comes along too.
type PullState struct {
	Number    int       `json:"number"`
	State     string    `json:"state"` // open | closed
	Merged    bool      `json:"merged"`
	UpdatedAt time.Time `json:"updated_at"`
}

// GetPull fetches the live state of a pull request.
func (r Repo) GetPull(number int) (*PullState, error) {
	status, body, err := r.do(http.MethodGet, fmt.Sprintf("/pulls/%d", number), nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("getting PR #%d returned %d: %.200s", number, status, string(body))
	}

	var ps PullState
	if err := json.Unmarshal(body, &ps); err != nil {
		return nil, fmt.Errorf("decoding PR #%d: %w", number, err)
	}
	return &ps, nil
}

// ErrDiffTooLarge is GitHub declining to render a pull request's diff (it
// caps the size); the PR is real, its diff is just unavailable this way.
var ErrDiffTooLarge = errors.New("github will not render this diff (too large)")

// GetPullDiff fetches a pull request's unified diff.
func (r Repo) GetPullDiff(number int) (string, error) {
	status, body, err := r.doAccept(http.MethodGet, fmt.Sprintf("/pulls/%d", number), nil, "application/vnd.github.diff")
	if err != nil {
		return "", err
	}
	switch status {
	case http.StatusOK:
		return string(body), nil
	case http.StatusNotAcceptable:
		return "", ErrDiffTooLarge
	default:
		return "", fmt.Errorf("getting diff for PR #%d returned %d: %.200s", number, status, string(body))
	}
}

// CreateComment posts a comment on a pull request (the issues endpoint serves
// PR conversation comments too).
func (r Repo) CreateComment(number int, text string) error {
	status, body, err := r.do(http.MethodPost, fmt.Sprintf("/issues/%d/comments", number), map[string]string{"body": text})
	if err != nil {
		return err
	}
	if status != http.StatusCreated {
		return fmt.Errorf("commenting on #%d returned %d: %.200s", number, status, string(body))
	}
	return nil
}

// ClosePull closes a pull request (PRs have no state_reason).
func (r Repo) ClosePull(number int) error {
	status, body, err := r.do(http.MethodPatch, fmt.Sprintf("/pulls/%d", number), map[string]string{"state": "closed"})
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("closing #%d returned %d: %.200s", number, status, string(body))
	}
	return nil
}

// ReopenPull reopens a closed pull request.
func (r Repo) ReopenPull(number int) error {
	status, body, err := r.do(http.MethodPatch, fmt.Sprintf("/pulls/%d", number), map[string]string{"state": "open"})
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("reopening #%d returned %d: %.200s", number, status, string(body))
	}
	return nil
}

// AddLabels adds labels to a pull request (existing labels are kept).
func (r Repo) AddLabels(number int, labels []string) error {
	status, body, err := r.do(http.MethodPost, fmt.Sprintf("/issues/%d/labels", number), map[string][]string{"labels": labels})
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("labelling #%d returned %d: %.200s", number, status, string(body))
	}
	return nil
}
