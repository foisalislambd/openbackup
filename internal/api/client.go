package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/foisalislambd/openbackup/internal/version"
)

// Client is the agent-side HTTP client. It is safe for concurrent use and
// retries idempotent requests, which is the whole reason the protocol is
// content-addressed: a retried chunk upload can never corrupt anything.
type Client struct {
	baseURL *url.URL
	http    *http.Client
	token   string

	// MaxRetries bounds the retry loop for transient failures.
	MaxRetries int
	// Backoff is the initial retry delay, doubled per attempt.
	Backoff time.Duration
	// Limiter, when set, paces upload bytes.
	Limiter Limiter
}

// Limiter paces outgoing bytes so backups never saturate a home connection.
type Limiter interface {
	// Wait blocks until n bytes may be sent, or ctx is done.
	Wait(ctx context.Context, n int) error
}

// APIError is a structured non-2xx response.
type APIError struct {
	StatusCode int
	Code       string
	Message    string
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("server returned %d (%s): %s", e.StatusCode, e.Code, e.Message)
	}
	return fmt.Sprintf("server returned %d: %s", e.StatusCode, e.Message)
}

// Retryable reports whether the request may be retried as-is.
func (e *APIError) Retryable() bool {
	return e.StatusCode == http.StatusTooManyRequests || e.StatusCode >= 500
}

// IsAuthError reports whether err means the device credentials are no longer
// valid, in which case the agent should stop and ask to be re-enrolled.
func IsAuthError(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden
	}
	return false
}

// IsQuotaError reports whether the account is out of space.
func IsQuotaError(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Code == CodeQuotaExceeded
}

// IsEncryptionRequired reports whether the server rejected plaintext data.
func IsEncryptionRequired(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Code == CodeEncryptionRequired
}

// IsNotFound reports whether the server does not know the requested object.
func IsNotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

// NewClient builds a client for a server base URL such as
// https://backup.example.com.
func NewClient(baseURL, token string) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("api: invalid server URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("api: server URL must be http or https, got %q", u.Scheme)
	}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   15 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		// A handful of connections is enough; more would just add memory and
		// compete with whatever the user is actually doing.
		MaxIdleConns:          8,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ExpectContinueTimeout: 5 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	return &Client{
		baseURL: u,
		http: &http.Client{
			Transport: transport,
			// No global timeout: a single chunk upload on a slow link can take
			// minutes, and per-request contexts already bound each call.
		},
		token:      token,
		MaxRetries: 6,
		Backoff:    time.Second,
	}, nil
}

// SetToken updates the device credentials, used right after enrolment.
func (c *Client) SetToken(token string) { c.token = token }

// BaseURL returns the configured server URL.
func (c *Client) BaseURL() string { return c.baseURL.String() }

// do performs a request with retries. body must be nil or a byte slice so it
// can be replayed on retry. Non-idempotent methods (POST that create resources)
// are not retried on network/5xx errors — a lost response after the server
// committed would duplicate snapshots or burn a join code.
func (c *Client) do(ctx context.Context, method, path string, body []byte, headers map[string]string) (*http.Response, error) {
	backoff := c.Backoff
	if backoff <= 0 {
		backoff = time.Second
	}
	maxAttempts := c.MaxRetries
	if !idempotentMethod(method) {
		maxAttempts = 0
	}
	var lastErr error
	for attempt := 0; attempt <= maxAttempts; attempt++ {
		if attempt > 0 {
			delay := backoff
			var apiErr *APIError
			if errors.As(lastErr, &apiErr) && apiErr.RetryAfter > 0 {
				delay = apiErr.RetryAfter
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
			// Cap the backoff so a device that was offline overnight still
			// reconnects within a minute of the link coming back.
			if backoff < time.Minute {
				backoff *= 2
			}
		}

		var reader io.Reader
		if body != nil {
			if c.Limiter != nil && len(body) > 0 {
				if err := c.Limiter.Wait(ctx, len(body)); err != nil {
					return nil, err
				}
			}
			reader = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, c.baseURL.String()+path, reader)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", version.UserAgent())
		req.Header.Set(HeaderProtocol, Version)
		if c.token != "" {
			req.Header.Set(HeaderDeviceToken, "Bearer "+c.token)
		}
		if body != nil {
			req.ContentLength = int64(len(body))
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, err := c.http.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = err
			continue
		}
		if resp.StatusCode < 300 {
			return resp, nil
		}
		apiErr := parseError(resp)
		resp.Body.Close()
		if !apiErr.Retryable() {
			return nil, apiErr
		}
		lastErr = apiErr
	}
	return nil, fmt.Errorf("api: %s %s failed after %d attempts: %w", method, path, maxAttempts+1, lastErr)
}

func idempotentMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete:
		return true
	default:
		return false
	}
}

