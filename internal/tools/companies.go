package tools

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/chrisbotelho/inventree-mcp/internal/client"
	"github.com/chrisbotelho/inventree-mcp/internal/coerce"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Company represents an InvenTree company (supplier, manufacturer or customer).
type Company struct {
	PK                int     `json:"pk"`
	Name              string  `json:"name"`
	Description       string  `json:"description"`
	Website           string  `json:"website"`
	Email             string  `json:"email"`
	Phone             string  `json:"phone"`
	Currency          string  `json:"currency"`
	Contact           string  `json:"contact"`
	Link              string  `json:"link"`
	Active            bool    `json:"active"`
	IsCustomer        bool    `json:"is_customer"`
	IsManufacturer    bool    `json:"is_manufacturer"`
	IsSupplier        bool    `json:"is_supplier"`
	PartsSupplied     int     `json:"parts_supplied"`
	PartsManufactured int     `json:"parts_manufactured"`
	Image             *string `json:"image"`
}

// ManufacturerPart links a part to a manufacturer and its MPN.
type ManufacturerPart struct {
	PK           int    `json:"pk"`
	Part         int    `json:"part"`
	Manufacturer int    `json:"manufacturer"`
	MPN          string `json:"MPN"`
	Description  string `json:"description"`
	Link         string `json:"link"`
}

// SupplierPart links a part to a supplier and its SKU.
type SupplierPart struct {
	PK               int     `json:"pk"`
	Part             int     `json:"part"`
	Supplier         int     `json:"supplier"`
	SKU              string  `json:"SKU"`
	ManufacturerPart *int    `json:"manufacturer_part"`
	MPN              string  `json:"MPN"`
	Description      string  `json:"description"`
	Link             string  `json:"link"`
	Note             string  `json:"note"`
	Packaging        string  `json:"packaging"`
	PackQuantity     string  `json:"pack_quantity"`
	Active           bool    `json:"active"`
	InStock          float64 `json:"in_stock"`
	Available        float64 `json:"available"`
}

// findCompanyByName looks up a company by exact (case-insensitive) name.
// Returns nil without error when nothing matches.
func findCompanyByName(c *client.Client, name string) (*Company, error) {
	path := fmt.Sprintf("/api/company/?search=%s&limit=50&format=json", url.QueryEscape(name))
	var resp client.PaginatedResponse[Company]
	if err := c.Get(path, &resp); err != nil {
		return nil, fmt.Errorf("searching companies: %w", err)
	}
	for i := range resp.Results {
		if strings.EqualFold(strings.TrimSpace(resp.Results[i].Name), strings.TrimSpace(name)) {
			return &resp.Results[i], nil
		}
	}
	return nil, nil
}

// ensureCompanyRoles widens an existing company's roles when the caller needs
// one it does not have yet (e.g. a known manufacturer now also used as a
// supplier). Roles are never removed.
func ensureCompanyRoles(c *client.Client, existing *Company, supplier, manufacturer, customer bool) (*Company, bool, error) {
	payload := map[string]any{}
	if supplier && !existing.IsSupplier {
		payload["is_supplier"] = true
	}
	if manufacturer && !existing.IsManufacturer {
		payload["is_manufacturer"] = true
	}
	if customer && !existing.IsCustomer {
		payload["is_customer"] = true
	}
	if len(payload) == 0 {
		return existing, false, nil
	}

	var updated Company
	path := fmt.Sprintf("/api/company/%d/", existing.PK)
	if err := c.Patch(path, payload, &updated); err != nil {
		return nil, false, fmt.Errorf("updating roles for company %d: %w", existing.PK, err)
	}
	return &updated, true, nil
}

