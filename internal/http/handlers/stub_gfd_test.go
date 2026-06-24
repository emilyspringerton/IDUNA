package handlers_test

import (
	"context"

	"iduna/internal/store"
)

// noopGFDTiers embeds in test stubs to satisfy the GFD tier IAMStore methods.
// All stubs that don't test GFD tier functionality embed this.
type noopGFDTiers struct{}

func (noopGFDTiers) ListSubscriptionTiers(_ context.Context) ([]store.GFDTier, error) {
	return nil, nil
}
func (noopGFDTiers) GetGFDUserTier(_ context.Context, _ string) (*string, error) { return nil, nil }
func (noopGFDTiers) SetGFDUserTier(_ context.Context, _, _ string) error         { return nil }
func (noopGFDTiers) RecordStripeEvent(_ context.Context, _, _, _, _ string) error { return nil }
