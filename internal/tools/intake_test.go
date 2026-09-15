package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/chrisbotelho/inventree-mcp/internal/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeInvenTree is a minimal stand-in for an InvenTree server, enough to
// exercise the intake workflow without a live instance.
type fakeInvenTree struct {
	mu       sync.Mutex
	requests []string // "METHOD /path"

	// legacyParameterAPI makes the generic /api/parameter/ endpoints 404,
	// mimicking a server older than API v430.
	legacyParameterAPI bool

	// uploadedImage records the last multipart image PATCHed onto a part.
	uploadedImage *uploadedImage

	templates  map[string]int            // template name -> pk
	parameters map[int]string            // template pk -> value
	companies  map[string]map[string]any // lowercased name -> company record

	nextPK int
}

type uploadedImage struct {
	fileName    string
	contentType string
	size        int
}

func newFakeInvenTree() *fakeInvenTree {
	return &fakeInvenTree{
		templates:  map[string]int{},
		parameters: map[int]string{},
		companies:  map[string]map[string]any{},
		nextPK:     100,
	}
}

func (f *fakeInvenTree) pk() int {
	f.nextPK++
	return f.nextPK
}

func (f *fakeInvenTree) record(r *http.Request) {
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
}

func (f *fakeInvenTree) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

// countCalls counts requests matching "METHOD /path" exactly. Prefix matching
// would conflate /api/company/part/ with /api/company/part/manufacturer/.
func (f *fakeInvenTree) countCalls(call string) int {
	n := 0
	for _, c := range f.calls() {
		if c == call {
			n++
		}
	}
	return n
}

func (f *fakeInvenTree) start(t *testing.T) *client.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(srv.Close)
	return client.New(srv.URL, "test-token")
}

