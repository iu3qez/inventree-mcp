package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeWithSupplierParts seeds part 42 with LCSC C8574 (pk 7) and part 43 with
// C1525 (pk 8), the shape of a booked-in distributor order.
func fakeWithSupplierParts() *fakeInvenTree {
	fake := newFakeInvenTree()
	fake.supplierParts[7] = map[string]any{"pk": 7, "part": 42, "supplier": 5, "SKU": "C8574"}
	fake.supplierParts[8] = map[string]any{"pk": 8, "part": 43, "supplier": 5, "SKU": "C1525"}
	return fake
}

// callToolError calls a tool that is expected to fail and returns its message.
func callToolError(t *testing.T, s *mcp.ClientSession, name string, args map[string]any) string {
	t.Helper()
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if !res.IsError {
		t.Fatalf("%s: expected an error result", name)
	}
	return res.Content[0].(*mcp.TextContent).Text
}

func TestAddStockLinksSupplierPart(t *testing.T) {
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"by pk", map[string]any{"supplier_part": 7}},
		{"by SKU", map[string]any{"SKU": "c8574"}},
		{"by pk and matching SKU", map[string]any{"supplier_part": 7, "SKU": "C8574"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := fakeWithSupplierParts()
			session := connect(t, fake.start(t))

			args := map[string]any{"part": 42, "quantity": 100, "location": 3}
			for k, v := range tc.args {
				args[k] = v
			}
			out := callTool(t, session, "add_stock", args)

			if got := toFloat(fake.stockBodies[0]["supplier_part"]); got != 7 {
				t.Errorf("POSTed supplier_part = %v, want 7", fake.stockBodies[0]["supplier_part"])
			}
			if got := toFloat(out["supplier_part"]); got != 7 {
				t.Errorf("returned supplier_part = %v, want 7", out["supplier_part"])
			}
		})
	}
}

func TestAddStockWithoutSupplierPart(t *testing.T) {
	fake := fakeWithSupplierParts()
	session := connect(t, fake.start(t))

	callTool(t, session, "add_stock", map[string]any{"part": 42, "quantity": 5})

	if _, ok := fake.stockBodies[0]["supplier_part"]; ok {
		t.Errorf("supplier_part sent without being asked for: %v", fake.stockBodies[0])
	}
	if n := fake.countCalls("GET /api/company/part/"); n != 0 {
		t.Errorf("looked up supplier parts %d times without a SKU", n)
	}
}

func TestAddStockRejectsForeignSupplierPart(t *testing.T) {
	for _, tc := range []struct {
		name string
		args map[string]any
		want string
	}{
		{"pk of another part", map[string]any{"supplier_part": 8}, "belongs to part 43"},
		{"SKU of another part", map[string]any{"SKU": "C1525"}, "does not belong to part 42"},
		{"pk and SKU disagree", map[string]any{"supplier_part": 7, "SKU": "C1525"}, "not C1525"},
		{"unknown SKU", map[string]any{"SKU": "C999999"}, "no supplier part with SKU"},
		{"unknown pk", map[string]any{"supplier_part": 999}, "getting supplier part 999"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := fakeWithSupplierParts()
			session := connect(t, fake.start(t))

			args := map[string]any{"part": 42, "quantity": 100}
			for k, v := range tc.args {
				args[k] = v
			}
			msg := callToolError(t, session, "add_stock", args)

			if !strings.Contains(msg, tc.want) {
				t.Errorf("error = %q, want it to mention %q", msg, tc.want)
			}
			if n := fake.countCalls("POST /api/stock/"); n != 0 {
				t.Errorf("stock was created despite the mismatch (%d POSTs)", n)
			}
		})
	}
}
