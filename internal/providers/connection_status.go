package providers

import (
	messageutils "account-connect/internal/accountconnectmessageutils"
	"account-connect/internal/clients"
	"account-connect/internal/messages"
	"context"
	"encoding/json"
	"log"
	"time"
)

func notifyProviderConnection(
	ctx context.Context,
	client *clients.AccountConnectClient,
	provider messages.Provider,
	streamID, state, reason string,
	retryAfter time.Duration,
) {
	if ctx.Err() != nil {
		return
	}

	payload, err := json.Marshal(messages.ProviderConnectionStatus{
		State:        state,
		StreamID:     streamID,
		Reason:       reason,
		RetryAfterMs: retryAfter.Milliseconds(),
	})
	if err != nil {
		log.Printf("Failed to marshal provider connection status: %v", err)
		return
	}

	response := messageutils.CreateSuccessResponse(
		ctx, messages.TypeProviderConnection, provider, client.ID, payload,
	)
	responseB, err := json.Marshal(response)
	if err != nil {
		log.Printf("Failed to marshal provider connection response: %v", err)
		return
	}

	select {
	case client.Send <- responseB:
	case <-ctx.Done():
	default:
		log.Printf("Client %s send queue full; dropping %s connection status for %s",
			client.ID, provider, streamID)
	}
}
