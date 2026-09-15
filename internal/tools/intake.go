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

// IntakePartInput drives the one-shot component intake workflow.
type IntakePartInput struct {
	// Part identity. Provide Part to attach sourcing data to an existing
	// part, or Name to create a new one.
	Part        int    `json:"part,omitempty" jsonschema:"Existing part ID (pk) to enrich. Omit to create a new part from name/description."`
	Name        string `json:"name,omitempty" jsonschema:"Part name, required when creating a new part (e.g. 'LM7805')"`
	Description string `json:"description,omitempty" jsonschema:"Short descriptive summary of what the part is. Never leave blank or a copy of the part number."`
	Category    int    `json:"category,omitempty" jsonschema:"Category ID. Use list_part_categories to pick the deepest category that fits."`
	IPN         string `json:"IPN,omitempty" jsonschema:"Internal Part Number"`
	Keywords    string `json:"keywords,omitempty" jsonschema:"Keywords for search"`
	Units       string `json:"units,omitempty" jsonschema:"Units of measure"`
	Link        string `json:"link,omitempty" jsonschema:"Datasheet URL for the part"`
	ImageURL    string `json:"image_url,omitempty" jsonschema:"Product image URL. InvenTree downloads it server-side."`

	MinimumStock    int `json:"minimum_stock,omitempty" jsonschema:"Minimum stock level"`
	DefaultLocation int `json:"default_location,omitempty" jsonschema:"Default stock location ID for this part"`

	Parameters []ParameterValueInput `json:"parameters,omitempty" jsonschema:"Technical parameters from the datasheet. Templates are created on demand."`

	Manufacturer     string `json:"manufacturer,omitempty" jsonschema:"Manufacturer name (e.g. 'Texas Instruments'). Created as a company if missing."`
	MPN              string `json:"MPN,omitempty" jsonschema:"Manufacturer Part Number. Requires manufacturer."`
	ManufacturerLink string `json:"manufacturer_link,omitempty" jsonschema:"URL of the manufacturer product page"`

	Supplier     string `json:"supplier,omitempty" jsonschema:"Supplier/distributor name (e.g. 'LCSC'). Created as a company if missing."`
	SKU          string `json:"SKU,omitempty" jsonschema:"Distributor order code. Requires supplier."`
	SupplierLink string `json:"supplier_link,omitempty" jsonschema:"URL of the supplier product page"`
	Packaging    string `json:"packaging,omitempty" jsonschema:"Packaging the part ships in (e.g. 'Reel', 'Tape')"`
	PackQuantity string `json:"pack_quantity,omitempty" jsonschema:"Quantity per pack, optionally with units"`

	InitialStock     float64 `json:"initial_stock,omitempty" jsonschema:"Quantity of stock to create for this part. 0 or omit to skip."`
	StockLocation    int     `json:"stock_location,omitempty" jsonschema:"Location ID for the initial stock. Falls back to default_location."`
	ReuseExistingSKU *bool   `json:"reuse_existing_sku,omitempty" jsonschema:"When a supplier part with the same SKU already exists, enrich that part instead of creating a duplicate (default true)"`
}

