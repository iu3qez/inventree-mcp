package tools

import (
	"context"
	"fmt"
	"net/url"

	"github.com/chrisbotelho/inventree-mcp/internal/client"
	"github.com/chrisbotelho/inventree-mcp/internal/coerce"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// PartCategory represents an InvenTree part category.
type PartCategory struct {
	PK              int    `json:"pk"`
	Name            string `json:"name"`
	Description     string `json:"description"`
	Parent          *int   `json:"parent"`
	PathString      string `json:"pathstring"`
	Level           int    `json:"level"`
	PartCount       int    `json:"part_count"`
	Subcategories   int    `json:"subcategories"`
	Starred         bool   `json:"starred"`
	Structural      bool   `json:"structural"`
	Icon            string `json:"icon"`
	DefaultLocation *int   `json:"default_location"`
}

// -- Search Part Categories --

type SearchCategoriesInput struct {
	Search string `json:"search" jsonschema:"Search query to find categories by name"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Maximum number of results (default 25)"`
}

func RegisterSearchCategories(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name:        "search_part_categories",
		Description: "Search for part categories by name. Use this to find the right category when creating parts. Returns categories with their full path (e.g., 'Electronic Components/Resistors/Through Hole').",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input SearchCategoriesInput) (*mcp.CallToolResult, any, error) {
		limit := input.Limit
		if limit <= 0 {
			limit = 25
		}
		path := fmt.Sprintf("/api/part/category/?search=%s&limit=%d&format=json", url.QueryEscape(input.Search), limit)
		var resp client.PaginatedResponse[PartCategory]
		if err := c.Get(path, &resp); err != nil {
			return errResult(fmt.Errorf("searching categories: %w", err)), nil, nil
		}
		return jsonResult(map[string]any{
			"count":   resp.Count,
			"results": resp.Results,
		})
	})
}

// -- List Part Categories --

type ListCategoriesInput struct {
	Parent int `json:"parent,omitempty" jsonschema:"Filter by parent category ID. 0 or omit to list all."`
	Limit  int `json:"limit,omitempty" jsonschema:"Maximum number of results (default 100)"`
	Offset int `json:"offset,omitempty" jsonschema:"Offset for pagination"`
}

func RegisterListCategories(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name:        "list_part_categories",
		Description: "List all part categories, optionally filtered by parent category. Shows the category hierarchy with pathstrings and part counts.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input ListCategoriesInput) (*mcp.CallToolResult, any, error) {
		limit := input.Limit
		if limit <= 0 {
			limit = 100
		}
		path := fmt.Sprintf("/api/part/category/?limit=%d&offset=%d&format=json", limit, input.Offset)
		if input.Parent != 0 {
			path += fmt.Sprintf("&parent=%d", input.Parent)
		}

		var resp client.PaginatedResponse[PartCategory]
		if err := c.Get(path, &resp); err != nil {
			return errResult(fmt.Errorf("listing categories: %w", err)), nil, nil
		}
		return jsonResult(map[string]any{
			"count":   resp.Count,
			"results": resp.Results,
		})
	})
}

// -- Create Part Category --

type CreateCategoryInput struct {
	Name            string `json:"name" jsonschema:"Category name (required)"`
	Description     string `json:"description,omitempty" jsonschema:"Category description"`
	Parent          int    `json:"parent,omitempty" jsonschema:"Parent category ID. 0 or omit for top-level category."`
	DefaultLocation int    `json:"default_location,omitempty" jsonschema:"Default stock location ID for parts in this category. 0 or omit for none."`
	Structural      *bool  `json:"structural,omitempty" jsonschema:"If true, parts cannot be directly assigned to this category (only to sub-categories)"`
}

func RegisterCreateCategory(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name:        "create_part_category",
		Description: "Create a new part category. Categories organize parts into a hierarchy (e.g., Electronic Components > Resistors > Through Hole). Always search for existing categories first to avoid duplicates.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input CreateCategoryInput) (*mcp.CallToolResult, any, error) {
		payload := map[string]any{
			"name": input.Name,
		}
		if input.Description != "" {
			payload["description"] = input.Description
		}
		if input.Parent != 0 {
			payload["parent"] = input.Parent
		}
		if input.DefaultLocation != 0 {
			payload["default_location"] = input.DefaultLocation
		}
		if input.Structural != nil {
			payload["structural"] = *input.Structural
		}

		var created PartCategory
		if err := c.Post("/api/part/category/", payload, &created); err != nil {
			return errResult(fmt.Errorf("creating category: %w", err)), nil, nil
		}
		return jsonResult(created)
	})
}

