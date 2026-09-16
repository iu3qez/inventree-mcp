package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/chrisbotelho/inventree-mcp/internal/client"
	"github.com/chrisbotelho/inventree-mcp/internal/coerce"
	"github.com/chrisbotelho/inventree-mcp/internal/imagesearch"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Part represents an InvenTree part.
type Part struct {
	PK           int      `json:"pk"`
	Name         string   `json:"name"`
	FullName     string   `json:"full_name"`
	Description  string   `json:"description"`
	Category     *int     `json:"category"`
	CategoryName string   `json:"category_name"`
	Active       bool     `json:"active"`
	InStock      float64  `json:"in_stock"`
	TotalInStock float64  `json:"total_in_stock"`
	MinimumStock float64  `json:"minimum_stock"`
	Units        string   `json:"units"`
	IPN          string   `json:"IPN"`
	Keywords     *string  `json:"keywords"`
	Link         *string  `json:"link"`
	Purchaseable bool     `json:"purchaseable"`
	Assembly     bool     `json:"assembly"`
	Component    bool     `json:"component"`
	Trackable    bool     `json:"trackable"`
	Virtual      bool     `json:"virtual"`
	Salable      bool     `json:"salable"`
	Revision     string   `json:"revision"`
	Image        *string  `json:"image"`
	Thumbnail    *string  `json:"thumbnail"`
	Tags         []string `json:"tags"`

	// DefaultLocation is the stock location new stock of this part is
	// assigned to by default.
	DefaultLocation *int `json:"default_location"`
}

// -- Search Parts --

type SearchPartsInput struct {
	Search string `json:"search" jsonschema:"Search query to find parts by name or keyword"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Maximum number of results to return (default 25)"`
}

func RegisterSearchParts(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name:        "search_parts",
		Description: "Search for parts by name, keyword, or description. Use this to find existing parts before creating new ones. Returns matching parts with their IDs, names, categories, and stock levels.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input SearchPartsInput) (*mcp.CallToolResult, any, error) {
		limit := input.Limit
		if limit <= 0 {
			limit = 25
		}
		path := fmt.Sprintf("/api/part/?search=%s&limit=%d&tags=true&format=json", url.QueryEscape(input.Search), limit)
		var resp client.PaginatedResponse[Part]
		if err := c.Get(path, &resp); err != nil {
			return errResult(fmt.Errorf("searching parts: %w", err)), nil, nil
		}
		return jsonResult(map[string]any{
			"count":   resp.Count,
			"results": resp.Results,
		})
	})
}

// -- Get Part --

type GetPartInput struct {
	ID int `json:"id" jsonschema:"The part ID (pk) to retrieve"`
}

func RegisterGetPart(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name:        "get_part",
		Description: "Get detailed information about a specific part by its ID.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input GetPartInput) (*mcp.CallToolResult, any, error) {
		path := fmt.Sprintf("/api/part/%d/?tags=true&format=json", input.ID)
		var part Part
		if err := c.Get(path, &part); err != nil {
			return errResult(fmt.Errorf("getting part %d: %w", input.ID, err)), nil, nil
		}
		return jsonResult(part)
	})
}

// -- Create Part --

type CreatePartInput struct {
	Name         string   `json:"name" jsonschema:"Part name (required)"`
	Description  string   `json:"description,omitempty" jsonschema:"Part description"`
	Category     int      `json:"category,omitempty" jsonschema:"Category ID for the part. 0 or omit for uncategorized."`
	IPN          string   `json:"IPN,omitempty" jsonschema:"Internal Part Number"`
	Keywords     string   `json:"keywords,omitempty" jsonschema:"Keywords for search"`
	Units        string   `json:"units,omitempty" jsonschema:"Units of measure"`
	MinimumStock int      `json:"minimum_stock,omitempty" jsonschema:"Minimum stock level"`
	Purchaseable *bool    `json:"purchaseable,omitempty" jsonschema:"Whether the part can be purchased. Omit to use the instance default (the PART_PURCHASEABLE setting)."`
	Component    *bool    `json:"component,omitempty" jsonschema:"Whether the part is a component (default true)"`
	Assembly     *bool    `json:"assembly,omitempty" jsonschema:"Whether the part is an assembly"`
	Trackable    *bool    `json:"trackable,omitempty" jsonschema:"Whether the part is trackable by serial number"`
	Virtual      *bool    `json:"virtual,omitempty" jsonschema:"Whether the part is virtual (not physical)"`
	Salable      *bool    `json:"salable,omitempty" jsonschema:"Whether the part can be sold to customers"`
	ImageURL     string   `json:"image_url,omitempty" jsonschema:"URL of an image to attach to the part. Downloaded by this MCP server and uploaded as file bytes after the part is created."`
	Tags         []string `json:"tags,omitempty" jsonschema:"Tags to attach to the part, e.g. [\"recommended\"]"`

	Link            string `json:"link,omitempty" jsonschema:"External URL for this part, typically the datasheet"`
	DefaultLocation int    `json:"default_location,omitempty" jsonschema:"Default stock location ID for new stock of this part. 0 or omit for none."`
	Revision        string `json:"revision,omitempty" jsonschema:"Part revision designator"`
}

