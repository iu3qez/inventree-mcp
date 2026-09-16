package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chrisbotelho/inventree-mcp/internal/client"
	"github.com/chrisbotelho/inventree-mcp/internal/lcsc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// startLCSC serves a single LCSC product, C8574, inside LCSC's response envelope.
func startLCSC(t *testing.T) *lcsc.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var result any
		if r.URL.Path == "/product/detail" && r.URL.Query().Get("productCode") == "C8574" {
			result = map[string]any{
				"productCode": "C8574", "productModel": "BC847 1E(RANGE:110-220)", "brandNameEn": "JSCJ",
				"productIntroEn": "Bipolar (BJT) Transistor NPN 45V 0.1A SOT-23",
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "result": result})
	}))
	t.Cleanup(srv.Close)
	lc := lcsc.New(srv.URL)
	t.Cleanup(func() { _ = lc.Close() })
	return lc
}

// startInvenTreeWithSKUs answers supplier part searches from a fixed list.
func startInvenTreeWithSKUs(t *testing.T, skus map[string]int) *client.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/company/part/" {
			http.Error(w, "unhandled: "+r.URL.Path, http.StatusNotFound)
			return
		}
		results := []map[string]any{}
		for sku, part := range skus {
			if strings.Contains(sku, r.URL.Query().Get("search")) {
				results = append(results, map[string]any{"pk": 7, "part": part, "SKU": sku})
			}
		}
		writeJSON(w, http.StatusOK, paginated(results))
	}))
	t.Cleanup(srv.Close)
	return client.New(srv.URL, "test-token")
}

// TestLCSCGetProductReportsExistingPart checks the lookup says whether the
// code is already a supplier part, which is what stops a duplicate intake.
func TestLCSCGetProductReportsExistingPart(t *testing.T) {
	for _, tc := range []struct {
		name     string
		skus     map[string]int
		wantPart float64 // 0 when no supplier part should be reported
	}{
		{"new component", map[string]int{"C99999": 12}, 0},
		{"already registered", map[string]int{"C8574": 42}, 42},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := connectWithLCSC(t, startInvenTreeWithSKUs(t, tc.skus), startLCSC(t))

			out := callTool(t, session, "lcsc_get_product", map[string]any{"code": "c8574"})

			product, _ := out["product"].(map[string]any)
			if product["MPN"] != "BC847 1E(RANGE:110-220)" {
				t.Errorf("product = %v, want the C8574 listing", product)
			}
			existing, present := out["inventree_supplier_part"]
			if !present {
				t.Fatalf("inventree_supplier_part missing from %v", out)
			}
			if tc.wantPart == 0 {
				if existing != nil {
					t.Errorf("inventree_supplier_part = %v, want null", existing)
				}
				return
			}
			sp, _ := existing.(map[string]any)
			if toFloat(sp["part"]) != tc.wantPart {
				t.Errorf("inventree_supplier_part = %v, want part %v", existing, tc.wantPart)
			}
		})
	}
}

func TestLCSCGetProductUnknownCode(t *testing.T) {
	session := connectWithLCSC(t, startInvenTreeWithSKUs(t, nil), startLCSC(t))

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "lcsc_get_product",
		Arguments: map[string]any{"code": "C1"},
	})
	if err != nil {
		t.Fatalf("calling lcsc_get_product: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected an error for an unknown code")
	}
	if text := res.Content[0].(*mcp.TextContent).Text; !strings.Contains(text, "no product with code C1") {
		t.Errorf("error = %q, want it to name the missing code", text)
	}
}
