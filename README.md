# InvenTree MCP Server

An [MCP](https://modelcontextprotocol.io/) (Model Context Protocol) server that connects AI assistants to your [InvenTree](https://inventree.org/) inventory management system. Talk to your inventory in natural language — create parts, manage stock, organize locations, and more.

**Example prompts:**

- *"Add 10 ESP32-P4 boards to Green 1 in the office"*
- *"How many potentiometers do I have in stock?"*
- *"Create a new category for voltage regulators under Electronic Components"*
- *"Move 5 resistors from Blue 1 to Green 1"*
- *"Take in LCSC C12345: LM7805, 5V regulator, TO-220, 25 pieces in Green 1"*
- *"Which part is distributor code C7593?"*

## Features

- **36 MCP tools** covering parts, stock, locations, categories, parameters and sourcing
- **Fuzzy search** — say "green box" and it finds "Green 1"
- **Hierarchical navigation** — locations and categories with full path display
- **Stock management** — add, remove, transfer, and track inventory
- **Component intake** — one call to register a part with its datasheet specs, MPN and distributor SKU
- **Image search** — optionally find and attach product images via Google
- **Type coercion middleware** — handles client quirks gracefully

## Prerequisites

- **Go 1.23+** (for building from source)
- **InvenTree instance** (v1.x) accessible over HTTP/HTTPS
- **InvenTree API token** (see [Generating an API Token](#generating-an-api-token))

## Installation

### Build from source

```bash
git clone https://github.com/syntaxerr66/inventree-mcp.git
cd inventree-mcp
go build -o inventree-mcp ./cmd/inventree-mcp
```

The binary is self-contained — copy it wherever you like.

### Verify it works

```bash
INVENTREE_URL=http://your-inventree-host \
INVENTREE_TOKEN=your-token-here \
./inventree-mcp
```

The server communicates over stdin/stdout using the MCP protocol. If it starts without errors, you're good. Press `Ctrl+C` to stop.

## Generating an API Token

The MCP server authenticates with InvenTree using an API token. There are two ways to get one:

### Option 1: From the InvenTree web UI

1. Log in to your InvenTree instance
2. Go to your user settings (click your username → **Settings**)
3. Find the **API Tokens** section
4. Create or copy your token

### Option 2: Programmatically

Send a GET request with your username and password using basic authentication:

```bash
curl -u your-username:your-password http://your-inventree-host/api/user/token/
```

Response:

```json
{
    "token": "inv-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx-xxxxxxxx"
}
```

### Token notes

- Tokens are **persistent** — they don't expire unless an administrator revokes them
- API access is **scoped to user permissions** — the token inherits the permissions of the user it belongs to
- Use a dedicated user account for the MCP server if you want to restrict what the AI can do

## Configuration

The server is configured via environment variables:

| Variable | Required | Description |
|---|---|---|
| `INVENTREE_URL` | Yes | Base URL of your InvenTree instance (e.g., `http://192.168.1.100`) |
| `INVENTREE_TOKEN` | Yes | API token for authentication |
| `GOOGLE_API_KEY` | No | Google Cloud API key for image search |
| `GOOGLE_CSE_ID` | No | Google Custom Search Engine ID for image search |

### Optional: Image search

The `search_part_images` tool lets the AI find product photos and attach them to parts. To enable it:

1. Create a [Google Custom Search Engine](https://cse.google.com/) configured for image search
2. Enable the Custom Search API in your [Google Cloud Console](https://console.cloud.google.com/)
3. Set `GOOGLE_API_KEY` and `GOOGLE_CSE_ID`

If not configured, the server starts normally — only the image search tool will return an informative error. All other tools work regardless.

## Setup with Claude Code (CLI)

Add a `.mcp.json` file to your project root (or `~/.claude/.mcp.json` for global config):

```json
{
  "mcpServers": {
    "inventree": {
      "command": "/path/to/inventree-mcp",
      "env": {
        "INVENTREE_URL": "http://your-inventree-host",
        "INVENTREE_TOKEN": "your-token-here"
      }
    }
  }
}
```

> **Security:** Add `.mcp.json` to your `.gitignore` — it contains your API token.

After adding the config, restart Claude Code. The tools will be available automatically. You can verify with:

```
/mcp
```

This lists all connected MCP servers and their tools.

## Setup with Claude Desktop

Edit your Claude Desktop configuration file:

- **macOS:** `~/Library/Application Support/Claude/claude_desktop_config.json`
- **Windows:** `%APPDATA%\Claude\claude_desktop_config.json`
- **Linux:** `~/.config/Claude/claude_desktop_config.json`

Add the server to the `mcpServers` section:

```json
{
  "mcpServers": {
    "inventree": {
      "command": "/path/to/inventree-mcp",
      "env": {
        "INVENTREE_URL": "http://your-inventree-host",
        "INVENTREE_TOKEN": "your-token-here"
      }
    }
  }
}
```

Restart Claude Desktop. You should see a hammer icon indicating MCP tools are available.

## Available Tools

### Parts

| Tool | Description |
|---|---|
| `search_parts` | Search parts by name, keyword, or description |
| `get_part` | Get detailed info about a specific part |
| `list_parts` | List parts with optional category filter |
| `create_part` | Create a new part |
| `update_part` | Update part fields (name, description, category, etc.) |
| `delete_part` | Delete a part (auto-deactivates first) |
| `set_part_image` | Attach an image to a part via URL |
| `search_part_images` | Find product images via Google (requires API keys) |

### Stock

| Tool | Description |
|---|---|
| `get_stock` | List stock items with part/location filters |
| `get_stock_item` | Get a specific stock item by ID |
| `add_stock` | Create a new stock entry (part + quantity + location) |
| `stock_add_quantity` | Add quantity to existing stock items |
| `stock_remove_quantity` | Remove quantity from existing stock items |
| `stock_transfer` | Move stock between locations |
| `delete_stock_item` | Delete a stock entry |

### Stock Locations

| Tool | Description |
|---|---|
| `search_stock_locations` | Fuzzy search locations by name or description |
| `get_stock_location` | Get location details with full path |
| `list_stock_locations` | List all locations with hierarchy |
| `create_stock_location` | Create a new location (supports nesting) |
| `update_stock_location` | Update location fields |
| `delete_stock_location` | Delete an empty location |

### Part Categories

| Tool | Description |
|---|---|
| `search_part_categories` | Search categories by name |
| `list_part_categories` | List the full category hierarchy |
| `create_part_category` | Create a new category (supports nesting) |
| `update_part_category` | Update category fields |
| `delete_part_category` | Delete an empty category |

### Parameters

| Tool | Description |
|---|---|
| `get_part_parameters` | List the technical specs attached to a part |
| `set_part_parameters` | Set several parameters at once, creating templates on demand |
| `list_parameter_templates` | List the parameter names already in use |

### Sourcing (companies, manufacturers, suppliers)

| Tool | Description |
|---|---|
| `search_companies` | Search suppliers, manufacturers and customers |
| `get_or_create_company` | Look up a company by name, creating it if missing |
| `create_manufacturer_part` | Record the manufacturer and MPN for a part |
| `create_supplier_part` | Record a distributor SKU for a part |
| `search_supplier_parts` | Go from a distributor order code back to the part |
| `get_part_sourcing` | List every MPN and SKU recorded for a part |

### Component intake

| Tool | Description |
|---|---|
| `intake_part` | Create a part with parameters, MPN, SKU and initial stock in one call |

`intake_part` is the shortcut for the usual workflow — you have a component and its
distributor code, and want it in InvenTree complete with specs and sourcing:

```
"Take in LCSC C25804: 10k 0603 resistor, 1%, 100 pieces into Blue 2"
```

It creates the part, sets the datasheet parameters, creates the manufacturer and
supplier companies if they do not exist, records the MPN and the SKU, and books the
stock. Steps are independent: if one fails the others still run and the result says
what succeeded and what did not. An existing supplier part with the same SKU is
reused rather than duplicated.

## How It Works

The server runs as a stdio process — the MCP client (Claude Code, Claude Desktop) launches it and communicates via JSON-RPC over stdin/stdout.

```
User → Claude → MCP Client → inventree-mcp (stdio) → InvenTree REST API
```

When you say something like *"Add 10 ESP32-P4 boards to Green 1"*, the AI:

1. Calls `search_parts("ESP32-P4")` to check if the part exists
2. Calls `search_stock_locations("Green 1")` to find the location
3. Creates the part with `create_part` if needed (after finding the right category)
4. Calls `add_stock(part=X, quantity=10, location=Y)` to add inventory

The AI handles disambiguation — if "green" matches multiple locations, it presents options. If a part doesn't exist, it asks whether to create it.

## Development

```bash
# Build all packages
go build ./...

# Build the binary
go build -o inventree-mcp ./cmd/inventree-mcp

# Run directly
INVENTREE_URL=http://... INVENTREE_TOKEN=... go run ./cmd/inventree-mcp

# Run the unit tests (no InvenTree instance needed — uses a fake server)
go test ./internal/tools/

# Run integration tests (requires a live InvenTree instance)
INVENTREE_URL=http://... INVENTREE_TOKEN=... go test -v ./internal/tools/

# Run all tests
go test ./...
```

### Project structure

```
cmd/inventree-mcp/main.go     Entry point — config, server, tool registration
internal/
  client/client.go             HTTP client for InvenTree REST API
  config/config.go             Environment variable configuration
  coerce/coerce.go             Type coercion middleware for MCP compatibility
  imagesearch/google.go        Google Custom Search image client
  tools/
    parts.go                   Part CRUD + image tools
    stock.go                   Stock item management tools
    locations.go               Stock location tools
    categories.go              Part category tools
    parameters.go              Part parameter and template tools
    companies.go               Company, manufacturer part, supplier part tools
    intake.go                  End-to-end component intake tool
    register.go                Tool registration orchestrator
    intake_test.go             Unit tests against a fake InvenTree server
    tools_integration_test.go  Integration tests
```

## License

MIT