func RegisterCreatePart(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name:        "create_part",
		Description: "Create a new part in InvenTree. Returns the created part with its ID. IMPORTANT workflow: (1) search_parts to check for duplicates, (2) list_part_categories to see the FULL category hierarchy (check pathstring fields to understand nesting), (3) find the deepest, most specific category that fits this part — categories can be nested many levels deep (e.g. Electronic Components/Resistors/Through Hole/1/8 Watt), so always prefer the most specific match, (4) if no suitable category exists, create one with create_part_category under the most appropriate parent — do NOT put a part in an unrelated category just because it exists, and do NOT create top-level categories when the part belongs under an existing parent, (5) create the part with the correct category ID. Parts should always have a category.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input CreatePartInput) (*mcp.CallToolResult, any, error) {
		payload := map[string]any{
			"name": input.Name,
		}
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
		if input.MinimumStock > 0 {
			payload["minimum_stock"] = input.MinimumStock
		}
		if input.Purchaseable != nil {
			payload["purchaseable"] = *input.Purchaseable
		}
		if input.Component != nil {
			payload["component"] = *input.Component
		}
		if input.Assembly != nil {
			payload["assembly"] = *input.Assembly
		}
		if input.Trackable != nil {
			payload["trackable"] = *input.Trackable
		}
		if input.Virtual != nil {
			payload["virtual"] = *input.Virtual
		}
		if input.Salable != nil {
			payload["salable"] = *input.Salable
		}
		if input.Link != "" {
			payload["link"] = input.Link
		}
		if input.DefaultLocation != 0 {
			payload["default_location"] = input.DefaultLocation
		}
		if input.Revision != "" {
			payload["revision"] = input.Revision
		}
		if len(input.Tags) > 0 {
			payload["tags"] = input.Tags
		}

		var created Part
		if err := c.Post("/api/part/", payload, &created); err != nil {
			return errResult(fmt.Errorf("creating part: %w", err)), nil, nil
		}
		if input.ImageURL != "" {
			withImage, err := attachPartImage(ctx, c, created.PK, input.ImageURL)
			if err != nil {
				// The part itself exists, so this is not a failed call - but
				// it must not pass silently either.
				return jsonResult(map[string]any{"part": created, "image_error": err.Error()})
			}
			created = withImage
		}
		return jsonResult(created)
	})
}

// -- Update Part --

type UpdatePartInput struct {
	ID           int       `json:"id" jsonschema:"The part ID (pk) to update"`
	Name         string    `json:"name,omitempty" jsonschema:"New part name"`
	Description  string    `json:"description,omitempty" jsonschema:"New description"`
	Category     int       `json:"category,omitempty" jsonschema:"New category ID. 0 or omit to leave unchanged."`
	Active       *bool     `json:"active,omitempty" jsonschema:"Whether the part is active"`
	IPN          string    `json:"IPN,omitempty" jsonschema:"New Internal Part Number"`
	Keywords     string    `json:"keywords,omitempty" jsonschema:"New keywords"`
	Units        string    `json:"units,omitempty" jsonschema:"New units of measure"`
	MinimumStock int       `json:"minimum_stock,omitempty" jsonschema:"New minimum stock level. 0 or omit to leave unchanged."`
	ImageURL     string    `json:"image_url,omitempty" jsonschema:"URL of an image to set for this part. Downloaded by this MCP server and uploaded as file bytes."`
	Tags         *[]string `json:"tags,omitempty" jsonschema:"Replacement list of tags. This REPLACES the existing tags rather than adding to them; pass an empty list to clear them. Omit to leave unchanged."`

	Link            string `json:"link,omitempty" jsonschema:"New external URL for this part, typically the datasheet"`
	DefaultLocation int    `json:"default_location,omitempty" jsonschema:"New default stock location ID. 0 or omit to leave unchanged."`
	Revision        string `json:"revision,omitempty" jsonschema:"New part revision designator"`
	Purchaseable    *bool  `json:"purchaseable,omitempty" jsonschema:"Whether the part can be purchased"`
	Component       *bool  `json:"component,omitempty" jsonschema:"Whether the part is a component"`
	Assembly        *bool  `json:"assembly,omitempty" jsonschema:"Whether the part is an assembly"`
	Trackable       *bool  `json:"trackable,omitempty" jsonschema:"Whether the part is trackable by serial number"`
	Virtual         *bool  `json:"virtual,omitempty" jsonschema:"Whether the part is virtual (not physical)"`
	Salable         *bool  `json:"salable,omitempty" jsonschema:"Whether the part can be sold to customers"`
}

