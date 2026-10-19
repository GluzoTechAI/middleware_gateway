package routing_test

import (
	"context"
	"errors"
	"testing"

	"github.com/gluzo/integration-gateway/app/routing"
	"github.com/google/uuid"
)

type integrationLookup func(context.Context, uuid.UUID) (*routing.Resolution, error)

func (f integrationLookup) ResolveIntegration(ctx context.Context, id uuid.UUID) (*routing.Resolution, error) {
	return f(ctx, id)
}

func TestIntegrationRoutingUsesAuthenticatedIDAndConfiguredLocation(t *testing.T) {
	id := uuid.New()
	lookup := integrationLookup(func(_ context.Context, got uuid.UUID) (*routing.Resolution, error) {
		if got != id {
			t.Fatalf("lookup ID = %s, want %s", got, id)
		}
		return &routing.Resolution{IntegrationID: id, IntegrationName: "bcpl", SourcePlatform: "easyecom", DestinationPlatform: "vinculum"}, nil
	})
	r, err := routing.NewIntegrationResolver(lookup, "easyecom", "vinculum", " PBW ", "bcpl-location")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []routing.Key{{}, {Type: routing.TypeWarehouse, Value: "unconfigured"}, {Type: routing.TypeIntegration, Value: uuid.NewString()}} {
		res, err := r.Resolve(context.Background(), id, key)
		if err != nil {
			t.Fatal(err)
		}
		if res.IntegrationID != id || res.Route.Value != id.String() || res.Route.Type != routing.TypeIntegration || res.Route.DestinationReference != "PBW" || res.Route.OriginReference != "bcpl-location" {
			t.Fatalf("unexpected resolution: %+v", res)
		}
	}
	if _, err := r.Resolve(context.Background(), uuid.Nil, routing.Key{}); !errors.Is(err, routing.ErrInvalidArgument) {
		t.Fatalf("empty ID: %v", err)
	}
	if _, err := routing.NewIntegrationResolver(lookup, "easyecom", "vinculum", "", ""); !errors.Is(err, routing.ErrInvalidArgument) {
		t.Fatalf("missing destination: %v", err)
	}
}

func TestIntegrationRoutingRejectsOtherPipelinesAndPropagatesLookupErrors(t *testing.T) {
	id := uuid.New()
	for _, tc := range []struct {
		name, source, destination string
		id                        uuid.UUID
		err                       error
	}{
		{"other source", "other", "vinculum", id, nil},
		{"other destination", "easyecom", "other", id, nil},
		{"other ID", "easyecom", "vinculum", uuid.New(), nil},
		{"inactive integration", "", "", id, routing.ErrNoRoute},
		{"database unavailable", "", "", id, errors.New("database unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lookup := integrationLookup(func(context.Context, uuid.UUID) (*routing.Resolution, error) {
				return &routing.Resolution{IntegrationID: tc.id, SourcePlatform: tc.source, DestinationPlatform: tc.destination}, tc.err
			})
			r, err := routing.NewIntegrationResolver(lookup, "easyecom", "vinculum", "PBW", "")
			if err != nil {
				t.Fatal(err)
			}
			_, err = r.Resolve(context.Background(), id, routing.Key{})
			want := tc.err
			if want == nil {
				want = routing.ErrNoRoute
			}
			if !errors.Is(err, want) {
				t.Fatalf("err = %v, want %v", err, want)
			}
		})
	}
}
