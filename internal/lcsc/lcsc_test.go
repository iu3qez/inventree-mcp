package lcsc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// c8574 is a trimmed copy of the real /product/detail answer for C8574. Prices
// arrive as strings and unused attributes as "-", as LCSC sends them.
var c8574 = map[string]any{
	"productCode":       "C8574",
	"productModel":      "BC847 1E(RANGE:110-220)",
	"brandNameEn":       "JSCJ",
	"productIntroEn":    "Bipolar (BJT) Transistor NPN 45V 0.1A  100MHz 200mW Surface Mount SOT-23",
	"encapStandard":     "SOT-23",
	"parentCatalogName": "Transistors/Thyristors",
	"catalogName":       "Single Bipolar Transistors",
	"pdfUrl":            "https://datasheet.lcsc.com/datasheet/pdf/31182f49.pdf?productCode=C8574",
	"productImages": []string{
		"https://assets.lcsc.com/images/lcsc/900x900/C8574_front.jpg",
		"https://assets.lcsc.com/images/lcsc/900x900/C8574_back.jpg",
	},
	"stockNumber": 9350,
	"productPriceList": []map[string]any{
		{"ladder": 50, "productPrice": "0.0228", "currencySymbol": "$"},
		{"ladder": 500, "productPrice": "0.0190", "currencySymbol": "$"},
	},
	"paramVOList": []map[string]any{
		{"paramNameEn": "Collector - Emitter Voltage VCEO", "paramValueEn": "45V"},
		{"paramNameEn": "Current - Collector(Ic)", "paramValueEn": "100mA"},
		{"paramNameEn": "Vce Saturation(VCE(sat)@Ic,Ib)", "paramValueEn": "-"},
	},
}

// fakeLCSC answers the endpoints go-lcsc calls, inside LCSC's response envelope.
type fakeLCSC struct {
	products map[string]map[string]any
	// searchHits is what /product/query/list returns for any keyword.
	searchHits []map[string]any
	// directMatch is the code /search/v3/global points at, if any.
	directMatch string

	requestedCodes []string
	cookies        []string
}

func (f *fakeLCSC) start(t *testing.T) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(srv.Close)
	c := New(srv.URL)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func (f *fakeLCSC) handle(w http.ResponseWriter, r *http.Request) {
	f.cookies = append(f.cookies, r.Header.Get("Cookie"))

	var result any
	switch r.URL.Path {
	case "/product/detail":
		code := r.URL.Query().Get("productCode")
		f.requestedCodes = append(f.requestedCodes, code)
		if p, ok := f.products[code]; ok {
			result = p
		}
	case "/search/v3/global":
		wrapper := map[string]any{"productSearchResultVO": map[string]any{"productList": []any{}, "totalCount": 0}}
		if f.directMatch != "" {
			wrapper["tipProductDetailUrlVO"] = map[string]any{"productCode": f.directMatch}
		}
		result = wrapper
	case "/product/query/list":
		result = map[string]any{"totalRow": len(f.searchHits), "dataList": f.searchHits}
	default:
		http.Error(w, "unhandled: "+r.URL.Path, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "msg": nil, "result": result, "ok": true})
}

