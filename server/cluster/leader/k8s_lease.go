package leader

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

// K8sLease implements leader election using Kubernetes Lease API.
type K8sLease struct {
	Namespace  string
	LeaseName  string
	Identity   string
	Duration   time.Duration
	Renew      time.Duration
	Retry      time.Duration
	OnStart    func(context.Context)
	OnStop     func()
	cancelFunc context.CancelFunc
	done       chan struct{}

	client     kubernetes.Interface
	lock       resourcelock.Interface
	isLeading  atomic.Bool
	logger     Logger
}

// NewK8sLease creates a new Kubernetes lease-based leader election client.
// It uses in-cluster config if available, otherwise falls back to kubeconfig.
func NewK8sLease(opts K8sLeaseOptions) (*K8sLease, error) {
	if opts.LeaseName == "" {
		return nil, errors.New("k8s lease requires a lease name")
	}
	if opts.Identity == "" {
		return nil, errors.New("k8s lease requires an identity")
	}
	if opts.Namespace == "" {
		return nil, errors.New("k8s lease requires a namespace")
	}

	// Get Kubernetes config
	// Try in-cluster config first (when running in a pod)
	config, err := rest.InClusterConfig()
	if err != nil {
		// Fallback to kubeconfig from default location (~/.kube/config)
		// This is useful for local development
		loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
		kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			loadingRules,
			&clientcmd.ConfigOverrides{},
		)
		config, err = kubeConfig.ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to get k8s config (tried in-cluster and kubeconfig): %w", err)
		}
	}

	// Create Kubernetes client
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create k8s client: %w", err)
	}

	duration := opts.Duration
	if duration <= 0 {
		duration = 15 * time.Second
	}

	renew := opts.Renew
	if renew <= 0 {
		renew = duration / 3
		if renew <= 0 {
			renew = 5 * time.Second
		}
	}

	retry := opts.Retry
	if retry <= 0 {
		retry = renew / 2
		if retry <= 0 {
			retry = 2 * time.Second
		}
	}

	// Create resource lock for leader election
	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      opts.LeaseName,
			Namespace: opts.Namespace,
		},
		Client: clientset.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: opts.Identity,
		},
	}

	return &K8sLease{
		Namespace:  opts.Namespace,
		LeaseName:  opts.LeaseName,
		Identity:   opts.Identity,
		Duration:   duration,
		Renew:      renew,
		Retry:      retry,
		OnStart:    opts.OnStart,
		OnStop:     opts.OnStop,
		client:     clientset,
		lock:       lock,
		done:       make(chan struct{}),
		logger:     opts.Logger,
	}, nil
}

// Logger interface for K8sLease
type Logger interface {
	Info(msg string, keyvals ...interface{})
	Warn(msg string, keyvals ...interface{})
	Error(msg string, keyvals ...interface{})
	Debug(msg string, keyvals ...interface{})
}

// K8sLeaseOptions configures the Kubernetes lease-based leader election.
type K8sLeaseOptions struct {
	Namespace string
	LeaseName string
	Identity  string
	Duration  time.Duration
	Renew     time.Duration
	Retry     time.Duration
	OnStart   func(context.Context)
	OnStop    func()
	Logger    Logger
}

// Start begins the leader election loop.
func (l *K8sLease) Start(parent context.Context) error {
	if l.cancelFunc != nil {
		return errors.New("k8s lease already started")
	}

	ctx, cancel := context.WithCancel(parent)
	l.cancelFunc = cancel

	// Configure leader election
	lec := leaderelection.LeaderElectionConfig{
		Lock:            l.lock,
		LeaseDuration:   l.Duration,
		RenewDeadline:   l.Renew,
		RetryPeriod:     l.Retry,
		ReleaseOnCancel: true,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				if l.logger != nil {
					l.logger.Info("k8s lease acquired", "lease_name", l.LeaseName, "identity", l.Identity)
				}
				l.isLeading.Store(true)
				if l.OnStart != nil {
					go l.OnStart(ctx)
				}
			},
			OnStoppedLeading: func() {
				if l.logger != nil {
					l.logger.Info("k8s lease lost", "lease_name", l.LeaseName, "identity", l.Identity)
				}
				l.isLeading.Store(false)
				if l.OnStop != nil {
					l.OnStop()
				}
			},
			OnNewLeader: func(identity string) {
				if l.logger != nil {
					if identity == l.Identity {
						l.logger.Debug("k8s lease: we are the leader", "lease_name", l.LeaseName)
					} else {
						l.logger.Debug("k8s lease: new leader elected", "lease_name", l.LeaseName, "leader", identity)
					}
				}
			},
		},
	}

	// Create and start leader elector
	le, err := leaderelection.NewLeaderElector(lec)
	if err != nil {
		return fmt.Errorf("failed to create leader elector: %w", err)
	}

	// Start leader election in a goroutine
	go func() {
		defer close(l.done)
		le.Run(ctx)
	}()

	return nil
}

// Stop stops the leader election and releases the lease if held.
func (l *K8sLease) Stop() {
	if l.cancelFunc != nil {
		l.cancelFunc()
	}
	if l.done != nil {
		<-l.done
	}
	if l.OnStop != nil && l.isLeading.Load() {
		l.OnStop()
	}
}

// IsLeader returns whether this instance is currently the leader.
func (l *K8sLease) IsLeader() bool {
	return l.isLeading.Load()
}