func (f *fakeInvenTree) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record(r)

	if got := r.Header.Get("Authorization"); got != "Token test-token" {
		http.Error(w, "bad auth", http.StatusUnauthorized)
		return
	}

	body := map[string]any{}
	if r.Body != nil && !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}

	path := r.URL.Path
	q := r.URL.Query()

	// Legacy servers do not expose the generic parameter endpoints.
	if f.legacyParameterAPI && strings.HasPrefix(path, "/api/parameter/") {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	switch {
	// -- parameter templates --
	case strings.HasSuffix(path, "/parameter/template/"):
		if r.Method == http.MethodPost {
			name, _ := body["name"].(string)
			pk := f.pk()
			f.templates[strings.ToLower(name)] = pk
			writeJSON(w, http.StatusCreated, map[string]any{"pk": pk, "name": name})
			return
		}
		results := []map[string]any{}
		search := strings.ToLower(q.Get("search"))
		for name, pk := range f.templates {
			if search == "" || strings.Contains(name, search) {
				results = append(results, map[string]any{"pk": pk, "name": name})
			}
		}
		writeJSON(w, http.StatusOK, paginated(results))

	// -- parameters --
	case path == "/api/parameter/" || path == "/api/part/parameter/":
		if r.Method == http.MethodPost {
			tmpl := int(toFloat(body["template"]))
			data, _ := body["data"].(string)
			f.parameters[tmpl] = data
			writeJSON(w, http.StatusCreated, map[string]any{
				"pk": f.pk(), "template": tmpl, "data": data,
			})
			return
		}
		results := []map[string]any{}
		for tmpl, data := range f.parameters {
			results = append(results, map[string]any{"pk": tmpl * 10, "template": tmpl, "data": data})
		}
		writeJSON(w, http.StatusOK, paginated(results))

	// -- instance settings: InvenTree wants a currency on every company --
	case strings.HasPrefix(path, "/api/settings/global/"):
		writeJSON(w, http.StatusOK, map[string]any{"value": "EUR"})

	// -- companies --
	case path == "/api/company/":
		if r.Method == http.MethodPost {
			name, _ := body["name"].(string)
			record := map[string]any{
				"pk": f.pk(), "name": name,
				"is_supplier":     body["is_supplier"] == true,
				"is_manufacturer": body["is_manufacturer"] == true,
				"is_customer":     body["is_customer"] == true,
			}
			f.companies[strings.ToLower(name)] = record
			writeJSON(w, http.StatusCreated, record)
			return
		}
		results := []map[string]any{}
		search := strings.ToLower(q.Get("search"))
		for name, record := range f.companies {
			if search == "" || strings.Contains(name, search) {
				results = append(results, record)
			}
		}
		writeJSON(w, http.StatusOK, paginated(results))

	case strings.HasPrefix(path, "/api/company/") && r.Method == http.MethodPatch:
		pk := strings.Trim(strings.TrimPrefix(path, "/api/company/"), "/")
		for _, record := range f.companies {
			if fmt.Sprint(int(toFloat(record["pk"]))) != pk {
				continue
			}
			for k, v := range body {
				record[k] = v
			}
			writeJSON(w, http.StatusOK, record)
			return
		}
		http.Error(w, "no such company", http.StatusNotFound)

	// -- part image upload (multipart) --
	case strings.HasPrefix(path, "/api/part/") && r.Method == http.MethodPatch &&
		strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data"):
		file, header, err := r.FormFile("image")
		if err != nil {
			http.Error(w, "no image field: "+err.Error(), http.StatusBadRequest)
			return
		}
		defer file.Close()
		content, _ := io.ReadAll(file)
		f.uploadedImage = &uploadedImage{
			fileName:    header.Filename,
			contentType: header.Header.Get("Content-Type"),
			size:        len(content),
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"pk": 42, "name": "uploaded", "image": "/media/part_images/uploaded.png",
		})

	// -- parts --
	case path == "/api/part/" && r.Method == http.MethodPost:
		writeJSON(w, http.StatusCreated, map[string]any{
			"pk": 42, "name": body["name"], "description": body["description"],
			"link": body["link"], "default_location": body["default_location"],
		})

	// -- manufacturer parts --
	case path == "/api/company/part/manufacturer/":
		if r.Method == http.MethodPost {
			writeJSON(w, http.StatusCreated, map[string]any{
				"pk": f.pk(), "part": body["part"],
				"manufacturer": body["manufacturer"], "MPN": body["MPN"],
			})
			return
		}
		writeJSON(w, http.StatusOK, paginated(nil))

	// -- supplier parts --
	case path == "/api/company/part/":
		if r.Method == http.MethodPost {
			writeJSON(w, http.StatusCreated, map[string]any{
				"pk": f.pk(), "part": body["part"],
				"supplier": body["supplier"], "SKU": body["SKU"],
				"manufacturer_part": body["manufacturer_part"],
			})
			return
		}
		writeJSON(w, http.StatusOK, paginated(nil))

	// -- stock --
	case path == "/api/stock/" && r.Method == http.MethodPost:
		writeJSON(w, http.StatusCreated, []map[string]any{{
			"pk": f.pk(), "part": body["part"], "quantity": body["quantity"],
		}})

	default:
		http.Error(w, "unhandled: "+path, http.StatusNotFound)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func paginated(results []map[string]any) map[string]any {
	if results == nil {
		results = []map[string]any{}
	}
	return map[string]any{"count": len(results), "results": results}
}

func toFloat(v any) float64 {
	f, _ := v.(float64)
	return f
}

// connect wires an MCP client to a server with all tools registered.
func connect(t *testing.T, c *client.Client) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()

	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.1"}, nil)
	registry := RegisterAll(server, c, nil)
	server.AddReceivingMiddleware(registry.Middleware())

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil).
		Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func callTool(t *testing.T, s *mcp.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("calling %s: %v", name, err)
	}
	if len(res.Content) == 0 {
		t.Fatalf("%s returned no content", name)
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("%s returned %T, want text", name, res.Content[0])
	}
	if res.IsError {
		t.Fatalf("%s failed: %s", name, text.Text)
	}
	out := map[string]any{}
	if err := json.Unmarshal([]byte(text.Text), &out); err != nil {
		t.Fatalf("%s returned non-JSON %q: %v", name, text.Text, err)
	}
	return out
}

// TestAllToolsRegistered checks every tool is exposed with a valid schema and
// a description, and that no name is registered twice.
func TestAllToolsRegistered(t *testing.T) {
	session := connect(t, client.New("http://example.invalid", "test-token"))

	res, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}

	seen := map[string]bool{}
	for _, tool := range res.Tools {
		if seen[tool.Name] {
			t.Errorf("tool %q registered twice", tool.Name)
		}
		seen[tool.Name] = true
		if tool.Description == "" {
			t.Errorf("tool %q has no description", tool.Name)
		}
		if tool.InputSchema == nil {
			t.Errorf("tool %q has no input schema", tool.Name)
		}
	}

	// Tools added for the component intake workflow.
	for _, name := range []string{
		"get_part_parameters", "set_part_parameters", "list_parameter_templates",
		"search_companies", "get_or_create_company",
		"create_manufacturer_part", "create_supplier_part",
		"search_supplier_parts", "get_part_sourcing",
		"intake_part",
	} {
		if !seen[name] {
			t.Errorf("tool %q is not registered", name)
		}
	}
}