// getOrCreateCompany returns an existing company by name or creates it.
// The returned string is one of "existing", "updated" or "created".
func getOrCreateCompany(c *client.Client, name, description, website string, supplier, manufacturer, customer bool) (*Company, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, "", fmt.Errorf("company name is required")
	}

	found, err := findCompanyByName(c, name)
	if err != nil {
		return nil, "", err
	}
	if found != nil {
		updated, changed, err := ensureCompanyRoles(c, found, supplier, manufacturer, customer)
		if err != nil {
			return nil, "", err
		}
		if changed {
			return updated, "updated", nil
		}
		return updated, "existing", nil
	}

	payload := map[string]any{
		"name":            name,
		"is_supplier":     supplier,
		"is_manufacturer": manufacturer,
		"is_customer":     customer,
	}
	// InvenTree requires a non-empty description on creation.
	if description == "" {
		description = name
	}
	payload["description"] = description
	if website != "" {
		payload["website"] = website
	}

	var created Company
	if err := c.Post("/api/company/", payload, &created); err != nil {
		return nil, "", fmt.Errorf("creating company %q: %w", name, err)
	}
	return &created, "created", nil
}

// -- Search Companies --

type SearchCompaniesInput struct {
	Search         string `json:"search,omitempty" jsonschema:"Search query matched against company name and description. Omit to list all."`
	IsSupplier     *bool  `json:"is_supplier,omitempty" jsonschema:"Filter to companies flagged as suppliers"`
	IsManufacturer *bool  `json:"is_manufacturer,omitempty" jsonschema:"Filter to companies flagged as manufacturers"`
	IsCustomer     *bool  `json:"is_customer,omitempty" jsonschema:"Filter to companies flagged as customers"`
	Limit          int    `json:"limit,omitempty" jsonschema:"Maximum number of results (default 50)"`
}

func RegisterSearchCompanies(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name:        "search_companies",
		Description: "Search companies (suppliers, manufacturers, customers) by name or description. Use this before get_or_create_company to reuse an existing record instead of creating a near-duplicate (e.g. 'TI' vs 'Texas Instruments').",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: true,
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input SearchCompaniesInput) (*mcp.CallToolResult, any, error) {
		limit := input.Limit
		if limit <= 0 {
			limit = 50
		}
		path := fmt.Sprintf("/api/company/?limit=%d&format=json", limit)
		if input.Search != "" {
			path += "&search=" + url.QueryEscape(input.Search)
		}
		if input.IsSupplier != nil {
			path += fmt.Sprintf("&is_supplier=%t", *input.IsSupplier)
		}
		if input.IsManufacturer != nil {
			path += fmt.Sprintf("&is_manufacturer=%t", *input.IsManufacturer)
		}
		if input.IsCustomer != nil {
			path += fmt.Sprintf("&is_customer=%t", *input.IsCustomer)
		}

		var resp client.PaginatedResponse[Company]
		if err := c.Get(path, &resp); err != nil {
			return errResult(fmt.Errorf("searching companies: %w", err)), nil, nil
		}
		return jsonResult(map[string]any{
			"count":   resp.Count,
			"results": resp.Results,
		})
	})
}

// -- Get or Create Company --

type GetOrCreateCompanyInput struct {
	Name           string `json:"name" jsonschema:"Company name (required). Matched case-insensitively against existing companies before creating."`
	Description    string `json:"description,omitempty" jsonschema:"Company description. Defaults to the name when omitted."`
	Website        string `json:"website,omitempty" jsonschema:"Company website URL"`
	IsSupplier     *bool  `json:"is_supplier,omitempty" jsonschema:"Mark the company as a supplier (distributor you buy from)"`
	IsManufacturer *bool  `json:"is_manufacturer,omitempty" jsonschema:"Mark the company as a manufacturer"`
	IsCustomer     *bool  `json:"is_customer,omitempty" jsonschema:"Mark the company as a customer"`
}

func RegisterGetOrCreateCompany(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "get_or_create_company",
		Description: "Look up a company by name and create it if missing, returning its ID either way. " +
			"Idempotent: safe to call before create_manufacturer_part or create_supplier_part. " +
			"If the company exists but lacks a requested role (e.g. it is a manufacturer and you need it as a supplier too), the role is added; roles are never removed.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
			IdempotentHint:  true,
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input GetOrCreateCompanyInput) (*mcp.CallToolResult, any, error) {
		company, status, err := getOrCreateCompany(
			c,
			input.Name,
			input.Description,
			input.Website,
			input.IsSupplier != nil && *input.IsSupplier,
			input.IsManufacturer != nil && *input.IsManufacturer,
			input.IsCustomer != nil && *input.IsCustomer,
		)
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(map[string]any{
			"status":  status,
			"company": company,
		})
	})
}