func RegisterUpdatePart(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name:        "update_part",
		Description: "Update an existing part's fields. Only provided fields are changed. Use this to rename parts, change categories, update descriptions, or deactivate parts.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input UpdatePartInput) (*mcp.CallToolResult, any, error) {
		payload := map[string]any{}
		if input.Name != "" {
			payload["name"] = input.Name
		}
		if input.Description != "" {
			payload["description"] = input.Description
		}
		if input.Category != 0 {
			payload["category"] = input.Category
		}
		if input.Active != nil {
			payload["active"] = *input.Active
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
		if input.MinimumStock != 0 {
			payload["minimum_stock"] = input.MinimumStock
		}
		if input.Link != "" {
			payload["link"] = input.Link
		}
		if input.DefaultLocation != 0 {
			payload["default_location"] = input.DefaultLocation
		}
		if input.Revision != "" {
			payload["revision"] = input.Revision
		}
		if input.Purchaseable != nil {
			payload["purchaseable"] = *input.Purchaseable
		}
		if input.Component != nil {
			payload["component"] = *input.Component
		}
		if input.Assembly != nil {
			payload["assembly"] = *input.Assembly
		}
		if input.Trackable != nil {
			payload["trackable"] = *input.Trackable
		}
		if input.Virtual != nil {
			payload["virtual"] = *input.Virtual
		}
		if input.Salable != nil {
			payload["salable"] = *input.Salable
		}
		if input.Tags != nil {
			payload["tags"] = *input.Tags
		}

		if len(payload) == 0 && input.ImageURL == "" {
			return errResult(fmt.Errorf("no fields to update")), nil, nil
		}

		var updated Part
		path := fmt.Sprintf("/api/part/%d/", input.ID)
		if len(payload) > 0 {
			if err := c.Patch(path, payload, &updated); err != nil {
				return errResult(fmt.Errorf("updating part %d: %w", input.ID, err)), nil, nil
			}
		}
		if input.ImageURL != "" {
			withImage, err := attachPartImage(ctx, c, input.ID, input.ImageURL)
			if err != nil {
				if len(payload) == 0 {
					// The image was the whole request, so its failure is the
					// call's failure.
					return errResult(err), nil, nil
				}
				return jsonResult(map[string]any{"part": updated, "image_error": err.Error()})
			}
			updated = withImage
		}
		return jsonResult(updated)
	})
}

// -- Delete Part --

type DeletePartInput struct {
	ID int `json:"id" jsonschema:"The part ID (pk) to delete"`
}

func RegisterDeletePart(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name:        "delete_part",
		Description: "Delete a part from InvenTree. This automatically deactivates the part first (required by InvenTree). The part must have no stock items. This is destructive and cannot be undone.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(true),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input DeletePartInput) (*mcp.CallToolResult, any, error) {
		path := fmt.Sprintf("/api/part/%d/", input.ID)
		// InvenTree requires parts to be inactive before deletion
		if err := c.Patch(path, map[string]any{"active": false}, nil); err != nil {
			return errResult(fmt.Errorf("deactivating part %d before delete: %w", input.ID, err)), nil, nil
		}
		if err := c.Delete(path); err != nil {
			return errResult(fmt.Errorf("deleting part %d: %w", input.ID, err)), nil, nil
		}
		return textResult(fmt.Sprintf("Part %d deleted successfully.", input.ID))
	})
}

// -- List Parts --

type ListPartsInput struct {
	Category int `json:"category,omitempty" jsonschema:"Filter by category ID. 0 or omit to list all."`
	Limit    int `json:"limit,omitempty" jsonschema:"Maximum number of results (default 50)"`
	Offset   int `json:"offset,omitempty" jsonschema:"Offset for pagination"`
}

