package acpcli

import (
	"context"
	"encoding/json"
	"time"

	"github.com/beyond5959/ngent/internal/agents"
)

// StructuredPermissionRequestHandler bridges normalized ACP permission payloads
// into ngent's shared permission workflow.
func StructuredPermissionRequestHandler(
	timeout time.Duration,
) func(context.Context, json.RawMessage, agents.PermissionHandler, bool) (json.RawMessage, error) {
	return func(
		ctx context.Context,
		params json.RawMessage,
		handler agents.PermissionHandler,
		hasHandler bool,
	) (json.RawMessage, error) {
		req, err := ParsePermissionRequestPayload(params)
		if err != nil {
			return buildDeclinedPermissionResponse(nil)
		}
		if !hasHandler {
			return buildDeclinedPermissionResponse(req.Options)
		}

		permCtx, cancel := permissionContext(ctx, timeout)
		defer cancel()

		resp, err := handler(permCtx, req.ToAgentPermissionRequest())
		if err != nil {
			return buildDeclinedPermissionResponse(req.Options)
		}

		switch resp.Outcome {
		case agents.PermissionOutcomeApproved:
			return buildApprovedPermissionResponse(req.Options)
		case agents.PermissionOutcomeCancelled:
			return BuildCancelledPermissionResponse()
		default:
			return buildDeclinedPermissionResponse(req.Options)
		}
	}
}

func buildApprovedPermissionResponse(options []PermissionOption) (json.RawMessage, error) {
	optionID := PickPermissionOptionID(options, "allow_once", "allow_always")
	if optionID == "" {
		return buildDeclinedPermissionResponse(options)
	}
	return BuildSelectedPermissionResponse(optionID)
}

func buildDeclinedPermissionResponse(options []PermissionOption) (json.RawMessage, error) {
	optionID := PickPermissionOptionID(options, "reject_once", "reject_always")
	if optionID == "" {
		return BuildCancelledPermissionResponse()
	}
	return BuildSelectedPermissionResponse(optionID)
}

func permissionContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}