// -- Create Manufacturer Part --

type CreateManufacturerPartInput struct {
	Part         int    `json:"part" jsonschema:"InvenTree part ID (pk) this manufacturer part belongs to"`
	Manufacturer int    `json:"manufacturer" jsonschema:"Company ID of the manufacturer. Use get_or_create_company with is_manufacturer=true to obtain it."`
	MPN          string `json:"MPN" jsonschema:"Manufacturer Part Number (required)"`
	Description  string `json:"description,omitempty" jsonschema:"Description of this manufacturer part"`
	Link         string `json:"link,omitempty" jsonschema:"URL to the manufacturer product page or datasheet"`
}

func RegisterCreateManufacturerPart(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name:        "create_manufacturer_part",
		Description: "Record the manufacturer and MPN for a part. This is what lets you go from a manufacturer part number back to the InvenTree part. Create the manufacturer company first with get_or_create_company (is_manufacturer=true).",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input CreateManufacturerPartInput) (*mcp.CallToolResult, any, error) {
		if input.Part == 0 || input.Manufacturer == 0 {
			return errResult(fmt.Errorf("part and manufacturer are required")), nil, nil
		}
		if strings.TrimSpace(input.MPN) == "" {
			return errResult(fmt.Errorf("MPN is required")), nil, nil
		}

		payload := map[string]any{
			"part":         input.Part,
			"manufacturer": input.Manufacturer,
			"MPN":          input.MPN,
		}
		if input.Description != "" {
			payload["description"] = input.Description
		}
		if input.Link != "" {
			payload["link"] = input.Link
		}

		var created ManufacturerPart
		if err := c.Post("/api/company/part/manufacturer/", payload, &created); err != nil {
			return errResult(fmt.Errorf("creating manufacturer part: %w", err)), nil, nil
		}
		return jsonResult(created)
	})
}

// -- Create Supplier Part --

type CreateSupplierPartInput struct {
	Part             int    `json:"part" jsonschema:"InvenTree part ID (pk) this supplier part belongs to"`
	Supplier         int    `json:"supplier" jsonschema:"Company ID of the supplier/distributor. Use get_or_create_company with is_supplier=true to obtain it."`
	SKU              string `json:"SKU" jsonschema:"Supplier stock keeping unit, i.e. the distributor order code (required)"`
	ManufacturerPart int    `json:"manufacturer_part,omitempty" jsonschema:"ID of the linked manufacturer part, if any"`
	Description      string `json:"description,omitempty" jsonschema:"Description of this supplier part"`
	Link             string `json:"link,omitempty" jsonschema:"URL to the supplier product page"`
	Note             string `json:"note,omitempty" jsonschema:"Free-form note"`
	Packaging        string `json:"packaging,omitempty" jsonschema:"Packaging this part ships in (e.g. 'Reel', 'Tape', 'Tube')"`
	PackQuantity     string `json:"pack_quantity,omitempty" jsonschema:"Quantity supplied in a single pack, optionally with units (e.g. '100', '5m')"`
}

