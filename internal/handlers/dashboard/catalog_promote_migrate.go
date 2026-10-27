package dashboard

import (
	"context"
	"fmt"

	json "encoding/json/v2"
)

// goString renders a Go/JSON string literal. The promotion payload carries
// user-typed names, so the quoting is done once here instead of at every
// insertion site.
func goString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// MoveNodeConnectionsToCatalog repoints every connection and custom model that
// belonged to a promoted node at the catalog provider id, then deletes the
// node.
//
// Doing this is what makes the move lossless: the alternative — writing the
// catalog entry and leaving the node in place — would either leave the node in
// the Custom Providers grid (so the same endpoint shows twice) or delete it and
// orphan connections whose provider no longer resolves. The rename keeps the
// connection ids, so client API keys, quotas and usage history stay attached.
func (h *DashboardHandler) MoveNodeConnectionsToCatalog(ctx context.Context, nodeID, catalogID string) (int, int, error) {
	node, nodeData, err := h.Repo.GetProviderNodeByID(nodeID)
	if err != nil {
		return 0, 0, fmt.Errorf("read node: %w", err)
	}
	if node == nil {
		return 0, 0, fmt.Errorf("node %s not found", nodeID)
	}

	conns, err := h.Repo.GetProviderConnections(nodeID, false)
	if err != nil {
		return 0, 0, fmt.Errorf("list connections: %w", err)
	}

	moved := 0
	for _, conn := range conns {
		if err := h.Repo.SetConnectionProvider(conn.ID, catalogID); err != nil {
			return moved, 0, fmt.Errorf("move connection %s: %w", conn.ID, err)
		}
		moved++
	}

	// The node's custom models are stored under "<nodeID>|<model>|llm" keys.
	// They are re-keyed rather than recreated so the model definitions (limits,
	// capability flags) survive the move.
	modelsMoved, err := h.Repo.ReassignCustomModelProvider(nodeID, catalogID)
	if err != nil {
		return moved, modelsMoved, fmt.Errorf("move custom models: %w", err)
	}

	// The base URL now lives in KnownProviders, so leaving the node row behind
	// would make the grid render an entry the registry already covers.
	if err := h.Repo.DeleteProviderNode(ctx, nodeID); err != nil {
		return moved, modelsMoved, fmt.Errorf("delete promoted node: %w", err)
	}
	_ = nodeData

	h.reconcileSmartCombosBestEffort(ctx, "provider node promoted to catalog")
	return moved, modelsMoved, nil
}
