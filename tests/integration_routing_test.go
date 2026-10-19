package tests

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gluzo/integration-gateway/app/auth"
	"github.com/gluzo/integration-gateway/app/event"
	"github.com/gluzo/integration-gateway/app/health"
	"github.com/gluzo/integration-gateway/app/httpserver"
	"github.com/gluzo/integration-gateway/app/idempotency"
	"github.com/gluzo/integration-gateway/app/integrations/easyecom"
	"github.com/gluzo/integration-gateway/app/integrations/vinculum"
	"github.com/gluzo/integration-gateway/app/queue"
	"github.com/gluzo/integration-gateway/app/routing"
	"github.com/gluzo/integration-gateway/app/skumap"
	"github.com/gluzo/integration-gateway/app/vendor"
	"github.com/gluzo/integration-gateway/app/webhook"
	"github.com/gluzo/integration-gateway/app/workflow"
	"github.com/gluzo/integration-gateway/app/workflow/ordersync"
	"github.com/gluzo/integration-gateway/app/workflowstate"
	"github.com/google/uuid"
)

// Real authentication, SQL integration lookup and SKU mapping, and real
// adapters over stub HTTP servers, without any integration_routes row.
func TestIntegrationRoutingDeliversOrdersWithoutWarehouseRoutes(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	creds := auth.NewStore(pool)
	platformKey := ensurePlatform(t, creds, "easyecom", auth.PlatformTypeSource)
	ensurePlatform(t, creds, "vinculum", auth.PlatformTypeDestination)
	integ, err := creds.CreateIntegration(ctx, "direct-"+uuid.NewString()[:8], "easyecom", "vinculum")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := creds.IssueToken(ctx, integ.Name, "test", 0)
	if err != nil {
		t.Fatal(err)
	}
	routes := routing.NewStore(pool)
	resolver, err := routing.NewIntegrationResolver(routes, "easyecom", "vinculum", "PBW", "bcpl-location")
	if err != nil {
		t.Fatal(err)
	}
	mappings := skumap.NewStore(pool)
	if _, err := mappings.Add(ctx, integ.Name, "BCPL-E2E", "VIN-E2E", 0); err != nil {
		t.Fatal(err)
	}
	easyAPI := newFakeEasyEcomAPI(t)
	easyClient, err := easyecom.NewClient(easyecom.Config{BaseURL: easyAPI.srv.URL, APIKey: e2eEasyAPIKey, JWTToken: e2eEasyJWT})
	if err != nil {
		t.Fatal(err)
	}
	vinAPI := newFakeVinculumAPI(t)
	vinClient, err := vinculum.NewClient(vinculum.Config{BaseURL: vinAPI.srv.URL, APIOwner: e2eVinculumOwner, APIKey: e2eVinculumKey, Location: "PBW", SellableBucket: "Good", OrderOptions: e2eVinculumOrderOptions()})
	if err != nil {
		t.Fatal(err)
	}
	vnd, err := vinculum.NewVendor(vinClient, logger)
	if err != nil {
		t.Fatal(err)
	}
	vendors := vendor.NewRegistry()
	if err := vendors.Register(vnd); err != nil {
		t.Fatal(err)
	}
	wf, err := ordersync.New(ordersync.Dependencies{Resolver: resolver,
		Origins: map[string]vendor.Origin{"easyecom": easyecom.NewSource(easyClient, logger)}, Vendors: vendors, SKUs: mappings})
	if err != nil {
		t.Fatal(err)
	}
	jobs := queue.NewMemory(8)
	h := webhook.NewHandler(easyecom.WebhookParser{IntegrationRouting: true}, idempotency.NewPostgresStore(pool), jobs, logger, webhook.WithIntegrationRouting(true))
	router := httpserver.NewRouter(httpserver.Dependencies{Logger: logger,
		Health: health.NewHandler("svc", "test", time.Second, logger), MaxBodyBytes: 1 << 20,
		PlatformKeys: creds, AccessTokens: creds, Webhooks: []*webhook.Handler{h}})
	executor := workflow.NewExecutor(workflowstate.NewMemoryRepository())
	consumeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	deliveries, err := jobs.Consume(consumeCtx)
	if err != nil {
		t.Fatal(err)
	}
	for _, warehouse := range []string{"", `"warehouseId":304788,`} {
		orderID := "direct-" + uuid.NewString()[:8]
		payload := strings.Replace(e2eWebhookOrders, `"order_id":424242,`, `"order_id":"`+orderID+`",`, 1)
		payload = strings.Replace(payload, `"warehouse_id":777,`, warehouse, 1)
		payload = strings.Replace(payload, `"invoice_id":"INV-E2E",`, "", 1) // force webhook fallback for the unique order
		req := httptest.NewRequest(http.MethodPost, "/webhooks/easyecom", strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(auth.CombinedCredentialHeader, platformKey+":"+token)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		if response.Code != http.StatusAccepted {
			t.Fatalf("intake: %d %s", response.Code, response.Body.String())
		}
		var job queue.Job
		select {
		case delivery := <-deliveries:
			job = delivery.Job
		case <-time.After(time.Second):
			t.Fatal("order was not queued")
		}
		var ev event.Event
		if err := json.Unmarshal(job.Payload, &ev); err != nil {
			t.Fatal(err)
		}
		if ev.RoutingKey.Type != routing.TypeIntegration || ev.RoutingKey.Value != integ.ID.String() {
			t.Fatalf("wrong event routing: %+v", ev.RoutingKey)
		}
		if err := event.Validate(ev); err != nil {
			t.Fatal(err)
		}
		state := workflow.NewState(ordersync.Name, ev, job.ID, time.Now())
		if err := executor.Run(ctx, wf, state); err != nil {
			t.Fatal(err)
		}
		if state.Status != workflow.StatusCompleted {
			t.Fatalf("workflow: %s, %v", state.Status, state.LastError)
		}
		doc, ok := vinAPI.order(orderID)
		if !ok || doc["orderLocation"] != "PBW" {
			t.Fatalf("destination order: %+v", doc)
		}
	}
	configured, err := routes.ListRoutes(ctx, integ.Name)
	if err != nil || len(configured) != 0 {
		t.Fatalf("expected no manual routes: %+v %v", configured, err)
	}
}
