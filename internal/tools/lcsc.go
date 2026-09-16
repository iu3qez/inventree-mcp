package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/chrisbotelho/inventree-mcp/internal/client"
	"github.com/chrisbotelho/inventree-mcp/internal/coerce"
	"github.com/chrisbotelho/inventree-mcp/internal/lcsc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// -- LCSC Get Product --

type LCSCGetProductInput struct {
	Code string `json:"code" jsonschema:"LCSC part number, e.g. 'C8574' - the code printed on LCSC bags and used in JLCPCB BOMs. The leading C is optional."`
}

func RegisterLCSCGetProduct(server *mcp.Server, c *client.Client, lc *lcsc.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "lcsc_get_product",
		Description: "Look up a component on LCSC by its C-number and return MPN, manufacturer, description, package, category, datasheet, image, stock, " +
			"price breaks (always USD) and datasheet parameters. It also reports the InvenTree supplier part already using this code as SKU, if any: " +
			"when one exists the component is already registered and must not be created again. " +
			"To register a new one, pass the data to intake_part: SKU=lcsc_code, MPN, manufacturer, description, link=datasheet_url, " +
			"supplier_link=product_url, image_url, and as supplier the LCSC company already in InvenTree (find it with search_companies). " +
			"Do not pass the parameters through as they are: LCSC names them verbosely (e.g. 'Collector - Emitter Voltage VCEO'), " +
			"so map each onto an existing template from list_parameter_templates and drop the ones that have none, or every LCSC label becomes a new template. " +
			"Relies on LCSC's undocumented website endpoints, which can change without notice.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(true),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input LCSCGetProductInput) (*mcp.CallToolResult, any, error) {
		if lc == nil {
			return errResult(fmt.Errorf("LCSC lookup is not available")), nil, nil
		}
		code := lcsc.NormalizeCode(input.Code)
		if code == "" {
			return errResult(fmt.Errorf("code is required")), nil, nil
		}

		product, err := lc.Product(ctx, code)
		if errors.Is(err, lcsc.ErrNotFound) {
			return errResult(fmt.Errorf("LCSC has no product with code %s", code)), nil, nil
		}
		if err != nil {
			return errResult(fmt.Errorf("looking up %s on LCSC: %w", code, err)), nil, nil
		}

		result := map[string]any{"product": product}
		// Checked here rather than left to the caller, so an LCSC lookup cannot
		// lead straight to a duplicate part.
		existing, err := findSupplierPartBySKU(c, product.Code)
		switch {
		case err != nil:
			result["inventree_check_failed"] = fmt.Sprintf("could not check InvenTree for SKU %s: %v - run search_supplier_parts before creating anything", product.Code, err)
		case existing != nil:
			result["inventree_supplier_part"] = existing
		default:
			result["inventree_supplier_part"] = nil
		}
		return jsonResult(result)
	})
}

// -- LCSC Search --

type LCSCSearchInput struct {
	Keyword string `json:"keyword" jsonschema:"MPN, LCSC code or free-text description (e.g. 'BC847', 'AMS1117-3.3', '10k 0603 1%')"`
	Limit   int    `json:"limit,omitempty" jsonschema:"Maximum number of results (default 10, at most 25)"`
}

func RegisterLCSCSearch(server *mcp.Server, lc *lcsc.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "lcsc_search",
		Description: "Search the LCSC catalogue by MPN, LCSC code or description and return one compact line per product: code, MPN, manufacturer, " +
			"description, package, stock and the lowest-quantity price (USD). direct_match is set when LCSC recognised the keyword as one exact product. " +
			"Use it to find the LCSC code for a part, then call lcsc_get_product on the chosen code for the full details. " +
			"Relies on LCSC's undocumented website endpoints, which can change without notice.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(true),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input LCSCSearchInput) (*mcp.CallToolResult, any, error) {
		if lc == nil {
			return errResult(fmt.Errorf("LCSC lookup is not available")), nil, nil
		}
		keyword := strings.TrimSpace(input.Keyword)
		if keyword == "" {
			return errResult(fmt.Errorf("keyword is required")), nil, nil
		}

		result, err := lc.Search(ctx, keyword, input.Limit)
		if err != nil {
			return errResult(fmt.Errorf("searching LCSC for %q: %w", keyword, err)), nil, nil
		}
		return jsonResult(result)
	})
}