func RegisterListParts(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name:        "list_parts",
		Description: "List all parts, optionally filtered by category. Use search_parts for finding specific parts by name.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input ListPartsInput) (*mcp.CallToolResult, any, error) {
		limit := input.Limit
		if limit <= 0 {
			limit = 50
		}
		path := fmt.Sprintf("/api/part/?limit=%d&offset=%d&tags=true&format=json", limit, input.Offset)
		if input.Category != 0 {
			path += fmt.Sprintf("&category=%d", input.Category)
		}

		var resp client.PaginatedResponse[Part]
		if err := c.Get(path, &resp); err != nil {
			return errResult(fmt.Errorf("listing parts: %w", err)), nil, nil
		}
		return jsonResult(map[string]any{
			"count":   resp.Count,
			"results": resp.Results,
		})
	})
}

// -- Set Part Image --

type SetPartImageInput struct {
	ID       int    `json:"id" jsonschema:"The part ID (pk) to set the image for"`
	ImageURL string `json:"image_url" jsonschema:"URL of the image. Downloaded by this MCP server and uploaded to InvenTree as file bytes."`
}

func RegisterSetPartImage(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "set_part_image",
		Description: "Set or replace a part's image by URL. Use this after search_part_images. The image is downloaded here and " +
			"uploaded to InvenTree as file bytes, which is the only mechanism InvenTree 1.x still has - see upload_part_image, " +
			"which does the same thing under a name that says so.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input SetPartImageInput) (*mcp.CallToolResult, any, error) {
		if input.ImageURL == "" {
			return errResult(fmt.Errorf("image_url is required")), nil, nil
		}
		updated, err := attachPartImage(ctx, c, input.ID, input.ImageURL)
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(updated)
	})
}

// -- Upload Part Image --

type UploadPartImageInput struct {
	ID       int    `json:"id" jsonschema:"The part ID (pk) to set the image for"`
	ImageURL string `json:"image_url" jsonschema:"URL of the image. Fetched by this MCP server and uploaded to InvenTree as multipart/form-data."`
}

func RegisterUploadPartImage(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "upload_part_image",
		Description: "Set a part's image by downloading it here and uploading the bytes to InvenTree. Identical to set_part_image; " +
			"both exist because InvenTree used to offer a second, server-side mechanism that 1.x removed.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input UploadPartImageInput) (*mcp.CallToolResult, any, error) {
		if input.ImageURL == "" {
			return errResult(fmt.Errorf("image_url is required")), nil, nil
		}
		updated, err := attachPartImage(ctx, c, input.ID, input.ImageURL)
		if err != nil {
			return errResult(err), nil, nil
		}
		return jsonResult(updated)
	})
}

// attachPartImage downloads an image and PATCHes the bytes onto a part as
// multipart/form-data. This is the only way to set a part image on InvenTree
// 1.x: the `remote_image` field that asked the server to fetch a URL itself
// was removed (verified against 1.4.3 / API 511 - the field is absent from
// OPTIONS and from the serializer, and the global setting that used to gate
// it, INVENTREE_DOWNLOAD_FROM_URL, 404s). Since DRF drops unknown keys
// without complaint, writing it returned HTTP 200 and left image null, which
// is why every image tool here goes through the upload instead.
func attachPartImage(ctx context.Context, c *client.Client, partPK int, imageURL string) (Part, error) {
	var updated Part

	content, contentType, err := fetchImage(ctx, imageURL)
	if err != nil {
		return updated, err
	}

	file := client.MultipartFile{
		FieldName:   "image",
		FileName:    imageFileName(imageURL, contentType),
		Content:     content,
		ContentType: contentType,
	}
	path := fmt.Sprintf("/api/part/%d/", partPK)
	if err := c.PatchMultipart(path, nil, []client.MultipartFile{file}, &updated); err != nil {
		return updated, fmt.Errorf("uploading image for part %d: %w", partPK, err)
	}
	if updated.Image == nil || *updated.Image == "" {
		return updated, fmt.Errorf("upload for part %d was accepted but the part still has no image", partPK)
	}
	return updated, nil
}

// maxImageBytes caps what will be pulled into memory for an upload. Part
// images are product photos; anything past this is a wrong URL.
const maxImageBytes = 25 << 20 // 25 MiB

func fetchImage(ctx context.Context, rawURL string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("building request for %q: %w", rawURL, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("fetching %q: %w", rawURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("fetching %q: HTTP %d", rawURL, resp.StatusCode)
	}

	content, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("reading %q: %w", rawURL, err)
	}
	if len(content) == 0 {
		return nil, "", fmt.Errorf("fetching %q: empty response body", rawURL)
	}
	if len(content) > maxImageBytes {
		return nil, "", fmt.Errorf("fetching %q: image exceeds the %d MiB limit", rawURL, maxImageBytes>>20)
	}

	// Trust what the bytes actually are over what the server claims, since
	// the file name and the multipart part header are both derived from it.
	contentType := http.DetectContentType(content)
	if !strings.HasPrefix(contentType, "image/") {
		return nil, "", fmt.Errorf("fetching %q: content is %s, not an image", rawURL, contentType)
	}
	return content, contentType, nil
}

