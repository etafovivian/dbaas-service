// Package metrics defines Prometheus metrics for the dbaas-service.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics holds all Prometheus collectors for the dbaas-service.
type Metrics struct {
	NATSRequests        *prometheus.CounterVec
	NATSRequestDuration *prometheus.HistogramVec
	OpenStackRequests   *prometheus.CounterVec
	AgentRequests       *prometheus.CounterVec
	CircuitBreakerState *prometheus.GaugeVec
	DBQueryDuration     *prometheus.HistogramVec
	InstancesTotal      *prometheus.GaugeVec
	ProvisionDuration   *prometheus.HistogramVec
}

// New registers and returns all dbaas-service metrics.
func New() *Metrics {
	return &Metrics{
		NATSRequests: promauto.NewCounterVec(prometheus.CounterOpts{
			Namespace: "etafovivian",
			Subsystem: "dbaas_service",
			Name:      "nats_requests_total",
			Help:      "Total NATS requests received, by subject and status (ok|error).",
		}, []string{"subject", "status"}),

		NATSRequestDuration: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "etafovivian",
			Subsystem: "dbaas_service",
			Name:      "nats_request_duration_seconds",
			Help:      "Duration of NATS request handling in seconds.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"subject"}),

		OpenStackRequests: promauto.NewCounterVec(prometheus.CounterOpts{
			Namespace: "etafovivian",
			Subsystem: "dbaas_service",
			Name:      "openstack_requests_total",
			Help:      "Total outbound openstack-adapter calls, by operation and status.",
		}, []string{"operation", "status"}),

		AgentRequests: promauto.NewCounterVec(prometheus.CounterOpts{
			Namespace: "etafovivian",
			Subsystem: "dbaas_service",
			Name:      "agent_requests_total",
			Help:      "Total outbound dbaas-agent calls, by operation and status.",
		}, []string{"operation", "status"}),

		CircuitBreakerState: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "etafovivian",
			Subsystem: "dbaas_service",
			Name:      "circuit_breaker_state",
			Help:      "Circuit breaker state: 0=closed, 1=open, 2=half-open.",
		}, []string{"name"}),

		DBQueryDuration: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "etafovivian",
			Subsystem: "dbaas_service",
			Name:      "db_query_duration_seconds",
			Help:      "Duration of PostgreSQL query execution in seconds.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"query"}),

		InstancesTotal: promauto.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "etafovivian",
			Subsystem: "dbaas_service",
			Name:      "instances_total",
			Help:      "Current number of database instances by tier and status.",
		}, []string{"tier", "status"}),

		ProvisionDuration: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "etafovivian",
			Subsystem: "dbaas_service",
			Name:      "provision_duration_seconds",
			Help:      "Duration of instance provisioning from create to available.",
			Buckets:   []float64{30, 60, 120, 180, 300, 600},
		}, []string{"tier"}),
	}
}
