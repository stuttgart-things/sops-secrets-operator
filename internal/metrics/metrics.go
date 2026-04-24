// Package metrics exposes Prometheus counters and histograms for the
// operator's reconcile paths. The metrics are registered with
// controller-runtime's shared registry in init() so main doesn't have
// to wire them explicitly.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	// ResultSuccess / ResultError are used for the "result" label on
	// reconcile counters and duration histograms.
	ResultSuccess = "success"
	ResultError   = "error"
)

// Reconcile lifecycle metrics. Labelled by kind (GitRepository, SopsSecret,
// SopsSecretManifest). Errors additionally carry a stage label so operators
// can see where a reconcile failed (auth / fetch / decrypt / parse / apply).
var (
	ReconcileTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "sops_reconcile_total",
			Help: "Total number of reconciles, labelled by kind and result.",
		},
		[]string{"kind", "result"},
	)

	ReconcileErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "sops_reconcile_errors_total",
			Help: "Reconcile failures by kind and stage (auth, fetch, decrypt, parse, apply).",
		},
		[]string{"kind", "stage"},
	)

	ReconcileDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "sops_reconcile_duration_seconds",
			Help:    "Reconcile wall time, labelled by kind and result.",
			Buckets: prometheus.ExponentialBuckets(0.005, 2, 12), // 5ms .. ~20s
		},
		[]string{"kind", "result"},
	)
)

// Git-source metrics. Populated by the source Registry around EnsureCached.
var (
	GitFetchDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "sops_git_fetch_duration_seconds",
			Help:    "Time spent in git clone/fetch/checkout on a cache refresh.",
			Buckets: prometheus.ExponentialBuckets(0.01, 2, 12), // 10ms .. ~40s
		},
		[]string{"result"},
	)
)

// Object-source metrics. Populated by the source Registry around
// EnsureObjectCached (HTTPS GET / HEAD or S3 StatObject+GetObject / BucketExists).
var (
	ObjectFetchDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "sops_object_fetch_duration_seconds",
			Help:    "Time spent fetching or probing an ObjectSource on a cache refresh.",
			Buckets: prometheus.ExponentialBuckets(0.01, 2, 12), // 10ms .. ~40s
		},
		[]string{"result"},
	)
)

// SOPS decrypt metrics. Populated by the decrypt package.
var (
	SopsDecryptDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "sops_decrypt_duration_seconds",
			Help:    "Time spent in SOPS decryption, labelled by result.",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 12), // 1ms .. ~4s
		},
		[]string{"result"},
	)
)

func init() {
	ctrlmetrics.Registry.MustRegister(
		ReconcileTotal,
		ReconcileErrorsTotal,
		ReconcileDurationSeconds,
		GitFetchDurationSeconds,
		ObjectFetchDurationSeconds,
		SopsDecryptDurationSeconds,
	)
}