func parseError(resp *http.Response) *APIError {
	apiErr := &APIError{StatusCode: resp.StatusCode, Message: resp.Status}
	// Error bodies are small; cap the read so a misbehaving proxy cannot make
	// the agent allocate an HTML page of unbounded size.
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	var parsed ErrorResponse
	if json.Unmarshal(raw, &parsed) == nil && parsed.Error != "" {
		apiErr.Message = parsed.Error
		apiErr.Code = parsed.Code
	} else if len(raw) > 0 {
		apiErr.Message = strings.TrimSpace(string(raw))
	}
	if v := resp.Header.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil {
			apiErr.RetryAfter = time.Duration(secs) * time.Second
		}
	}
	return apiErr
}

// postJSON sends a JSON request and decodes a JSON response into out, which may
// be nil.
func (c *Client) postJSON(ctx context.Context, method, path string, in, out any) error {
	var body []byte
	if in != nil {
		var err error
		body, err = json.Marshal(in)
		if err != nil {
			return err
		}
	}
	headers := map[string]string{"Content-Type": "application/json"}
	if out != nil {
		headers["Accept"] = "application/json"
	}
	resp, err := c.do(ctx, method, path, body, headers)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Health checks server reachability and protocol compatibility.
func (c *Client) Health(ctx context.Context) error {
	resp, err := c.do(ctx, http.MethodGet, PathHealth, nil, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	return nil
}

// Enroll exchanges a join token for device credentials.
func (c *Client) Enroll(ctx context.Context, req EnrollRequest) (*EnrollResponse, error) {
	var out EnrollResponse
	if err := c.postJSON(ctx, http.MethodPost, PathEnroll, req, &out); err != nil {
		return nil, err
	}
	if out.DeviceToken == "" {
		return nil, errors.New("api: server returned an empty device token")
	}
	c.SetToken(out.DeviceToken)
	return &out, nil
}

// Heartbeat reports status and collects pending commands.
func (c *Client) Heartbeat(ctx context.Context, req HeartbeatRequest) (*HeartbeatResponse, error) {
	var out HeartbeatResponse
	if err := c.postJSON(ctx, http.MethodPost, PathHeartbeat, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// MissingChunks returns the subset of digests the server does not have yet.
func (c *Client) MissingChunks(ctx context.Context, digests []string) ([]string, error) {
	if len(digests) == 0 {
		return nil, nil
	}
	var out MissingChunksResponse
	if err := c.postJSON(ctx, http.MethodPost, PathChunksMissing, MissingChunksRequest{Digests: digests}, &out); err != nil {
		return nil, err
	}
	return out.Missing, nil
}

// PutChunk uploads one encoded blob. The digest is of the *plaintext*, so the
// server can verify content addressing without the encryption key.
func (c *Client) PutChunk(ctx context.Context, digest string, blob []byte, plainLen int) error {
	headers := map[string]string{
		"Content-Type":      "application/octet-stream",
		HeaderChunkPlainLen: strconv.Itoa(plainLen),
	}
	resp, err := c.do(ctx, http.MethodPut, PathChunk+digest, blob, headers)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	return nil
}

// GetChunk downloads one encoded blob, used during restore.
func (c *Client) GetChunk(ctx context.Context, digest string) ([]byte, error) {
	resp, err := c.do(ctx, http.MethodGet, PathChunk+digest, nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// Chunks are bounded by the policy max size; the limit guards against a
	// hostile or broken server streaming forever.
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

// StartSnapshot opens a snapshot and returns the id plus the kind the server
// actually accepted (it may promote an unusable delta request to a full).
func (c *Client) StartSnapshot(ctx context.Context, req StartSnapshotRequest) (StartSnapshotResponse, error) {
	var out StartSnapshotResponse
	if err := c.postJSON(ctx, http.MethodPost, PathSnapshots, req, &out); err != nil {
		return StartSnapshotResponse{}, err
	}
	if out.SnapshotID == "" {
		return StartSnapshotResponse{}, errors.New("api: server returned an empty snapshot id")
	}
	if out.Kind == "" {
		out.Kind = req.Kind
	}
	return out, nil
}

// AddEntries appends a batch of entries to an open snapshot.
func (c *Client) AddEntries(ctx context.Context, snapshotID string, req AddEntriesRequest) error {
	return c.postJSON(ctx, http.MethodPost, PathSnapshots+"/"+snapshotID+PathSnapshotEntry, req, nil)
}

// CompleteSnapshot closes a snapshot.
func (c *Client) CompleteSnapshot(ctx context.Context, snapshotID string, req CompleteSnapshotRequest) error {
	return c.postJSON(ctx, http.MethodPost, PathSnapshots+"/"+snapshotID+PathSnapshotFinish, req, nil)
}

// ListSnapshots returns this device's snapshots, newest first.
func (c *Client) ListSnapshots(ctx context.Context) ([]Snapshot, error) {
	var out struct {
		Snapshots []Snapshot `json:"snapshots"`
	}
	if err := c.postJSON(ctx, http.MethodGet, PathSnapshots, nil, &out); err != nil {
		return nil, err
	}
	return out.Snapshots, nil
}

// FileVersion is one distinct appearance of a path across backups.
type FileVersion struct {
	Snapshot Snapshot `json:"snapshot"`
	Entry    Entry    `json:"entry"`
}

// FileVersions lists distinct content versions of a path on this device.
func (c *Client) FileVersions(ctx context.Context, path string, limit int) ([]FileVersion, error) {
	q := url.Values{}
	q.Set("path", path)
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var out struct {
		Versions []FileVersion `json:"versions"`
	}
	if err := c.postJSON(ctx, http.MethodGet, PathFileVersions+"?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	if out.Versions == nil {
		return []FileVersion{}, nil
	}
	return out.Versions, nil
}

// EntryQuery selects a page of snapshot entries.
type EntryQuery struct {
	// Prefix limits the result to one path and everything under it.
	Prefix string
	// Cursor continues a previous page.
	Cursor string
	Limit  int
	// DirectOnly asks for the immediate children of Prefix only, which is what a
	// folder-by-folder browser wants. Restores need the full subtree.
	DirectOnly bool
}

// SnapshotEntries fetches a page of entries for restore. An empty nextCursor in
// the response means the tree is complete.
func (c *Client) SnapshotEntries(ctx context.Context, snapshotID string, query EntryQuery) ([]Entry, string, error) {
	q := url.Values{}
	if query.Prefix != "" {
		q.Set("prefix", query.Prefix)
	}
	if query.Cursor != "" {
		q.Set("cursor", query.Cursor)
	}
	if query.Limit > 0 {
		q.Set("limit", strconv.Itoa(query.Limit))
	}
	if query.DirectOnly {
		q.Set("children", "1")
	}
	path := PathSnapshots + "/" + snapshotID + PathSnapshotEntry
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out struct {
		Entries    []Entry `json:"entries"`
		NextCursor string  `json:"next_cursor"`
	}
	if err := c.postJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, "", err
	}
	return out.Entries, out.NextCursor, nil
}

// PutEscrow stores the passphrase-wrapped master key.
func (c *Client) PutEscrow(ctx context.Context, e Escrow) error {
	return c.postJSON(ctx, http.MethodPut, PathKeyEscrow, e, nil)
}

// GetEscrow fetches the wrapped master key, if the account has one.
func (c *Client) GetEscrow(ctx context.Context) (*Escrow, error) {
	var out Escrow
	if err := c.postJSON(ctx, http.MethodGet, PathKeyEscrow, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SendEvents uploads log events for the dashboard activity feed. Event delivery
// is best effort: losing a log line must never fail a backup.
func (c *Client) SendEvents(ctx context.Context, events []Event) error {
	if len(events) == 0 {
		return nil
	}
	return c.postJSON(ctx, http.MethodPost, PathEvents, EventsRequest{Events: events}, nil)
}