func RegisterIntakePart(server *mcp.Server, c *client.Client, res *paramAPIResolver, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "intake_part",
		Description: "Register a component end to end in a single call: create (or enrich) the part, set its datasheet parameters, record manufacturer + MPN, record supplier + SKU, and optionally book initial stock. " +
			"This is the tool for 'I have an LCSC/Mouser/Digi-Key code, put this component into InvenTree'. " +
			"Companies are looked up by name and created only when missing; an existing supplier part with the same SKU is reused rather than duplicated. " +
			"Steps are independent: if one fails the rest still run and the result lists what succeeded and what did not. " +
			"Pick the category first with list_part_categories — choose the deepest category that fits.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input IntakePartInput) (*mcp.CallToolResult, any, error) {
		report := map[string]any{}
		problems := []string{}

		reuseSKU := input.ReuseExistingSKU == nil || *input.ReuseExistingSKU

		// 1. Resolve the part: explicit ID, existing SKU, or a new record.
		partID := input.Part
		if partID == 0 && reuseSKU && input.SKU != "" {
			if existing, err := findSupplierPartBySKU(c, input.SKU); err != nil {
				problems = append(problems, fmt.Sprintf("looking up SKU %q: %v", input.SKU, err))
			} else if existing != nil {
				partID = existing.Part
				report["part_status"] = "reused_from_sku"
				report["supplier_part"] = existing
			}
		}

		if partID == 0 {
			if strings.TrimSpace(input.Name) == "" {
				return errResult(fmt.Errorf("either part (existing ID) or name (to create a new part) is required")), nil, nil
			}
			created, err := createIntakePart(c, input)
			if err != nil {
				// Without a part nothing else can be attached: fail outright.
				return errResult(err), nil, nil
			}
			partID = created.PK
			report["part_status"] = "created"
			report["part"] = created

			// The image has to go up as file bytes in a second call: the
			// remote_image field was removed from the Part API in v489.
			if input.ImageURL != "" {
				withImage, err := attachPartImage(ctx, c, partID, input.ImageURL)
				if err != nil {
					problems = append(problems, fmt.Sprintf("image: %v", err))
				} else {
					report["part"] = withImage
				}
			}
		} else if _, ok := report["part_status"]; !ok {
			report["part_status"] = "existing"
		}
		report["part_id"] = partID

		// 2. Parameters.
		if len(input.Parameters) > 0 {
			applied, errs := applyParameters(c, res, partID, input.Parameters)
			report["parameters"] = applied
			problems = append(problems, errs...)
		}

		// 3. Manufacturer part.
		var manufacturerPartID int
		if input.Manufacturer != "" && input.MPN != "" {
			mp, status, err := ensureManufacturerPart(c, partID, input)
			if err != nil {
				problems = append(problems, fmt.Sprintf("manufacturer part: %v", err))
			} else {
				manufacturerPartID = mp.PK
				report["manufacturer_part"] = mp
				report["manufacturer_part_status"] = status
			}
		} else if input.MPN != "" {
			problems = append(problems, "MPN given without manufacturer: manufacturer part skipped")
		}

		// 4. Supplier part.
		if input.Supplier != "" && input.SKU != "" {
			sp, status, err := ensureSupplierPart(c, partID, manufacturerPartID, input)
			if err != nil {
				problems = append(problems, fmt.Sprintf("supplier part: %v", err))
			} else {
				report["supplier_part"] = sp
				report["supplier_part_status"] = status
			}
		} else if input.SKU != "" {
			problems = append(problems, "SKU given without supplier: supplier part skipped")
		}

		// 5. Initial stock.
		if input.InitialStock > 0 {
			location := input.StockLocation
			if location == 0 {
				location = input.DefaultLocation
			}
			item, err := createIntakeStock(c, partID, input.InitialStock, location)
			if err != nil {
				problems = append(problems, fmt.Sprintf("initial stock: %v", err))
			} else {
				report["stock_item"] = item
			}
		}

		if len(problems) > 0 {
			report["problems"] = problems
		}
		report["ok"] = len(problems) == 0
		return jsonResult(report)
	})
}

// createIntakePart creates the Part record from the intake input.
func createIntakePart(c *client.Client, input IntakePartInput) (*Part, error) {
	payload := map[string]any{"name": input.Name}
	if input.Description != "" {
		payload["description"] = input.Description
	}
	if input.Category != 0 {
		payload["category"] = input.Category
	}
	if input.IPN != "" {
		payload["IPN"] = input.IPN
	}
	if input.Keywords != "" {
		payload["keywords"] = input.Keywords
	}
	if input.Units != "" {
		payload["units"] = input.Units
	}
	if input.Link != "" {
		payload["link"] = input.Link
	}
	if input.MinimumStock > 0 {
		payload["minimum_stock"] = input.MinimumStock
	}
	if input.DefaultLocation != 0 {
		payload["default_location"] = input.DefaultLocation
	}

	var created Part
	if err := c.Post("/api/part/", payload, &created); err != nil {
		return nil, fmt.Errorf("creating part: %w", err)
	}
	return &created, nil
}

// applyParameters sets each parameter, collecting per-parameter failures
// instead of aborting the whole intake.
func applyParameters(c *client.Client, res *paramAPIResolver, partID int, params []ParameterValueInput) ([]map[string]any, []string) {
	api, err := res.resolve(c)
	if err != nil {
		return nil, []string{fmt.Sprintf("parameters: %v", err)}
	}

	existing, err := api.listForPart(c, partID)
	if err != nil {
		return nil, []string{fmt.Sprintf("parameters: %v", err)}
	}

	applied := make([]map[string]any, 0, len(params))
	problems := []string{}
	for _, p := range params {
		name := strings.TrimSpace(p.Name)
		if name == "" {
			problems = append(problems, "parameter with empty name skipped")
			continue
		}
		tmpl, err := api.findOrCreateTemplate(c, name, p.Units, "", true)
		if err != nil {
			problems = append(problems, fmt.Sprintf("parameter %q: %v", name, err))
			continue
		}
		status, saved, err := api.setValue(c, partID, tmpl.PK, p.Value, p.Note, existing)
		if err != nil {
			problems = append(problems, fmt.Sprintf("parameter %q: %v", name, err))
			continue
		}
		if status == "created" && saved != nil {
			existing = append(existing, *saved)
		}
		applied = append(applied, map[string]any{
			"name":   name,
			"value":  p.Value,
			"status": status,
		})
	}
	return applied, problems
}