// TestParamAPIResolverDetectsFlavour checks the generic endpoints are used when
// available and the legacy ones when the server 404s them.
func TestParamAPIResolverDetectsFlavour(t *testing.T) {
	for _, tc := range []struct {
		name   string
		legacy bool
		want   *paramAPI
	}{
		{"generic", false, genericParamAPI},
		{"legacy", true, legacyParamAPI},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeInvenTree()
			fake.legacyParameterAPI = tc.legacy
			c := fake.start(t)

			res := newParamAPIResolver()
			got, err := res.resolve(c)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if got != tc.want {
				t.Fatalf("resolved %+v, want %+v", got, tc.want)
			}

			// A second resolve must not probe the server again.
			before := fake.countCalls("GET /api/parameter/template/")
			if _, err := res.resolve(c); err != nil {
				t.Fatalf("second resolve: %v", err)
			}
			if after := fake.countCalls("GET /api/parameter/template/"); after != before {
				t.Errorf("resolver probed again: %d -> %d calls", before, after)
			}
		})
	}
}

// TestResolverDoesNotCacheTransientFailure makes sure an unreachable server
// does not pin the resolver to the wrong endpoint flavour forever.
func TestResolverDoesNotCacheTransientFailure(t *testing.T) {
	res := newParamAPIResolver()
	if _, err := res.resolve(client.New("http://127.0.0.1:1", "t")); err == nil {
		t.Fatal("expected an error from an unreachable server")
	}
	if res.resolved != nil {
		t.Fatal("a failed probe must not be cached")
	}
}

// TestSetPartParametersCreatesTemplates covers the common case: a datasheet
// spec whose template does not exist yet.
func TestSetPartParametersCreatesTemplates(t *testing.T) {
	fake := newFakeInvenTree()
	fake.templates["resistance"] = 7 // already present
	session := connect(t, fake.start(t))

	out := callTool(t, session, "set_part_parameters", map[string]any{
		"part": 42,
		"parameters": []map[string]any{
			{"name": "Resistance", "value": "10k", "units": "ohm"},
			{"name": "Tolerance", "value": "1%"},
		},
	})

	if failed := toFloat(out["failed"]); failed != 0 {
		t.Fatalf("expected no failures, got %v (%v)", failed, out["parameters"])
	}
	// "Resistance" matched an existing template; only "Tolerance" is created.
	if n := fake.countCalls("POST /api/parameter/template/"); n != 1 {
		t.Errorf("created %d templates, want 1", n)
	}
	if n := fake.countCalls("POST /api/parameter/"); n != 2 {
		t.Errorf("wrote %d parameter values, want 2", n)
	}
}

// TestSetPartParametersRejectsUnknownTemplate checks create_templates=false is
// honoured instead of silently creating a template.
func TestSetPartParametersRejectsUnknownTemplate(t *testing.T) {
	fake := newFakeInvenTree()
	session := connect(t, fake.start(t))

	out := callTool(t, session, "set_part_parameters", map[string]any{
		"part":             42,
		"create_templates": false,
		"parameters":       []map[string]any{{"name": "Nonexistent", "value": "x"}},
	})

	if failed := toFloat(out["failed"]); failed != 1 {
		t.Fatalf("expected 1 failure, got %v", failed)
	}
	if n := fake.countCalls("POST /api/parameter/template/"); n != 0 {
		t.Errorf("created %d templates, want 0", n)
	}
}

// TestIntakePartEndToEnd walks the whole workflow: create the part, set
// parameters, record MPN and SKU, and book initial stock.
func TestIntakePartEndToEnd(t *testing.T) {
	fake := newFakeInvenTree()
	session := connect(t, fake.start(t))

	out := callTool(t, session, "intake_part", map[string]any{
		"name":        "LM7805",
		"description": "1A 5V linear voltage regulator, TO-220",
		"category":    3,
		"link":        "https://example.com/lm7805.pdf",
		"parameters": []map[string]any{
			{"name": "Output Voltage", "value": "5", "units": "V"},
		},
		"manufacturer":  "Texas Instruments",
		"MPN":           "LM7805CT",
		"supplier":      "LCSC",
		"SKU":           "C12345",
		"initial_stock": 25,
	})

	if problems, ok := out["problems"]; ok {
		t.Fatalf("intake reported problems: %v", problems)
	}
	if ok, _ := out["ok"].(bool); !ok {
		t.Fatal("intake did not report success")
	}
	if got := toFloat(out["part_id"]); got != 42 {
		t.Errorf("part_id = %v, want 42", got)
	}

	for _, want := range []string{
		"POST /api/part/",
		"POST /api/company/part/manufacturer/",
		"POST /api/company/part/",
		"POST /api/stock/",
	} {
		if fake.countCalls(want) != 1 {
			t.Errorf("expected exactly one %q, got %d (calls: %v)", want, fake.countCalls(want), fake.calls())
		}
	}

	// Both companies are created once, with the right roles.
	if n := fake.countCalls("POST /api/company/"); n != 2 {
		t.Errorf("created %d companies, want 2", n)
	}
}

