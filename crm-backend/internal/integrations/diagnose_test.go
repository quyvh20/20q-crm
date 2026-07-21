package integrations

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// diagnoseProvider lets a test drive each layer independently.
type diagnoseProvider struct {
	UnimplementedProvider
	healthErr  error
	subscribed bool
	subErr     error
}

func (p *diagnoseProvider) Info() ProviderInfo { return ProviderInfo{Key: "diag", Label: "Diag"} }
func (p *diagnoseProvider) HealthCheck(context.Context, *IntegrationConnection, Credentials) error {
	return p.healthErr
}
func (p *diagnoseProvider) CheckSubscription(context.Context, *IntegrationConnection, Credentials) (bool, error) {
	return p.subscribed, p.subErr
}

func statusOf(checks []diagnoseCheck, key string) string {
	for _, c := range checks {
		if c.Key == key {
			return c.Status
		}
	}
	return ""
}

// A provider with no probe is not a broken provider. Every future adapter inherits
// UnimplementedProvider, and reporting `fail` would make each one read as broken on
// the day it ships.
func TestDiagnose_UnsupportedCapabilityIsUnknownNotFailure(t *testing.T) {
	require.ErrorIs(t, UnimplementedProvider{}.HealthCheck(context.Background(), nil, Credentials{}),
		ErrProviderCapabilityUnsupported)
	_, err := UnimplementedProvider{}.CheckSubscription(context.Background(), nil, Credentials{})
	require.ErrorIs(t, err, ErrProviderCapabilityUnsupported,
		"a provider that cannot answer must be distinguishable from one that answered no")
}

// The layer that was previously invisible: the token is healthy, so the card reads
// "connected", but the provider is not configured to send anything and never will be.
func TestCheckSubscription_DistinguishesLapsedFromHealthy(t *testing.T) {
	p := &diagnoseProvider{subscribed: false}
	ok, err := p.CheckSubscription(context.Background(), nil, Credentials{})
	require.NoError(t, err)
	require.False(t, ok, "a lapsed subscription must be reportable while the token is fine")

	p.subscribed = true
	ok, err = p.CheckSubscription(context.Background(), nil, Credentials{})
	require.NoError(t, err)
	require.True(t, ok)
}

// "We could not ask" must never be reported as "the answer was no" — they lead to
// different actions, and conflating them sends an admin to redo OAuth over an outage.
func TestDiagnose_UnknownIsNotFail(t *testing.T) {
	p := &diagnoseProvider{subErr: errors.New("graph unreachable")}
	_, err := p.CheckSubscription(context.Background(), nil, Credentials{})
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrProviderCapabilityUnsupported,
		"a transport failure and an unsupported capability are different states")
}
