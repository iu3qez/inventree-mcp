package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/chrisbotelho/inventree-mcp/internal/client"
	"github.com/chrisbotelho/inventree-mcp/internal/coerce"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// SalePriceBreak is a quantity/price tier for selling one Part.
//
// The buying side of this lives on a SupplierPart instead, see
// SupplierPriceBreak in companies.go.
type SalePriceBreak struct {
	PK       int     `json:"pk"`
	Part     int     `json:"part"`
	Quantity float64 `json:"quantity"`
	// Price arrives as a JSON string in some InvenTree versions and a number
	// in others, so it is decoded loosely and echoed back as received.
	Price         any    `json:"price"`
	PriceCurrency string `json:"price_currency"`
}

// -- Get Sale Price Breaks --

type GetSalePriceBreaksInput struct {
	Part int `json:"part" jsonschema:"Part ID (pk) whose sale prices to list"`
}

func RegisterGetSalePriceBreaks(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "get_sale_price_breaks",
		Description: "List the quantity/price tiers a part is sold at. Takes a part pk - unlike get_supplier_price_breaks, " +
			"which takes a supplier part pk, because selling prices hang off the part itself.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input GetSalePriceBreaksInput) (*mcp.CallToolResult, any, error) {
		if input.Part == 0 {
			return errResult(fmt.Errorf("part is required")), nil, nil
		}
		breaks, err := listSalePriceBreaks(c, input.Part)
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(map[string]any{"count": len(breaks), "results": breaks})
	})
}

func listSalePriceBreaks(c *client.Client, partID int) ([]SalePriceBreak, error) {
	path := fmt.Sprintf("/api/part/sale-price/?part=%d&limit=100&format=json", partID)
	var resp client.PaginatedResponse[SalePriceBreak]
	if err := c.Get(path, &resp); err != nil {
		// Reading fails for a part that is not salable, not just writing.
		err = explainQuerysetError(err, "part", partID,
			"sale prices only exist for parts marked salable; call update_part with salable=true first")
		return nil, fmt.Errorf("listing sale price breaks for part %d: %w", partID, err)
	}
	return resp.Results, nil
}

// -- Set Sale Price Break (upsert) --

type SetSalePriceBreakInput struct {
	Part          int     `json:"part" jsonschema:"Part ID (pk) to price. The part must be salable - use update_part with salable=true first if it is not."`
	Quantity      float64 `json:"quantity" jsonschema:"Quantity threshold this price applies from (e.g. 1, 10, 100)"`
	Price         float64 `json:"price" jsonschema:"Unit price at this quantity"`
	PriceCurrency string  `json:"price_currency,omitempty" jsonschema:"ISO currency code, e.g. 'EUR'. Defaults to the instance's currency."`
}

func RegisterSetSalePriceBreak(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "set_sale_price_break",
		Description: "Set the sale price of a part at a given quantity, updating the existing tier at that exact quantity if there is one " +
			"and creating it otherwise. InvenTree enforces one tier per (part, quantity), so a plain create would fail on a repeat call. " +
			"The part must be marked salable - update_part can set that flag.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false)},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input SetSalePriceBreakInput) (*mcp.CallToolResult, any, error) {
		if input.Part == 0 {
			return errResult(fmt.Errorf("part is required")), nil, nil
		}
		if input.Quantity <= 0 {
			return errResult(fmt.Errorf("quantity must be greater than 0")), nil, nil
		}

		existing, err := listSalePriceBreaks(c, input.Part)
		if err != nil {
			return errResult(err), nil, nil
		}
		var match *SalePriceBreak
		for i := range existing {
			if existing[i].Quantity == input.Quantity {
				match = &existing[i]
				break
			}
		}

		payload := map[string]any{"price": input.Price}
		if input.PriceCurrency != "" {
			payload["price_currency"] = input.PriceCurrency
		}

		var result SalePriceBreak
		if match != nil {
			path := fmt.Sprintf("/api/part/sale-price/%d/", match.PK)
			if err := c.Patch(path, payload, &result); err != nil {
				return errResult(fmt.Errorf("updating sale price break %d: %w", match.PK, err)), nil, nil
			}
		} else {
			payload["part"] = input.Part
			payload["quantity"] = input.Quantity
			if err := c.Post("/api/part/sale-price/", payload, &result); err != nil {
				return errResult(explainQuerysetError(err, "part", input.Part,
					"sale prices can only be set on parts marked salable; call update_part with salable=true first")), nil, nil
			}
		}
		return jsonResult(result)
	})
}

// querysetHintMarker introduces the added explanation and marks the error as
// already explained.
const querysetHintMarker = "\n\nNote: "