// imageExtensions pins the extension for the common image types rather than
// taking mime.ExtensionsByType's first entry, which is alphabetical and
// depends on the host's mime database - it yields ".jfif" for image/jpeg on
// a stock Debian, which is valid but surprising in a file listing.
var imageExtensions = map[string]string{
	"image/jpeg":    ".jpg",
	"image/png":     ".png",
	"image/gif":     ".gif",
	"image/webp":    ".webp",
	"image/svg+xml": ".svg",
	"image/bmp":     ".bmp",
	"image/tiff":    ".tiff",
}

// imageFileName derives an upload file name from the URL, falling back to the
// detected content type. InvenTree stores files under its own generated name,
// so this only needs a sane extension.
func imageFileName(rawURL, contentType string) string {
	name := "image"
	if u, err := url.Parse(rawURL); err == nil {
		if base := path.Base(u.Path); base != "" && base != "." && base != "/" {
			name = base
		}
	}
	if path.Ext(name) != "" {
		return name
	}
	// Content types can carry parameters, e.g. "image/jpeg; charset=binary".
	mediaType := contentType
	if parsed, _, err := mime.ParseMediaType(contentType); err == nil {
		mediaType = parsed
	}
	if ext, ok := imageExtensions[mediaType]; ok {
		return name + ext
	}
	if exts, err := mime.ExtensionsByType(mediaType); err == nil && len(exts) > 0 {
		return name + exts[0]
	}
	return name + ".img"
}

// -- Search Part Images --

type SearchPartImagesInput struct {
	Query string `json:"query" jsonschema:"Search query for finding product images (e.g. part name, manufacturer part number)"`
	Num   int    `json:"num,omitempty" jsonschema:"Number of results to return (1-10, default 5)"`
}

func RegisterSearchPartImages(server *mcp.Server, imgClient *imagesearch.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "search_part_images",
		Description: "Search for product images using Google Custom Search. Returns image URLs that can be used with create_part (image_url field) or set_part_image. " +
			"Decision logic: If the top result clearly matches the part (e.g. exact product photo from manufacturer or distributor), use it directly. " +
			"If unsure, present all results to the user and let them choose. " +
			"Tip: include the manufacturer name or 'datasheet' in the query for better results. " +
			"Requires GOOGLE_API_KEY and GOOGLE_CSE_ID environment variables to be configured.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: true,
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input SearchPartImagesInput) (*mcp.CallToolResult, any, error) {
		if imgClient == nil {
			return errResult(fmt.Errorf("image search is not configured: set GOOGLE_API_KEY and GOOGLE_CSE_ID environment variables")), nil, nil
		}
		if input.Query == "" {
			return errResult(fmt.Errorf("query is required")), nil, nil
		}
		num := input.Num
		if num <= 0 {
			num = 5
		}
		results, err := imgClient.Search(input.Query, num)
		if err != nil {
			return errResult(fmt.Errorf("image search failed: %w", err)), nil, nil
		}
		if len(results) == 0 {
			return textResult("No images found for query: " + input.Query)
		}
		return jsonResult(map[string]any{
			"query":   input.Query,
			"count":   len(results),
			"results": results,
		})
	})
}

// -- helpers shared by all tool files --

func errResult(err error) *mcp.CallToolResult {
	r := &mcp.CallToolResult{}
	r.SetError(err)
	return r
}

func textResult(text string) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}, nil, nil
}

func jsonResult(v any) (*mcp.CallToolResult, any, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return errResult(fmt.Errorf("marshaling result: %w", err)), nil, nil
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(b)}},
	}, nil, nil
}

func boolPtr(b bool) *bool { return &b }

// countOf renders "1 stock item" / "3 stock items".
func countOf(n int, singular, plural string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, singular)
	}
	return fmt.Sprintf("%d %s", n, plural)
}

// deletionSummary spells out what became of the contents of a container
// InvenTree has just deleted. InvenTree removes the container whether or not
// it is empty, so the contents were either deleted alongside it or moved up to
// its parent - and the caller should not have to guess which.
func deletionSummary(deleted, moved []string, destination string) string {
	var s string
	if len(deleted) > 0 {
		s += fmt.Sprintf(" Deleted with it: %s.", strings.Join(deleted, " and "))
	}
	if len(moved) > 0 {
		s += fmt.Sprintf(" Moved to %s: %s.", destination, strings.Join(moved, " and "))
	}
	return s

}
