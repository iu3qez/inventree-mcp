package tools

import (
	"github.com/chrisbotelho/inventree-mcp/internal/client"
	"github.com/chrisbotelho/inventree-mcp/internal/coerce"
	"github.com/chrisbotelho/inventree-mcp/internal/imagesearch"
	"github.com/chrisbotelho/inventree-mcp/internal/lcsc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// RegisterAll registers all InvenTree MCP tools with the server and returns
// a coerce.Registry populated with the schema types for each tool. Use the
// registry to install coercion middleware via registry.Middleware().
// imgClient may be nil if image search is not configured, and lcscClient may
// be nil where LCSC must not be reached (tests).
func RegisterAll(server *mcp.Server, c *client.Client, imgClient *imagesearch.Client, lcscClient *lcsc.Client) *coerce.Registry {
	r := coerce.NewRegistry()

	// Parts
	RegisterSearchParts(server, c, r)
	RegisterGetPart(server, c, r)
	RegisterCreatePart(server, c, r)
	RegisterUpdatePart(server, c, r)
	RegisterDeletePart(server, c, r)
	RegisterListParts(server, c, r)
	RegisterSetPartImage(server, c, r)
	RegisterUploadPartImage(server, c, r)
	RegisterSearchPartImages(server, imgClient, r)

	// Stock
	RegisterGetStock(server, c, r)
	RegisterGetStockItem(server, c, r)
	RegisterAddStock(server, c, r)
	RegisterStockAdd(server, c, r)
	RegisterStockRemove(server, c, r)
	RegisterStockTransfer(server, c, r)
	RegisterDeleteStockItem(server, c, r)
	RegisterGetStockHistory(server, c, r)

	// Locations
	RegisterSearchLocations(server, c, r)
	RegisterGetLocation(server, c, r)
	RegisterListLocations(server, c, r)
	RegisterCreateLocation(server, c, r)
	RegisterUpdateLocation(server, c, r)
	RegisterDeleteLocation(server, c, r)

	// Categories
	RegisterSearchCategories(server, c, r)
	RegisterListCategories(server, c, r)
	RegisterCreateCategory(server, c, r)
	RegisterUpdateCategory(server, c, r)
	RegisterDeleteCategory(server, c, r)

	// Parameters. The resolver is shared so the parameter API flavour
	// (generic vs legacy) is probed once per server, not once per tool.
	paramRes := newParamAPIResolver()
	RegisterGetPartParameters(server, c, paramRes, r)
	RegisterSetPartParameters(server, c, paramRes, r)
	RegisterListParameterTemplates(server, c, paramRes, r)

	// Companies, manufacturer parts and supplier parts
	RegisterSearchCompanies(server, c, r)
	RegisterGetOrCreateCompany(server, c, r)
	RegisterCreateManufacturerPart(server, c, r)
	RegisterCreateSupplierPart(server, c, r)
	RegisterSearchSupplierParts(server, c, r)
	RegisterGetPartSourcing(server, c, r)

	// Pricing
	RegisterGetSupplierPriceBreaks(server, c, r)
	RegisterSetSupplierPriceBreak(server, c, r)
	RegisterGetSalePriceBreaks(server, c, r)
	RegisterSetSalePriceBreak(server, c, r)

	// End-to-end component intake
	RegisterIntakePart(server, c, paramRes, r)

	// LCSC catalogue lookup, the data source for intake from an LCSC code
	RegisterLCSCGetProduct(server, c, lcscClient, r)
	RegisterLCSCSearch(server, lcscClient, r)

	return r
}