func RegisterCreateSupplierPart(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "create_supplier_part",
		Description: "Record a distributor SKU for a part (e.g. an LCSC, Mouser or Digi-Key order code), optionally linked to a manufacturer part. " +
			"Without this there is no way to go from a distributor code back to the InvenTree part. Create the supplier company first with get_or_create_company (is_supplier=true).",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input CreateSupplierPartInput) (*mcp.CallToolResult, any, error) {
		if input.Part == 0 || input.Supplier == 0 {
			return errResult(fmt.Errorf("part and supplier are required")), nil, nil
		}
		if strings.TrimSpace(input.SKU) == "" {
			return errResult(fmt.Errorf("SKU is required")), nil, nil
		}

		payload := map[string]any{
			"part":     input.Part,
			"supplier": input.Supplier,
			"SKU":      input.SKU,
		}
		if input.ManufacturerPart != 0 {
			payload["manufacturer_part"] = input.ManufacturerPart
		}
		if input.Description != "" {
			payload["description"] = input.Description
		}
		if input.Link != "" {
			payload["link"] = input.Link
		}
		if input.Note != "" {
			payload["note"] = input.Note
		}
		if input.Packaging != "" {
			payload["packaging"] = input.Packaging
		}
		if input.PackQuantity != "" {
			payload["pack_quantity"] = input.PackQuantity
		}

		var created SupplierPart
		if err := c.Post("/api/company/part/", payload, &created); err != nil {
			return errResult(fmt.Errorf("creating supplier part: %w", err)), nil, nil
		}
		return jsonResult(created)
	})
}

// -- Search Supplier Parts --

type SearchSupplierPartsInput struct {
	Search   string `json:"search,omitempty" jsonschema:"Search query matched against SKU, MPN and description. Use the distributor order code here."`
	Part     int    `json:"part,omitempty" jsonschema:"Filter by InvenTree part ID. 0 or omit for all."`
	Supplier int    `json:"supplier,omitempty" jsonschema:"Filter by supplier company ID. 0 or omit for all."`
	Limit    int    `json:"limit,omitempty" jsonschema:"Maximum number of results (default 25)"`
}

func RegisterSearchSupplierParts(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name:        "search_supplier_parts",
		Description: "Find supplier parts by SKU, MPN or description. This is the way back from a distributor order code (e.g. an LCSC code on a bag of components) to the InvenTree part it belongs to. Check this before creating a part for an incoming order.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: true,
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input SearchSupplierPartsInput) (*mcp.CallToolResult, any, error) {
		limit := input.Limit
		if limit <= 0 {
			limit = 25
		}
		path := fmt.Sprintf("/api/company/part/?limit=%d&format=json", limit)
		if input.Search != "" {
			path += "&search=" + url.QueryEscape(input.Search)
		}
		if input.Part != 0 {
			path += fmt.Sprintf("&part=%d", input.Part)
		}
		if input.Supplier != 0 {
			path += fmt.Sprintf("&supplier=%d", input.Supplier)
		}

		var resp client.PaginatedResponse[SupplierPart]
		if err := c.Get(path, &resp); err != nil {
			return errResult(fmt.Errorf("searching supplier parts: %w", err)), nil, nil
		}
		return jsonResult(map[string]any{
			"count":   resp.Count,
			"results": resp.Results,
		})
	})
}

// -- Get Part Sourcing --

type GetPartSourcingInput struct {
	Part int `json:"part" jsonschema:"The part ID (pk) to list sourcing information for"`
}

func RegisterGetPartSourcing(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name:        "get_part_sourcing",
		Description: "List every manufacturer part (MPN) and supplier part (SKU) recorded for a part, so you can see who makes it and where it can be reordered from.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: true,
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input GetPartSourcingInput) (*mcp.CallToolResult, any, error) {
		if input.Part == 0 {
			return errResult(fmt.Errorf("part is required")), nil, nil
		}

		var mfg client.PaginatedResponse[ManufacturerPart]
		mfgPath := fmt.Sprintf("/api/company/part/manufacturer/?part=%d&limit=100&format=json", input.Part)
		if err := c.Get(mfgPath, &mfg); err != nil {
			return errResult(fmt.Errorf("listing manufacturer parts for part %d: %w", input.Part, err)), nil, nil
		}

		var sup client.PaginatedResponse[SupplierPart]
		supPath := fmt.Sprintf("/api/company/part/?part=%d&limit=100&format=json", input.Part)
		if err := c.Get(supPath, &sup); err != nil {
			return errResult(fmt.Errorf("listing supplier parts for part %d: %w", input.Part, err)), nil, nil
		}

		return jsonResult(map[string]any{
			"part":               input.Part,
			"manufacturer_parts": mfg.Results,
			"supplier_parts":     sup.Results,
		})
	})
}
