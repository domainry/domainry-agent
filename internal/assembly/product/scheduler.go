package product

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
	"github.com/domainry/domainry-scheduler-sdk/saashost/httptransport"
)

type scheduledPlanClient struct {
	transport   *httptransport.Transport
	application schedulersdk.ApplicationRef
}

// OpenScheduledPlansFromEnvironment creates a plan-only SDK client. Product
// composition does not open Scheduler workers, stores, or Runtime callbacks.
func OpenScheduledPlansFromEnvironment(ctx context.Context, runtimeID string) (schedulersdk.ScheduledPlanService, func(), error) {
	config := httptransport.ConfigFromEnvironment()
	values := []string{strings.TrimSpace(config.Endpoint), strings.TrimSpace(config.Token), strings.TrimSpace(config.CapabilityContractSHA256)}
	if strings.Join(values, "") == "" {
		return nil, func() {}, nil
	}
	for _, value := range values {
		if value == "" {
			return nil, func() {}, fmt.Errorf("Scheduler SaaS requires endpoint, service token and expected contract SHA256")
		}
	}
	if !serviceOrigin(values[0]) {
		return nil, func() {}, fmt.Errorf("Scheduler endpoint must be an HTTPS or loopback HTTP origin")
	}
	config.Client = &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	transport, err := httptransport.Open(ctx, config)
	if err != nil {
		return nil, func() {}, err
	}
	application := schedulersdk.ApplicationRef{RuntimeID: strings.TrimSpace(runtimeID)}
	if err := application.Validate(); err != nil {
		return nil, func() {}, err
	}
	descriptor, err := transport.Descriptor(ctx, application)
	if err != nil || !descriptor.Supports(schedulersdk.CapabilityScheduledPlanRecords) {
		_ = transport.Close(context.Background(), application)
		if err != nil {
			return nil, func() {}, err
		}
		return nil, func() {}, fmt.Errorf("Scheduler SaaS does not expose scheduled plan records")
	}
	client := &scheduledPlanClient{transport: transport, application: application}
	return client, func() { _ = transport.Close(context.Background(), application) }, nil
}

func (c *scheduledPlanClient) CreateScheduledPlan(ctx context.Context, input schedulersdk.ScheduledPlanCreate) (schedulersdk.ScheduledPlanReceipt, error) {
	return c.transport.CreateScheduledPlan(ctx, c.application, input)
}
func (c *scheduledPlanClient) GetScheduledPlan(ctx context.Context, input schedulersdk.ScheduledPlanLookup) (schedulersdk.ScheduledPlan, error) {
	return c.transport.GetScheduledPlan(ctx, c.application, input)
}
func (c *scheduledPlanClient) ListScheduledPlans(ctx context.Context, input schedulersdk.ScheduledPlanList) (schedulersdk.ScheduledPlanPage, error) {
	return c.transport.ListScheduledPlans(ctx, c.application, input)
}
func (c *scheduledPlanClient) UpdateScheduledPlan(ctx context.Context, input schedulersdk.ScheduledPlanUpdate) (schedulersdk.ScheduledPlanReceipt, error) {
	return c.transport.UpdateScheduledPlan(ctx, c.application, input)
}
func (c *scheduledPlanClient) PauseScheduledPlan(ctx context.Context, input schedulersdk.ScheduledPlanStatusChange) (schedulersdk.ScheduledPlanReceipt, error) {
	return c.transport.PauseScheduledPlan(ctx, c.application, input)
}
func (c *scheduledPlanClient) ResumeScheduledPlan(ctx context.Context, input schedulersdk.ScheduledPlanStatusChange) (schedulersdk.ScheduledPlanReceipt, error) {
	return c.transport.ResumeScheduledPlan(ctx, c.application, input)
}
func (c *scheduledPlanClient) DeleteScheduledPlan(ctx context.Context, input schedulersdk.ScheduledPlanStatusChange) (schedulersdk.ScheduledPlanDeleteReceipt, error) {
	return c.transport.DeleteScheduledPlan(ctx, c.application, input)
}

var _ schedulersdk.ScheduledPlanService = (*scheduledPlanClient)(nil)
