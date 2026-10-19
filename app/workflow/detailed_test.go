package workflow_test

import (
	"context"
	"testing"

	"github.com/gluzo/integration-gateway/app/apperror"
	"github.com/gluzo/integration-gateway/app/intlog"
	"github.com/gluzo/integration-gateway/app/workflow"
)

func TestCallContextTracksActionRetries(t *testing.T) {
	h := newHarness(t)
	st := newState("INT-call-context")
	st.IntegrationID = "pipeline-1"
	st.Route = &workflow.RouteInfo{DestinationPlatform: "vinculum"}
	var snapshots []intlog.Entry
	step := workflow.Step{Action: workflow.NewAction("FETCH_ORDER", func(ctx context.Context, _ *workflow.State) error {
		snapshots = append(snapshots, intlog.CallEntry(ctx))
		if len(snapshots) == 1 {
			return apperror.New(apperror.Timeout, "retry")
		}
		return nil
	}), Policy: workflow.DefaultPolicy()}
	if err := h.exec.Run(context.Background(), workflow.NewDefinition("TEST", step), st); err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 2 {
		t.Fatalf("attempts = %d", len(snapshots))
	}
	for i, e := range snapshots {
		if e.CorrelationID != st.CorrelationID || e.Workflow != "TEST" || e.IntegrationID != "pipeline-1" || e.Integration != "vinculum" || e.ExternalOrderID != "1001" || e.Action != "FETCH_ORDER" || e.Attempt != i+1 {
			t.Fatalf("call context: %+v", e)
		}
	}
}
