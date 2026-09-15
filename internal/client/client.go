package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"time"
)

// Client is an HTTP client for the InvenTree REST API.
// Fields are unexported to prevent accidental exposure of credentials
// in logs, fmt output, or JSON serialization.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// New creates a new InvenTree API client.
func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Do executes an HTTP request against the InvenTree API with authentication
// and a JSON content type. The path may include query parameters
// (e.g., "/api/part/?search=foo").
func (c *Client) Do(method, path string, body io.Reader) (*http.Response, error) {
	return c.DoWithContentType(method, path, body, "application/json")
}

// DoWithContentType is Do with an explicit request content type, for bodies
// that aren't JSON (multipart uploads carry their own boundary parameter).
func (c *Client) DoWithContentType(method, path string, body io.Reader, contentType string) (*http.Response, error) {
	// Split path from query string to avoid url.JoinPath encoding the '?'.
	pathPart, query, _ := strings.Cut(path, "?")

	u, err := url.JoinPath(c.baseURL, pathPart)
	if err != nil {
		return nil, fmt.Errorf("building URL for %s: %w", pathPart, err)
	}
	if query != "" {
		u += "?" + query
	}

	req, err := http.NewRequest(method, u, body)
	if err != nil {
		return nil, fmt.Errorf("creating %s request for %s: %w", method, pathPart, err)
	}

	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Content-Type", contentType)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %s", method, pathPart, sanitizeError(err))
	}
	return resp, nil
}

// Get performs a GET request and decodes the JSON response into dest.
func (c *Client) Get(path string, dest any) error {
	resp, err := c.Do(http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return decodeResponse(resp, dest)
}

// Post performs a POST request with a JSON body and decodes the response.
func (c *Client) Post(path string, payload any, dest any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling request body: %w", err)
	}

	resp, err := c.Do(http.MethodPost, path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return decodeResponse(resp, dest)
}

// Patch performs a PATCH request with a JSON body and decodes the response.
func (c *Client) Patch(path string, payload any, dest any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling request body: %w", err)
	}

	resp, err := c.Do(http.MethodPatch, path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return decodeResponse(resp, dest)
}

// MultipartFile is one file part of a multipart/form-data request body.
type MultipartFile struct {
	FieldName   string // form field name, e.g. "image"
	FileName    string // e.g. "121350.jpg"
	Content     []byte
	ContentType string // e.g. "image/jpeg"; sniffed from Content if empty
}

// PatchMultipart performs a PATCH request with a multipart/form-data body
// (optional plain fields plus one or more files) and decodes the JSON
// response into dest.
//
// InvenTree accepts file fields only as multipart, never as JSON — this is
// the path to use for uploading a part image from bytes the caller already
// holds, as opposed to the `remote_image` field which asks the InvenTree
// server to fetch a URL itself.
func (c *Client) PatchMultipart(path string, fields map[string]string, files []MultipartFile, dest any) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	for name, value := range fields {
		if err := w.WriteField(name, value); err != nil {
			return fmt.Errorf("writing form field %q: %w", name, err)
		}
	}

	for _, f := range files {
		contentType := f.ContentType
		if contentType == "" {
			contentType = http.DetectContentType(f.Content)
		}
		// CreateFormFile hardcodes application/octet-stream, so build the
		// part header by hand to keep the real content type.
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition",
			fmt.Sprintf(`form-data; name="%s"; filename="%s"`,
				escapeQuotes(f.FieldName), escapeQuotes(f.FileName)))
		h.Set("Content-Type", contentType)

		part, err := w.CreatePart(h)
		if err != nil {
			return fmt.Errorf("creating form file %q: %w", f.FieldName, err)
		}
		if _, err := part.Write(f.Content); err != nil {
			return fmt.Errorf("writing form file %q: %w", f.FieldName, err)
		}
	}

	if err := w.Close(); err != nil {
		return fmt.Errorf("finalizing multipart body: %w", err)
	}

	resp, err := c.DoWithContentType(http.MethodPatch, path, &buf, w.FormDataContentType())
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return decodeResponse(resp, dest)
}

var quoteEscaper = strings.NewReplacer("\\", "\\\\", `"`, "\\\"")

func escapeQuotes(s string) string { return quoteEscaper.Replace(s) }

// Delete performs a DELETE request.
func (c *Client) Delete(path string) error {
	resp, err := c.Do(http.MethodDelete, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return decodeResponse(resp, nil)
}

// DeleteWithBody performs a DELETE request with a JSON body.
//
// A few InvenTree endpoints put required fields on the delete serializer and
// reject a body-less DELETE with a 400: stock locations want
// delete_stock_items and delete_sub_locations, part categories want
// delete_parts and delete_child_categories. Everything else deletes fine with
// plain Delete.
func (c *Client) DeleteWithBody(path string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling request body: %w", err)
	}

	resp, err := c.Do(http.MethodDelete, path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return decodeResponse(resp, nil)
}

// decodeResponse reads the full response body and handles errors uniformly.
// If dest is nil the body is consumed but not decoded.
func decodeResponse(resp *http.Response, dest any) error {
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return apiError(resp.StatusCode, data)
	}

	if dest == nil {
		return nil
	}

	if len(data) == 0 {
		return fmt.Errorf("empty response body (HTTP %d)", resp.StatusCode)
	}

	if err := json.Unmarshal(data, dest); err != nil {
		return fmt.Errorf("decoding response: %w (body: %.200s)", err, string(data))
	}
	return nil
}

// APIError is returned for non-2xx responses from the InvenTree API.
// Callers can inspect StatusCode via errors.As to branch on specific
// conditions (e.g. falling back to a legacy endpoint on 404).
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string { return e.Message }

// StatusCode extracts the HTTP status code from an error returned by this
// package, unwrapping as needed. It returns 0 if err is not an APIError.
func StatusCode(err error) int {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode
	}
	return 0
}

// apiError returns a descriptive error for non-2xx responses.
func apiError(status int, body []byte) error {
	msg := strings.TrimSpace(string(body))

	switch status {
	case http.StatusUnauthorized:
		return &APIError{status, "authentication failed (401): check INVENTREE_TOKEN"}
	case http.StatusForbidden:
		return &APIError{status, "permission denied (403): token lacks access to this resource"}
	case http.StatusNotFound:
		return &APIError{status, "not found (404): resource does not exist"}
	default:
		if msg == "" {
			return &APIError{status, fmt.Sprintf("API error %d (empty response)", status)}
		}
		// Truncate long error bodies to keep messages readable.
		if len(msg) > 500 {
			msg = msg[:500] + "..."
		}
		return &APIError{status, fmt.Sprintf("API error %d: %s", status, msg)}
	}
}

// sanitizeError strips potential credential info from network errors.
func sanitizeError(err error) string {
	s := err.Error()
	// Remove any token values that might appear in URL-related errors.
	if i := strings.Index(s, "Token "); i != -1 {
		end := strings.IndexAny(s[i+6:], " \"')")
		if end == -1 {
			s = s[:i] + "Token [REDACTED]"
		} else {
			s = s[:i] + "Token [REDACTED]" + s[i+6+end:]
		}
	}
	return s
}

// PaginatedResponse represents a Django REST Framework paginated response.
type PaginatedResponse[T any] struct {
	Count    int    `json:"count"`
	Next     string `json:"next"`
	Previous string `json:"previous"`
	Results  []T    `json:"results"`
}