// explainQuerysetError rewrites InvenTree's two most misleading validation
// errors. Several endpoints restrict a foreign key to a filtered queryset:
// sale prices to salable parts, supplier and manufacturer parts to purchaseable
// ones, and both to companies carrying the matching role flag. An object
// outside that filter is not reported as "wrong flag" but as missing entirely,
// in one of two shapes depending on whether it was written or filtered on:
//
//	POST/PATCH: {"part": ["Invalid pk \"239\" - object does not exist."]}
//	GET ?part=: {"part": ["Select a valid choice. That choice is not one of the available choices."]}
//
// The second one is the nastier of the two, because a plain read of an
// existing part fails with it. Both are verified against InvenTree 1.4.3 /
// API 511. The hint names the flag that is actually missing; the original
// error is kept so nothing is hidden if the pk really is wrong.
func explainQuerysetError(err error, field string, pk int, hint string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	// One error gets one hint: create_manufacturer_part asks about both its
	// fields in turn, and a second answer to an already-answered error would
	// only be a guess.
	if strings.Contains(msg, querysetHintMarker) {
		return err
	}
	// The rejected field appears as a JSON key in the response body. Matching
	// the quoted key rather than the bare word matters: the surrounding error
	// text says things like "listing supplier parts", which contains "part".
	if !strings.Contains(msg, `"`+field+`"`) {
		return err
	}
	rejected := strings.Contains(msg, fmt.Sprintf(`Invalid pk \"%d\"`, pk)) ||
		strings.Contains(msg, fmt.Sprintf(`Invalid pk "%d"`, pk)) ||
		strings.Contains(msg, "Select a valid choice")
	if !rejected {
		return err
	}
	return fmt.Errorf("%w"+querysetHintMarker+"%s %d most likely exists - %s. InvenTree reports an object outside the "+
		"endpoint's filtered queryset as if it did not exist at all, on reads as well as writes", err, field, pk, hint)
}

// -- Supplier Price Breaks (buying side) --

// SupplierPriceBreak is a quantity/price tier for one SupplierPart.
type SupplierPriceBreak struct {
	PK       int     `json:"pk"`
	Part     int     `json:"part"` // SupplierPart pk, NOT Part pk — InvenTree's own naming
	Quantity float64 `json:"quantity"`
	// Price arrives as a JSON string in some InvenTree versions and a number
	// in others, so it is decoded loosely and echoed back as received.
	Price         any    `json:"price"`
	PriceCurrency string `json:"price_currency"`
	Supplier      int    `json:"supplier"`
	Updated       string `json:"updated"`
}

type GetSupplierPriceBreaksInput struct {
	SupplierPart int `json:"supplier_part" jsonschema:"Supplier part ID (pk) - from search_supplier_parts or get_part_sourcing, NOT the part ID"`
}

func RegisterGetSupplierPriceBreaks(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "get_supplier_price_breaks",
		Description: "List the quantity/price tiers of one supplier part. Takes a supplier part pk (from search_supplier_parts or get_part_sourcing), not a part pk - " +
			"InvenTree calls this field 'part' in its own API, which is a well-worn trap.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input GetSupplierPriceBreaksInput) (*mcp.CallToolResult, any, error) {
		if input.SupplierPart == 0 {
			return errResult(fmt.Errorf("supplier_part is required")), nil, nil
		}
		breaks, err := listPriceBreaks(c, input.SupplierPart)
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(map[string]any{"count": len(breaks), "results": breaks})
	})
}

func listPriceBreaks(c *client.Client, supplierPartID int) ([]SupplierPriceBreak, error) {
	path := fmt.Sprintf("/api/company/price-break/?part=%d&limit=100&format=json", supplierPartID)
	var resp client.PaginatedResponse[SupplierPriceBreak]
	if err := c.Get(path, &resp); err != nil {
		return nil, fmt.Errorf("listing price breaks for supplier part %d: %w", supplierPartID, err)
	}
	return resp.Results, nil
}

// -- Set Supplier Price Break (upsert) --

type SetSupplierPriceBreakInput struct {
	SupplierPart  int     `json:"supplier_part" jsonschema:"Supplier part ID (pk) - from search_supplier_parts or get_part_sourcing, NOT the part ID"`
	Quantity      float64 `json:"quantity" jsonschema:"Quantity threshold this price applies from (e.g. 1, 10, 100)"`
	Price         float64 `json:"price" jsonschema:"Unit price at this quantity"`
	PriceCurrency string  `json:"price_currency,omitempty" jsonschema:"ISO currency code, e.g. 'EUR'. Defaults to the instance's currency."`
}

func RegisterSetSupplierPriceBreak(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "set_supplier_price_break",
		Description: "Set the price of a supplier part at a given quantity, updating the existing tier at that exact quantity if there is one " +
			"and creating it otherwise. Safe to call repeatedly when refreshing prices from a supplier feed - it will not pile up duplicate tiers.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(false)},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input SetSupplierPriceBreakInput) (*mcp.CallToolResult, any, error) {
		if input.SupplierPart == 0 {
			return errResult(fmt.Errorf("supplier_part is required")), nil, nil
		}
		if input.Quantity <= 0 {
			return errResult(fmt.Errorf("quantity must be greater than 0")), nil, nil
		}

		existing, err := listPriceBreaks(c, input.SupplierPart)
		if err != nil {
			return errResult(err), nil, nil
		}
		var match *SupplierPriceBreak
		for i := range existing {
			if existing[i].Quantity == input.Quantity {
				match = &existing[i]
				break
			}
		}

		payload := map[string]any{"price": input.Price}
		if input.PriceCurrency != "" {
			payload["price_currency"] = input.PriceCurrency
		}

		var result SupplierPriceBreak
		if match != nil {
			path := fmt.Sprintf("/api/company/price-break/%d/", match.PK)
			if err := c.Patch(path, payload, &result); err != nil {
				return errResult(fmt.Errorf("updating price break %d: %w", match.PK, err)), nil, nil
			}
		} else {
			payload["part"] = input.SupplierPart
			payload["quantity"] = input.Quantity
			if err := c.Post("/api/company/price-break/", payload, &result); err != nil {
				return errResult(fmt.Errorf("creating price break: %w", err)), nil, nil
			}
		}
		return jsonResult(result)
	})
}