func TestProductMapsListing(t *testing.T) {
	fake := &fakeLCSC{products: map[string]map[string]any{"C8574": c8574}}
	c := fake.start(t)

	// Bare digits: the "C" prefix is restored before the request.
	p, err := c.Product(context.Background(), " 8574 ")
	if err != nil {
		t.Fatalf("Product: %v", err)
	}

	if len(fake.requestedCodes) != 1 || fake.requestedCodes[0] != "C8574" {
		t.Errorf("requested %v, want [C8574]", fake.requestedCodes)
	}
	for field, got := range map[string][2]string{
		"lcsc_code":     {p.Code, "C8574"},
		"MPN":           {p.MPN, "BC847 1E(RANGE:110-220)"},
		"manufacturer":  {p.Manufacturer, "JSCJ"},
		"description":   {p.Description, "Bipolar (BJT) Transistor NPN 45V 0.1A 100MHz 200mW Surface Mount SOT-23"},
		"package":       {p.Package, "SOT-23"},
		"category":      {p.Category, "Transistors/Thyristors / Single Bipolar Transistors"},
		"datasheet_url": {p.DatasheetURL, "https://datasheet.lcsc.com/datasheet/pdf/31182f49.pdf?productCode=C8574"},
		"product_url":   {p.ProductURL, "https://www.lcsc.com/product-detail/C8574.html"},
		"image_url":     {p.ImageURL, "https://assets.lcsc.com/images/lcsc/900x900/C8574_front.jpg"},
	} {
		if got[0] != got[1] {
			t.Errorf("%s = %q, want %q", field, got[0], got[1])
		}
	}
	if p.Stock != 9350 {
		t.Errorf("stock = %d, want 9350", p.Stock)
	}

	wantPrices := []PriceBreak{{50, 0.0228, "USD"}, {500, 0.019, "USD"}}
	if fmt.Sprint(p.PriceBreaks) != fmt.Sprint(wantPrices) {
		t.Errorf("price breaks = %v, want %v", p.PriceBreaks, wantPrices)
	}

	// The "-" placeholder is not a value.
	if len(p.Parameters) != 2 {
		t.Errorf("parameters = %v, want the 2 with values", p.Parameters)
	}
}

// TestPricesStayInUSD guards the currency trap described at priceCurrency:
// asking LCSC for another currency relabels USD figures instead of converting.
func TestPricesStayInUSD(t *testing.T) {
	fake := &fakeLCSC{products: map[string]map[string]any{"C8574": c8574}}
	c := fake.start(t)

	if _, err := c.Product(context.Background(), "C8574"); err != nil {
		t.Fatalf("Product: %v", err)
	}
	if len(fake.cookies) != 1 || fake.cookies[0] != "currencyCode=USD" {
		t.Errorf("cookies = %v, want [currencyCode=USD]", fake.cookies)
	}
}

func TestProductNotFound(t *testing.T) {
	// LCSC answers an unknown code with code 200 and a null result.
	c := (&fakeLCSC{}).start(t)

	_, err := c.Product(context.Background(), "C999999999")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestSearchLimitsAndSummarises(t *testing.T) {
	hits := make([]map[string]any, 30)
	for i := range hits {
		hits[i] = map[string]any{
			"productCode":      fmt.Sprintf("C%d", 1000+i),
			"productModel":     "BC847B,215",
			"brandNameEn":      "Nexperia",
			"encapStandard":    "SOT-23",
			"stockNumber":      351000,
			"productPriceList": []map[string]any{{"ladder": 20, "productPrice": "0.0150"}},
		}
	}
	fake := &fakeLCSC{searchHits: hits, directMatch: "C1000"}
	c := fake.start(t)

	for _, tc := range []struct {
		limit, want int
	}{
		{0, defaultSearchLimit},
		{3, 3},
		{100, maxSearchLimit},
	} {
		res, err := c.Search(context.Background(), "BC847B", tc.limit)
		if err != nil {
			t.Fatalf("Search(limit=%d): %v", tc.limit, err)
		}
		if len(res.Products) != tc.want {
			t.Errorf("limit=%d returned %d products, want %d", tc.limit, len(res.Products), tc.want)
		}
		if res.Total != 30 || res.DirectMatch != "C1000" {
			t.Errorf("total=%d direct_match=%q, want 30 and C1000", res.Total, res.DirectMatch)
		}
	}

	res, _ := c.Search(context.Background(), "BC847B", 1)
	first := res.Products[0]
	if first.PriceFrom == nil || *first.PriceFrom != (PriceBreak{20, 0.015, "USD"}) {
		t.Errorf("price_from = %+v, want 20 @ 0.015 USD", first.PriceFrom)
	}
}

func TestNormalizeCode(t *testing.T) {
	for in, want := range map[string]string{
		"C8574":   "C8574",
		"c8574":   "C8574",
		" 8574 ":  "C8574",
		"BC847":   "BC847",
		"":        "",
		"C12-345": "C12-345",
	} {
		if got := NormalizeCode(in); got != want {
			t.Errorf("NormalizeCode(%q) = %q, want %q", in, got, want)
		}
	}
}
