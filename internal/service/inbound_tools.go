package service

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ListInboundEndpointsInput is the input for the chetter_list_inbound_endpoints
// MCP tool (issue #120). Secret values are never returned.
type ListInboundEndpointsInput struct {
	Limit  int `json:"limit,omitempty" jsonschema:"Maximum endpoints to return (default 50)"`
	Offset int `json:"offset,omitempty" jsonschema:"Number of endpoints to skip (default 0)"`
}

// ListInboundEndpointsOutput is the output for the
// chetter_list_inbound_endpoints MCP tool.
type ListInboundEndpointsOutput struct {
	Endpoints []inboundEndpointRecord `json:"endpoints"`
}

func (s *Service) listInboundEndpointsTool(ctx context.Context, _ *mcp.CallToolRequest, in ListInboundEndpointsInput) (*mcp.CallToolResult, ListInboundEndpointsOutput, error) {
	endpoints, err := s.ListInboundEndpoints(ctx, in.Limit, in.Offset)
	if err != nil {
		return nil, ListInboundEndpointsOutput{}, err
	}
	return nil, ListInboundEndpointsOutput{Endpoints: endpoints}, nil
}

// ListInboundDeliveriesInput is the input for the
// chetter_list_inbound_deliveries MCP tool.
type ListInboundDeliveriesInput struct {
	EndpointID string `json:"endpoint_id,omitempty" jsonschema:"Only deliveries for this endpoint id; empty returns all"`
	Status     string `json:"status,omitempty" jsonschema:"Only deliveries with this status (pending, processing, succeeded, retry_wait, failed_permanent, dead_letter); empty returns all"`
	Limit      int    `json:"limit,omitempty" jsonschema:"Maximum deliveries to return (default 50)"`
	Offset     int    `json:"offset,omitempty" jsonschema:"Number of deliveries to skip (default 0)"`
}

// ListInboundDeliveriesOutput is the output for the
// chetter_list_inbound_deliveries MCP tool.
type ListInboundDeliveriesOutput struct {
	Deliveries []inboundDeliveryRecord `json:"deliveries"`
}

func (s *Service) listInboundDeliveriesTool(ctx context.Context, _ *mcp.CallToolRequest, in ListInboundDeliveriesInput) (*mcp.CallToolResult, ListInboundDeliveriesOutput, error) {
	deliveries, err := s.ListInboundDeliveries(ctx, in.EndpointID, in.Status, in.Limit, in.Offset)
	if err != nil {
		return nil, ListInboundDeliveriesOutput{}, err
	}
	return nil, ListInboundDeliveriesOutput{Deliveries: deliveries}, nil
}