// ensureManufacturerPart reuses an existing MPN record for the part or creates one.
func ensureManufacturerPart(c *client.Client, partID int, input IntakePartInput) (*ManufacturerPart, string, error) {
	company, _, err := getOrCreateCompany(c, input.Manufacturer, "", "", false, true, false)
	if err != nil {
		return nil, "", err
	}

	path := fmt.Sprintf("/api/company/part/manufacturer/?part=%d&limit=100&format=json", partID)
	var existing client.PaginatedResponse[ManufacturerPart]
	if err := c.Get(path, &existing); err != nil {
		return nil, "", fmt.Errorf("checking existing manufacturer parts: %w", err)
	}
	for i := range existing.Results {
		if strings.EqualFold(existing.Results[i].MPN, input.MPN) {
			return &existing.Results[i], "existing", nil
		}
	}

	payload := map[string]any{
		"part":         partID,
		"manufacturer": company.PK,
		"MPN":          input.MPN,
	}
	if input.ManufacturerLink != "" {
		payload["link"] = input.ManufacturerLink
	} else if input.Link != "" {
		payload["link"] = input.Link
	}
	if input.Description != "" {
		payload["description"] = input.Description
	}

	var created ManufacturerPart
	if err := c.Post("/api/company/part/manufacturer/", payload, &created); err != nil {
		return nil, "", err
	}
	return &created, "created", nil
}

// ensureSupplierPart reuses an existing SKU record for the supplier or creates one.
func ensureSupplierPart(c *client.Client, partID, manufacturerPartID int, input IntakePartInput) (*SupplierPart, string, error) {
	company, _, err := getOrCreateCompany(c, input.Supplier, "", "", true, false, false)
	if err != nil {
		return nil, "", err
	}

	path := fmt.Sprintf("/api/company/part/?supplier=%d&search=%s&limit=100&format=json",
		company.PK, url.QueryEscape(input.SKU))
	var existing client.PaginatedResponse[SupplierPart]
	if err := c.Get(path, &existing); err != nil {
		return nil, "", fmt.Errorf("checking existing supplier parts: %w", err)
	}
	for i := range existing.Results {
		if strings.EqualFold(existing.Results[i].SKU, input.SKU) {
			return &existing.Results[i], "existing", nil
		}
	}

	payload := map[string]any{
		"part":     partID,
		"supplier": company.PK,
		"SKU":      input.SKU,
	}
	if manufacturerPartID != 0 {
		payload["manufacturer_part"] = manufacturerPartID
	}
	if input.SupplierLink != "" {
		payload["link"] = input.SupplierLink
	}
	if input.Packaging != "" {
		payload["packaging"] = input.Packaging
	}
	if input.PackQuantity != "" {
		payload["pack_quantity"] = input.PackQuantity
	}
	if input.Description != "" {
		payload["description"] = input.Description
	}

	var created SupplierPart
	if err := c.Post("/api/company/part/", payload, &created); err != nil {
		return nil, "", err
	}
	return &created, "created", nil
}

// createIntakeStock books the initial stock quantity for a freshly taken-in part.
func createIntakeStock(c *client.Client, partID int, quantity float64, location int) (*StockItem, error) {
	payload := map[string]any{
		"part":     partID,
		"quantity": quantity,
	}
	if location != 0 {
		payload["location"] = location
	}

	var created []StockItem
	if err := c.Post("/api/stock/", payload, &created); err != nil {
		return nil, err
	}
	if len(created) == 0 {
		return nil, fmt.Errorf("no stock item returned")
	}
	return &created[0], nil
}

// findSupplierPartBySKU returns the supplier part matching an exact SKU, if any.
func findSupplierPartBySKU(c *client.Client, sku string) (*SupplierPart, error) {
	path := fmt.Sprintf("/api/company/part/?search=%s&limit=50&format=json", url.QueryEscape(sku))
	var resp client.PaginatedResponse[SupplierPart]
	if err := c.Get(path, &resp); err != nil {
		return nil, err
	}
	for i := range resp.Results {
		if strings.EqualFold(strings.TrimSpace(resp.Results[i].SKU), strings.TrimSpace(sku)) {
			return &resp.Results[i], nil
		}
	}
	return nil, nil
}