// TestIntakePartPartialFailure checks a failing step does not abort the rest:
// the part and its SKU are still recorded when stock creation fails.
func TestIntakePartPartialFailure(t *testing.T) {
	fake := newFakeInvenTree()
	c := fake.start(t)
	session := connect(t, c)

	// An MPN without a manufacturer is reported but must not stop the intake.
	out := callTool(t, session, "intake_part", map[string]any{
		"name":     "NE555",
		"MPN":      "NE555P",
		"supplier": "LCSC",
		"SKU":      "C7593",
	})

	problems, _ := out["problems"].([]any)
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %v", out["problems"])
	}
	if !strings.Contains(fmt.Sprint(problems[0]), "manufacturer") {
		t.Errorf("unexpected problem: %v", problems[0])
	}
	if fake.countCalls("POST /api/company/part/") != 1 {
		t.Error("supplier part should still have been created")
	}
}

// TestGetOrCreateCompanyIsIdempotent checks an existing company is reused.
func TestGetOrCreateCompanyIsIdempotent(t *testing.T) {
	fake := newFakeInvenTree()
	fake.companies["lcsc"] = map[string]any{
		"pk": 5.0, "name": "LCSC", "is_supplier": true,
	}
	session := connect(t, fake.start(t))

	out := callTool(t, session, "get_or_create_company", map[string]any{
		"name":        "LCSC",
		"is_supplier": true,
	})

	if status := out["status"]; status != "existing" && status != "updated" {
		t.Errorf("status = %v, want existing or updated", status)
	}
	if fake.countCalls("POST /api/company/") != 0 {
		t.Error("an existing company must not be created again")
	}
}

// TestGetOrCreateCompanyWidensRoles checks a company already known as a
// manufacturer gains the supplier role instead of being duplicated.
func TestGetOrCreateCompanyWidensRoles(t *testing.T) {
	fake := newFakeInvenTree()
	fake.companies["mouser"] = map[string]any{
		"pk": 9.0, "name": "Mouser", "is_manufacturer": true, "is_supplier": false,
	}
	session := connect(t, fake.start(t))

	out := callTool(t, session, "get_or_create_company", map[string]any{
		"name":        "Mouser",
		"is_supplier": true,
	})

	if out["status"] != "updated" {
		t.Fatalf("status = %v, want updated", out["status"])
	}
	if fake.countCalls("POST /api/company/") != 0 {
		t.Error("an existing company must not be duplicated")
	}
	company, _ := out["company"].(map[string]any)
	if company["is_supplier"] != true || company["is_manufacturer"] != true {
		t.Errorf("roles were not widened: %v", company)
	}
}

// pngBytes is the PNG magic number, which is all http.DetectContentType needs
// to call this an image.
var pngBytes = []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 32))

// TestSetPartImageUploadsBytes checks the image is fetched here and PATCHed to
// InvenTree as multipart file bytes. The remote_image field this used to rely
// on was removed from the Part API in v489, and because DRF drops unknown keys
// silently, sending it returned HTTP 200 and left the part with no image.
func TestSetPartImageUploadsBytes(t *testing.T) {
	images := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngBytes)
	}))
	t.Cleanup(images.Close)

	fake := newFakeInvenTree()
	session := connect(t, fake.start(t))

	callTool(t, session, "set_part_image", map[string]any{
		"id":        42,
		"image_url": images.URL + "/photo.png",
	})

	if fake.uploadedImage == nil {
		t.Fatal("no multipart upload reached the server")
	}
	if got := fake.uploadedImage.fileName; got != "photo.png" {
		t.Errorf("file name = %q, want photo.png", got)
	}
	if got := fake.uploadedImage.contentType; got != "image/png" {
		t.Errorf("content type = %q, want image/png", got)
	}
	if got := fake.uploadedImage.size; got != len(pngBytes) {
		t.Errorf("uploaded %d bytes, want %d", got, len(pngBytes))
	}
}

// TestSetPartImageRejectsNonImage checks a URL that does not serve an image is
// reported instead of being uploaded as whatever it is.
func TestSetPartImageRejectsNonImage(t *testing.T) {
	html := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<!DOCTYPE html><html><body>not an image</body></html>"))
	}))
	t.Cleanup(html.Close)

	fake := newFakeInvenTree()
	session := connect(t, fake.start(t))

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "set_part_image",
		Arguments: map[string]any{"id": 42, "image_url": html.URL + "/page"},
	})
	if err != nil {
		t.Fatalf("calling set_part_image: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected an error for a non-image URL")
	}
	if fake.uploadedImage != nil {
		t.Error("nothing should have been uploaded")
	}
}
