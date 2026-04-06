// Package handler registers NATS subscription handlers for the dbaas-service.
// Each handler decodes a request, performs business logic (store + openstack-adapter),
// and publishes a reply — all with Prometheus metrics and circuit breaker protection.
package handler

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/etafovivian/shared/circuitbreaker"
	"github.com/etafovivian/shared/natsutil"
	"github.com/etafovivian/dbaas-service/internal/engine"
	"github.com/etafovivian/dbaas-service/internal/metrics"
	"github.com/etafovivian/dbaas-service/internal/store"
	"github.com/nats-io/nats.go"
)

// StoreClient defines the database operations used by handlers.
type StoreClient interface {
	// Instances
	CreateInstance(ctx context.Context, tenantID, environmentID, name, engine, engineVersion, tier, flavorID string, diskSizeGB int, adminPassword string, metadata map[string]string) (*store.Instance, error)
	GetInstance(ctx context.Context, id string) (*store.Instance, error)
	ListInstances(ctx context.Context, tenantID, environmentID string) ([]*store.Instance, error)
	UpdateInstanceStatus(ctx context.Context, id, status string) error
	UpdateInstanceHost(ctx context.Context, id, host string) error
	UpdateInstanceProjectID(ctx context.Context, id, projectID string) error
	UpdateInstanceBackupBucket(ctx context.Context, id, bucket string) error
	SoftDeleteInstance(ctx context.Context, id string) error
	// Nodes
	CreateNode(ctx context.Context, instanceID string, nodeIndex int) (*store.InstanceNode, error)
	GetNodesByInstance(ctx context.Context, instanceID string) ([]*store.InstanceNode, error)
	GetNode(ctx context.Context, nodeID string) (*store.InstanceNode, error)
	UpdateNodeStatus(ctx context.Context, nodeID, status string) error
	UpdateNodeRole(ctx context.Context, nodeID, role string) error
	UpdateNodeOpenStackIDs(ctx context.Context, nodeID, serverID, volumeID string) error
	UpdateNodeIPs(ctx context.Context, nodeID, privateIP, mgmtIP string) error
	UpdateNodeAgentConnected(ctx context.Context, nodeID string, connected bool) error
	UpdateNodeHeartbeat(ctx context.Context, nodeID, role string) error
	CountReadyNodes(ctx context.Context, instanceID string) (int, error)
	GetInstanceByNodeID(ctx context.Context, nodeID string) (*store.Instance, error)
}

// Deps holds injected dependencies for the handler package.
type Deps struct {
	Store         StoreClient
	NC            *nats.Conn
	Metrics       *metrics.Metrics
	Engines       *engine.Registry // Registered database engines
	ImageID       string           // Glance image UUID for DB VMs
	MgmtNetID     string           // dbaas-mgmt Neutron network UUID
	AgentVersion  string           // Docker image tag for dbaas-agent
	S3Endpoint    string           // Ceph RGW S3 endpoint
	S3AccessKey   string
	S3SecretKey   string
}

// Handler manages NATS subscriptions and request routing.
type Handler struct {
	store        StoreClient
	nc           *nats.Conn
	m            *metrics.Metrics
	engines      *engine.Registry
	imageID      string
	mgmtNetID    string
	agentVersion string
	s3Endpoint   string
	s3AccessKey  string
	s3SecretKey  string
	breakers     map[string]*circuitbreaker.Breaker
	shutdownCtx  context.Context
	shutdownFn   context.CancelFunc
}

// New creates a Handler with the provided dependencies.
func New(deps Deps) *Handler {
	ctx, cancel := context.WithCancel(context.Background())
	return &Handler{
		store:        deps.Store,
		nc:           deps.NC,
		m:            deps.Metrics,
		engines:      deps.Engines,
		imageID:      deps.ImageID,
		mgmtNetID:    deps.MgmtNetID,
		agentVersion: deps.AgentVersion,
		s3Endpoint:   deps.S3Endpoint,
		s3AccessKey:  deps.S3AccessKey,
		s3SecretKey:  deps.S3SecretKey,
		breakers:     buildBreakers(deps.Metrics),
		shutdownCtx:  ctx,
		shutdownFn:   cancel,
	}
}

// Shutdown signals all provisioning goroutines to stop.
func (h *Handler) Shutdown() {
	h.shutdownFn()
}

// buildBreakers creates circuit breakers for external service calls.
func buildBreakers(m *metrics.Metrics) map[string]*circuitbreaker.Breaker {
	names := []string{"openstack", "iam", "agent"}
	breakers := make(map[string]*circuitbreaker.Breaker, len(names))
	for _, name := range names {
		breakers[name] = circuitbreaker.New(name, m.CircuitBreakerState)
	}
	return breakers
}

// RegisterSubscriptions subscribes to all NATS subjects this service handles.
func (h *Handler) RegisterSubscriptions(nc *nats.Conn) error {
	subs := map[string]nats.MsgHandler{
		// Instance CRUD
		"dbaas.instance.create": h.handleInstanceCreate,
		"dbaas.instance.get":    h.handleInstanceGet,
		"dbaas.instance.list":   h.handleInstanceList,
		"dbaas.instance.delete": h.handleInstanceDelete,
		// Agent callbacks
		"dbaas.agent.ready":     h.handleAgentReady,
		"dbaas.agent.heartbeat": h.handleAgentHeartbeat,
	}

	for subj, hdlr := range subs {
		if _, err := nc.Subscribe(subj, h.wrap(subj, hdlr)); err != nil {
			return fmt.Errorf("subscribe %s: %w", subj, err)
		}
		slog.Info("subscribed", "subject", subj)
	}
	return nil
}

// wrap instruments any NATS handler with metrics tracking.
func (h *Handler) wrap(subject string, fn nats.MsgHandler) nats.MsgHandler {
	return func(msg *nats.Msg) {
		start := time.Now()
		fn(msg)
		h.m.NATSRequestDuration.WithLabelValues(subject).Observe(time.Since(start).Seconds())
	}
}

// replyErr sends an error reply and increments the error metric.
func (h *Handler) replyErr(msg *nats.Msg, subject, errMsg string) {
	h.m.NATSRequests.WithLabelValues(subject, "error").Inc()
	natsutil.ReplyErr(msg, errMsg)
}

// svcRequest makes a cross-service NATS request with a named circuit breaker.
func (h *Handler) svcRequest(breakerName, subject string, req, resp any) error {
	_, err := h.breakers[breakerName].Execute(func() (any, error) {
		reply, reqErr := h.nc.Request(subject, mustMarshal(req), 15*time.Second)
		if reqErr != nil {
			return nil, fmt.Errorf("nats request %s: %w", subject, reqErr)
		}
		if resp != nil {
			if err := parseOSReply(reply.Data, resp); err != nil {
				return nil, err
			}
		} else {
			if err := checkOSReply(reply.Data); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if err != nil {
		return fmt.Errorf("%s %s: %w", breakerName, subject, err)
	}
	return nil
}

// osRequest makes a cross-service NATS request to openstack-adapter with circuit breaker.
func (h *Handler) osRequest(subject string, req, resp any) error {
	return h.svcRequest("openstack", subject, req, resp)
}

// iamRequest makes a cross-service NATS request to iam-service with circuit breaker.
func (h *Handler) iamRequest(subject string, req, resp any) error {
	return h.svcRequest("iam", subject, req, resp)
}