// -- Update Part Category --

type UpdateCategoryInput struct {
	ID              int    `json:"id" jsonschema:"The category ID (pk) to update"`
	Name            string `json:"name,omitempty" jsonschema:"New category name"`
	Description     string `json:"description,omitempty" jsonschema:"New category description"`
	Parent          int    `json:"parent,omitempty" jsonschema:"New parent category ID. 0 or omit to leave unchanged."`
	DefaultLocation int    `json:"default_location,omitempty" jsonschema:"New default stock location ID. 0 or omit to leave unchanged."`
}

func RegisterUpdateCategory(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name:        "update_part_category",
		Description: "Update an existing part category's fields. Only provided fields are changed. Use this to rename categories, change descriptions, or move a category under a different parent.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input UpdateCategoryInput) (*mcp.CallToolResult, any, error) {
		payload := map[string]any{}
		if input.Name != "" {
			payload["name"] = input.Name
		}
		if input.Description != "" {
			payload["description"] = input.Description
		}
		if input.Parent != 0 {
			payload["parent"] = input.Parent
		}
		if input.DefaultLocation != 0 {
			payload["default_location"] = input.DefaultLocation
		}

		if len(payload) == 0 {
			return errResult(fmt.Errorf("no fields to update")), nil, nil
		}

		var updated PartCategory
		path := fmt.Sprintf("/api/part/category/%d/", input.ID)
		if err := c.Patch(path, payload, &updated); err != nil {
			return errResult(fmt.Errorf("updating category %d: %w", input.ID, err)), nil, nil
		}
		return jsonResult(updated)
	})
}

// -- Delete Part Category --

type DeleteCategoryInput struct {
	ID                    int  `json:"id" jsonschema:"The category ID (pk) to delete"`
	DeleteParts           bool `json:"delete_parts,omitempty" jsonschema:"Delete any parts still in the category. Default false, which moves them to the parent category instead."`
	DeleteChildCategories bool `json:"delete_child_categories,omitempty" jsonschema:"Delete any sub-categories. Default false, which moves them to the parent category instead."`
}

func RegisterDeleteCategory(server *mcp.Server, c *client.Client, r *coerce.Registry) {
	coerce.AddTool(server, r, &mcp.Tool{
		Name: "delete_part_category",
		Description: "Delete a part category. This is destructive and cannot be undone. " +
			"The category does not have to be empty: by default any parts and sub-categories it still holds are MOVED to its parent category (or left uncategorised if it has no parent), not deleted. " +
			"Set delete_parts or delete_child_categories to true to delete the contents along with the category. " +
			"The result reports what happened to the contents.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: boolPtr(true),
		},
	}, func(ctx context.Context, req *mcp.CallToolRequest, input DeleteCategoryInput) (*mcp.CallToolResult, any, error) {
		path := fmt.Sprintf("/api/part/category/%d/", input.ID)

		// Read the category before it is gone, so the result can say what
		// became of its contents.
		var existing PartCategory
		if err := c.Get(path+"?format=json", &existing); err != nil {
			return errResult(fmt.Errorf("looking up category %d: %w", input.ID, err)), nil, nil
		}

		// InvenTree puts these on the delete serializer and rejects a
		// body-less DELETE with "This field is required."
		err := c.DeleteWithBody(path, map[string]any{
			"delete_parts":            input.DeleteParts,
			"delete_child_categories": input.DeleteChildCategories,
		})
		if err != nil {
			return errResult(fmt.Errorf("deleting category %d: %w", input.ID, err)), nil, nil
		}

		var deleted, moved []string
		if existing.PartCount > 0 {
			s := countOf(existing.PartCount, "part", "parts")
			if input.DeleteParts {
				deleted = append(deleted, s)
			} else {
				moved = append(moved, s)
			}
		}
		if existing.Subcategories > 0 {
			s := countOf(existing.Subcategories, "sub-category", "sub-categories")
			if input.DeleteChildCategories {
				deleted = append(deleted, s)
			} else {
				moved = append(moved, s)
			}
		}
		destination := "the parent category"
		if existing.Parent == nil {
			destination = "no category (now uncategorised)"
		}

		return textResult(fmt.Sprintf("Category %d (%s) deleted successfully.%s",
			input.ID, existing.Name, deletionSummary(deleted, moved, destination)))
	})
}
