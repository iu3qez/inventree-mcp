// Package lcsc looks up components on LCSC (lcsc.com) through go-lcsc, which
// wraps the undocumented endpoints behind the LCSC website: they need no
// credentials and can change without notice. The package returns this server's
// own compact types, so the tools do not depend on the library's and swapping
// it out touches only this file.
package lcsc

import (
	"context"
	"strings"

	golcsc "github.com/PatrickWalther/go-lcsc"
)

// ErrNotFound is returned when LCSC has no product for a code.
var ErrNotFound = golcsc.ErrNotFound

const (
	defaultSearchLimit = 10
	// The search endpoint returns at most one page of 25 products.
	maxSearchLimit = 25
)

// Prices are always USD. productPrice, the only price go-lcsc reads, is quoted
// in USD whatever currency is asked for: go-lcsc's WithCurrency only changes
// the currencyCode cookie, and LCSC answers by relabelling that USD figure with
// the other currency's symbol while the converted amount goes into a
// currencyPrice field go-lcsc does not map (verified 2026-09-16 on C8574:
// 0.0228 "€" with currencyPrice 0.0203). The client therefore never sets a
// currency, and conversion is left to InvenTree's exchange rates.
const priceCurrency = "USD"

// Product is a component as listed on LCSC, trimmed to what part intake needs.
type Product struct {
	Code         string       `json:"lcsc_code"`
	MPN          string       `json:"MPN"`
	Manufacturer string       `json:"manufacturer"`
	Description  string       `json:"description"`
	Package      string       `json:"package,omitempty"`
	Category     string       `json:"category,omitempty"`
	DatasheetURL string       `json:"datasheet_url,omitempty"`
	ProductURL   string       `json:"product_url"`
	ImageURL     string       `json:"image_url,omitempty"`
	Stock        int          `json:"stock"`
	PriceBreaks  []PriceBreak `json:"price_breaks,omitempty"`
	Parameters   []Parameter  `json:"parameters,omitempty"`
}

// Summary is one search hit, small enough to list many of.
type Summary struct {
	Code         string      `json:"lcsc_code"`
	MPN          string      `json:"MPN"`
	Manufacturer string      `json:"manufacturer"`
	Description  string      `json:"description"`
	Package      string      `json:"package,omitempty"`
	Stock        int         `json:"stock"`
	PriceFrom    *PriceBreak `json:"price_from,omitempty"`
}

// PriceBreak is the unit price from a quantity upwards.
type PriceBreak struct {
	Quantity int     `json:"quantity"`
	Price    float64 `json:"price"`
	Currency string  `json:"currency"`
}

// Parameter is one datasheet specification, under LCSC's own name for it.
type Parameter struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// SearchResult holds the hits for a keyword. DirectMatch is set when LCSC
// recognised the keyword as one specific product code or MPN.
type SearchResult struct {
	Total       int       `json:"total"`
	DirectMatch string    `json:"direct_match,omitempty"`
	Products    []Summary `json:"products"`
}

// Client queries LCSC.
type Client struct {
	api *golcsc.Client
}

// New returns an LCSC client. baseURL replaces the public endpoints and exists
// for tests; pass "" otherwise.
func New(baseURL string) *Client {
	var opts []golcsc.ClientOption
	if baseURL != "" {
		opts = append(opts, golcsc.WithBaseURL(baseURL))
	}
	return &Client{api: golcsc.NewClient(opts...)}
}

// Close stops the library's cache janitor.
func (c *Client) Close() error {
	return c.api.Close()
}

// Product fetches the full listing for an LCSC code such as "C8574". The
// leading "C" may be omitted.
func (c *Client) Product(ctx context.Context, code string) (*Product, error) {
	p, err := c.api.Product.Details(ctx, NormalizeCode(code))
	if err != nil {
		return nil, err
	}

	product := &Product{
		Code:         p.ProductCode,
		MPN:          clean(p.ProductModel),
		Manufacturer: clean(p.BrandNameEn),
		Description:  clean(p.ProductIntroEn),
		Package:      clean(p.EncapStandard),
		Category:     joinNonEmpty(" / ", clean(p.ParentCatalogName), clean(p.CatalogName)),
		DatasheetURL: strings.TrimSpace(p.PdfURL),
		ProductURL:   p.GetProductURL(),
		ImageURL:     imageURL(p),
		Stock:        p.StockNumber,
	}
	for _, pb := range p.ProductPriceList {
		product.PriceBreaks = append(product.PriceBreaks, priceBreak(pb))
	}
	for _, param := range p.ParamVOList {
		name, value := clean(param.ParamNameEn), clean(param.ParamValueEn)
		// LCSC lists every attribute of the category, filling the ones that do
		// not apply with "-".
		if name == "" || value == "" || value == "-" {
			continue
		}
		product.Parameters = append(product.Parameters, Parameter{Name: name, Value: value})
	}
	return product, nil
}

// Search looks a keyword up in the LCSC catalogue: an MPN, an LCSC code or a
// free-text description. limit defaults to 10 and is capped at 25.
func (c *Client) Search(ctx context.Context, keyword string, limit int) (*SearchResult, error) {
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	limit = min(limit, maxSearchLimit)

	resp, err := c.api.Search.Keyword(ctx, &golcsc.SearchRequest{Keyword: keyword})
	if err != nil {
		return nil, err
	}

	result := &SearchResult{
		Total:       resp.TotalCount,
		DirectMatch: resp.DirectMatchCode,
		Products:    []Summary{},
	}
	for _, p := range resp.Products {
		if len(result.Products) == limit {
			break
		}
		summary := Summary{
			Code:         p.ProductCode,
			MPN:          clean(p.ProductModel),
			Manufacturer: clean(p.BrandNameEn),
			Description:  clean(p.ProductIntroEn),
			Package:      clean(p.EncapStandard),
			Stock:        p.StockNumber,
		}
		if len(p.ProductPriceList) > 0 {
			first := priceBreak(p.ProductPriceList[0])
			summary.PriceFrom = &first
		}
		result.Products = append(result.Products, summary)
	}
	return result, nil
}

// NormalizeCode upper-cases an LCSC code and restores the "C" prefix when only
// the digits were given.
func NormalizeCode(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code != "" && strings.Trim(code, "0123456789") == "" {
		code = "C" + code
	}
	return code
}

func priceBreak(pb golcsc.PriceBreak) PriceBreak {
	return PriceBreak{Quantity: pb.Ladder, Price: float64(pb.ProductPrice), Currency: priceCurrency}
}

// imageURL prefers the first gallery image, which LCSC orders front view first.
func imageURL(p *golcsc.Product) string {
	for _, img := range p.ProductImages {
		if img = strings.TrimSpace(img); img != "" {
			return img
		}
	}
	return strings.TrimSpace(p.ProductImageURL)
}

// clean collapses the runs of spaces LCSC leaves in its text fields.
func clean(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func joinNonEmpty(sep string, parts ...string) string {
	kept := parts[:0]
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, sep)
}
