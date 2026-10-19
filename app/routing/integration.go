package routing

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// IntegrationLookup reads a pipeline by its authenticated ID.
type IntegrationLookup interface {
	ResolveIntegration(context.Context, uuid.UUID) (*Resolution, error)
}

// IntegrationResolver addresses one configured source/vendor pair at one
// destination location. Payload routing keys are ignored; the integration ID
// is established by authentication and persisted on the queued event.
type IntegrationResolver struct {
	lookup                                                     IntegrationLookup
	source, destination, destinationReference, originReference string
}

func NewIntegrationResolver(lookup IntegrationLookup, source, destination, destinationReference, originReference string) (*IntegrationResolver, error) {
	source, destination = strings.TrimSpace(source), strings.TrimSpace(destination)
	destinationReference = strings.TrimSpace(destinationReference)
	if lookup == nil || source == "" || destination == "" || destinationReference == "" {
		return nil, fmt.Errorf("%w: integration routing requires a lookup, platform pair and destination location", ErrInvalidArgument)
	}
	return &IntegrationResolver{lookup: lookup, source: source, destination: destination,
		destinationReference: destinationReference, originReference: strings.TrimSpace(originReference)}, nil
}

func (r *IntegrationResolver) Resolve(ctx context.Context, integrationID uuid.UUID, _ Key) (*Resolution, error) {
	if integrationID == uuid.Nil {
		return nil, fmt.Errorf("%w: integration ID is required", ErrInvalidArgument)
	}
	res, err := r.lookup.ResolveIntegration(ctx, integrationID)
	if err != nil {
		return nil, err
	}
	if res.IntegrationID != integrationID || res.SourcePlatform != r.source || res.DestinationPlatform != r.destination {
		return nil, ErrNoRoute
	}
	resolved := *res
	resolved.Route = Route{
		IntegrationID: integrationID, Type: TypeIntegration, Value: integrationID.String(),
		DestinationReference: r.destinationReference, OriginReference: r.originReference, Status: StatusActive,
	}
	return &resolved, nil
}
