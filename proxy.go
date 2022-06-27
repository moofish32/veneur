package veneur

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/pprof"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"context"

	"github.com/DataDog/datadog-go/statsd"
	"github.com/getsentry/sentry-go"
	"github.com/golang/protobuf/ptypes/empty"
	"github.com/hashicorp/consul/api"
	"github.com/pkg/profile"
	"github.com/sirupsen/logrus"
	"github.com/stripe/veneur/v14/discovery"
	"github.com/stripe/veneur/v14/discovery/consul"
	"github.com/stripe/veneur/v14/discovery/kubernetes"
	"github.com/stripe/veneur/v14/forwardrpc"
	vhttp "github.com/stripe/veneur/v14/http"
	"github.com/stripe/veneur/v14/samplers"
	"github.com/stripe/veneur/v14/samplers/metricpb"
	"github.com/stripe/veneur/v14/scopedstatsd"
	"github.com/stripe/veneur/v14/ssf"
	"github.com/stripe/veneur/v14/trace"
	"github.com/stripe/veneur/v14/trace/metrics"
	"github.com/stripe/veneur/v14/util/build"
	"github.com/stripe/veneur/v14/util/matcher"
	"github.com/zenazn/goji/bind"
	"github.com/zenazn/goji/graceful"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
	"stathat.com/c/consistent"

	"goji.io"
	"goji.io/pat"
)

type Proxy struct {
	Hostname                 string
	ForwardDestinations      *consistent.Consistent
	Discoverer               discovery.Discoverer
	ConsulForwardService     string
	ConsulForwardGrpcService string
	ConsulInterval           time.Duration
	MetricsInterval          time.Duration
	ForwardDestinationsMtx   sync.Mutex
	HTTPAddr                 string
	HTTPClient               *http.Client
	AcceptingForwards        bool
	AcceptingGRPCForwards    bool
	ForwardTimeout           time.Duration

	ignoreTags      []matcher.TagMatcher
	logger          *logrus.Entry
	statsd          scopedstatsd.Client
	usingConsul     bool
	usingKubernetes bool
	enableProfiling bool
	TraceClient     *trace.Client

	// gRPC
	destinations     map[string]Destination
	destinationsHash *consistent.Consistent
	dialTimeout      time.Duration
	forwardAddresses []string
	grpcAddress      string
	grpcListener     net.Listener
	grpcServer       *grpc.Server
	shutdownTimeout  time.Duration

	// HTTP
	// An atomic boolean for whether or not the HTTP server is listening
	numListeningHTTP *int32
}

type Destination struct {
	client forwardrpc.Forward_SendMetricsV2Client
}

func NewProxyFromConfig(
	logger *logrus.Logger, conf ProxyConfig, statsdClient scopedstatsd.Client,
) (*Proxy, error) {
	hostname, err := os.Hostname()
	if err != nil {
		logger.WithError(err).Error("error finding hostname")
		return nil, err
	}

	proxy := &Proxy{
		AcceptingForwards:        conf.ConsulForwardServiceName != "" || conf.ForwardAddress != "",
		AcceptingGRPCForwards:    conf.ConsulForwardGrpcServiceName != "" || len(conf.GrpcForwardAddress) > 0,
		ConsulForwardService:     conf.ConsulForwardServiceName,
		ConsulForwardGrpcService: conf.ConsulForwardGrpcServiceName,
		destinations:             map[string]Destination{},
		destinationsHash:         consistent.New(),
		dialTimeout:              conf.DialTimeout,
		enableProfiling:          conf.EnableProfiling,
		forwardAddresses:         conf.GrpcForwardAddress,
		ForwardDestinations:      consistent.New(),
		ForwardTimeout:           conf.ForwardTimeout,
		grpcServer:               grpc.NewServer(),
		Hostname:                 hostname,
		HTTPAddr:                 conf.HTTPAddress,
		HTTPClient: &http.Client{
			Transport: &http.Transport{
				IdleConnTimeout: conf.IdleConnectionTimeout,
				// Each of these properties DTRT (according to Go docs) when supplied
				// with zero values as of Go 0.10.3
				MaxIdleConns:        conf.MaxIdleConns,
				MaxIdleConnsPerHost: conf.MaxIdleConnsPerHost,
			},
		},
		ignoreTags:       conf.IgnoreTags,
		logger:           logrus.NewEntry(logger),
		MetricsInterval:  conf.RuntimeMetricsInterval,
		numListeningHTTP: new(int32),
		shutdownTimeout:  conf.ShutdownTimeout,
		statsd:           statsdClient,
		usingConsul:      conf.ConsulForwardServiceName != "" || conf.ConsulForwardGrpcServiceName != "",
	}

	if conf.SentryDsn != "" {
		err = sentry.Init(sentry.ClientOptions{
			Dsn:        conf.SentryDsn,
			ServerName: hostname,
			Release:    build.VERSION,
		})
		if err != nil {
			return nil, err
		}

		logger.AddHook(SentryHook{
			Level: []logrus.Level{
				logrus.ErrorLevel,
				logrus.FatalLevel,
				logrus.PanicLevel,
			},
		})
	}

	// check if we are running on Kubernetes
	_, err = os.Stat("/var/run/secrets/kubernetes.io/serviceaccount")
	if !os.IsNotExist(err) {
		logger.Info("Using Kubernetes for service discovery")
		proxy.usingKubernetes = true

		//TODO don't overload this
		proxy.AcceptingForwards = conf.ConsulForwardServiceName != ""
	}

	// We got a static forward address, stick it in the destination!
	if proxy.ConsulForwardService == "" && conf.ForwardAddress != "" {
		proxy.ForwardDestinations.Add(conf.ForwardAddress)
	}

	if !proxy.AcceptingForwards &&
		!proxy.AcceptingGRPCForwards {
		err = errors.New(
			"refusing to start with no Consul service names or static addresses in config")
		logger.WithError(err).WithFields(logrus.Fields{
			"consul_forward_service_name":      proxy.ConsulForwardService,
			"consul_forward_grpc_service_name": proxy.ConsulForwardGrpcService,
			"forward_address":                  conf.ForwardAddress,
			"trace_address":                    conf.TraceAddress,
		}).Error("Oops")
		return nil, err
	}

	if proxy.usingConsul {
		proxy.ConsulInterval = conf.ConsulRefreshInterval
		logger.WithField("interval", conf.ConsulRefreshInterval).
			Info("Will use Consul for service discovery")
	}

	proxy.TraceClient = trace.DefaultClient
	if conf.SsfDestinationAddress.Value != nil {
		stats, err := statsd.New(
			conf.StatsAddress, statsd.WithoutTelemetry(),
			statsd.WithMaxMessagesPerPayload(4096))
		if err != nil {
			return nil, err
		}
		stats.Namespace = "veneur_proxy."
		format := "ssf_format:packet"
		if conf.SsfDestinationAddress.Value.Scheme == "unix" {
			format = "ssf_format:framed"
		}

		proxy.TraceClient, err = trace.NewClient(conf.SsfDestinationAddress.Value,
			trace.Buffered,
			trace.Capacity(uint(conf.TracingClientCapacity)),
			trace.FlushInterval(conf.TracingClientFlushInterval),
			trace.ReportStatistics(
				stats, conf.TracingClientMetricsInterval, []string{format}),
		)
		if err != nil {
			logger.WithField("ssf_destination_address", conf.SsfDestinationAddress).
				WithError(err).
				Fatal("Error using SSF destination address")
		}
	}

	logger.WithField("config", conf).Debug("Initialized server")

	return proxy, nil
}

// Start fires up the various goroutines that run on behalf of the server.
// This is separated from the constructor for testing convenience.
func (proxy *Proxy) Start(ctx context.Context) {
	proxy.logger.WithField("version", build.VERSION).Info("Starting server")

	config := api.DefaultConfig()
	// Use the same HTTP Client we're using for other things, so we can leverage
	// it for testing.
	config.HttpClient = proxy.HTTPClient

	if proxy.usingKubernetes {
		disc, err := kubernetes.NewKubernetesDiscoverer(proxy.logger)
		if err != nil {
			proxy.logger.WithError(err).Error("Error creating KubernetesDiscoverer")
			return
		}
		proxy.Discoverer = disc
		proxy.logger.Info("Set Kubernetes discoverer")
	} else if proxy.usingConsul {
		disc, consulErr := consul.NewConsul(config)
		if consulErr != nil {
			proxy.logger.WithError(consulErr).
				Error("Error creating Consul discoverer")
			return
		}
		proxy.Discoverer = disc
		proxy.logger.Info("Set Consul discoverer")
	}

	if proxy.AcceptingForwards && proxy.ConsulForwardService != "" {
		proxy.RefreshDestinations(proxy.ConsulForwardService,
			proxy.ForwardDestinations, &proxy.ForwardDestinationsMtx)
		if len(proxy.ForwardDestinations.Members()) == 0 {
			proxy.logger.WithField("serviceName", proxy.ConsulForwardService).
				Fatal("Refusing to start with zero destinations for forwarding.")
		}
	}

	if proxy.usingConsul || proxy.usingKubernetes {
		proxy.logger.Info("Creating service discovery goroutine")
		go func() {
			defer func() {
				ConsumePanic(proxy.TraceClient, proxy.Hostname, recover())
			}()
			ticker := time.NewTicker(proxy.ConsulInterval)
			for range ticker.C {
				proxy.logger.WithFields(logrus.Fields{
					"acceptingForwards":        proxy.AcceptingForwards,
					"consulForwardService":     proxy.ConsulForwardService,
					"consulForwardGRPCService": proxy.ConsulForwardGrpcService,
				}).Debug("About to refresh destinations")
				if proxy.AcceptingForwards && proxy.ConsulForwardService != "" {
					proxy.RefreshDestinations(
						proxy.ConsulForwardService, proxy.ForwardDestinations,
						&proxy.ForwardDestinationsMtx)
				}
			}
		}()
	}

	// Add static gRPC destinations.
	proxy.addDestinations(ctx, proxy.forwardAddresses)

	// Poll discovery for available gRPC destinations.
	go func() {
		proxy.handleDiscovery(ctx)
		discoveryTicker := time.NewTicker(proxy.ConsulInterval)
		for {
			select {
			case <-discoveryTicker.C:
				go func() {
					proxy.handleDiscovery(ctx)
				}()
			case <-ctx.Done():
				discoveryTicker.Stop()
				return
			}
		}
	}()
}

// handleDiscovery uses the discoverer to find the current set of destination
// servers, identifies which ones are new, and establishes connections to them.
func (proxy *Proxy) handleDiscovery(ctx context.Context) {
	startTime := time.Now()

	newDestinations, err :=
		proxy.Discoverer.GetDestinationsForService(proxy.ConsulForwardGrpcService)
	if err != nil {
		logrus.WithField("error", err).Error("failed to discover destinations")
		proxy.statsd.Count(
			"veneur_proxy.discovery.count", 1, []string{"status:fail"}, 1.0)
		return
	}

	addedDestinations := []string{}
	for _, newDestination := range newDestinations {
		_, ok := proxy.destinations[newDestination]
		if !ok {
			addedDestinations = append(addedDestinations, newDestination)
		}
	}

	proxy.addDestinations(ctx, addedDestinations)

	time.Since(startTime)
	proxy.statsd.Timing(
		"veneur_proxy.discovery.duration", time.Since(startTime), []string{}, 1.0)
	proxy.statsd.Count(
		"veneur_proxy.discovery.count", 1, []string{"status:success"}, 1.0)
}

// addDestinations starts a streaming connection with each of the destination
// addresses provided.
func (proxy *Proxy) addDestinations(
	ctx context.Context, destinations []string,
) {
	waitGroup := sync.WaitGroup{}
	for _, addedDestination := range destinations {
		waitGroup.Add(1)
		go func(addedDestination string) {
			// Establish a connection.
			dialContext, cancel := context.WithTimeout(ctx, proxy.dialTimeout)
			connection, err := grpc.DialContext(
				dialContext, addedDestination, grpc.WithBlock(), grpc.WithInsecure())
			cancel()
			if err != nil {
				logrus.WithError(err).WithField("destination", addedDestination).
					Error("failed to dial destination")
				proxy.statsd.Count(
					"veneur_proxy.forward.connect", 1,
					[]string{"status:failed_dial"}, 1.0)
				return
			}

			// Start a streaming request.
			forwardClient := forwardrpc.NewForwardClient(connection)
			client, err := forwardClient.SendMetricsV2(ctx)
			if err != nil {
				logrus.WithError(err).WithField("destination", addedDestination).
					Error("failed to connect to destination")
				proxy.statsd.Count(
					"veneur_proxy.forward.connect", 1,
					[]string{"status:failed_connect"}, 1.0)
				return
			}
			proxy.destinations[addedDestination] = Destination{
				client: client,
			}
			proxy.destinationsHash.Add(addedDestination)
			proxy.statsd.Count(
				"veneur_proxy.forward.connect", 1, []string{"status:success"}, 1.0)

			// Listen for the request to be closed by the server.
			go func() {
				var empty empty.Empty
				err := client.RecvMsg(&empty)
				if err != nil {
					logrus.WithError(err).WithField("destination", addedDestination).
						Error("disconnected from destination")
					proxy.statsd.Count(
						"veneur_proxy.forward.disconnect", 1, []string{"error:true"}, 1.0)
				} else {
					proxy.statsd.Count(
						"veneur_proxy.forward.disconnect", 1, []string{"error:false"}, 1.0)
				}
				proxy.destinationsHash.Remove(addedDestination)
				delete(proxy.destinations, addedDestination)
				connection.Close()
			}()
		}(addedDestination)
	}
	waitGroup.Wait()
}

func (proxy *Proxy) SendMetrics(
	ctx context.Context, metricList *forwardrpc.MetricList,
) (*empty.Empty, error) {
	proxy.statsd.Count(
		"veneur_proxy.ingest.request_count", 1,
		[]string{"protocol:grpc-single"}, 1.0)
	proxy.statsd.Count(
		"veneur_proxy.ingest.metrics_count", int64(len(metricList.Metrics)),
		[]string{"protocol:grpc-single"}, 1.0)
	errorCount := 0
	for _, metric := range metricList.Metrics {
		err := proxy.handleMetric(metric)
		if err != nil {
			errorCount += 1
		}
	}
	return &emptypb.Empty{}, nil
}

func (proxy *Proxy) SendMetricsV2(
	server forwardrpc.Forward_SendMetricsV2Server,
) error {
	proxy.statsd.Count(
		"veneur_proxy.ingest.request_count", 1,
		[]string{"protocol:grpc-stream"}, 1.0)
	errorCount := 0
	for {
		metric, err := server.Recv()
		if err == io.EOF {
			return nil
		} else if err != nil {
			return err
		}
		err = proxy.handleMetric(metric)
		if err != nil {
			errorCount += 1
			continue
		}
	}
}

func (proxy *Proxy) handleMetric(metric *metricpb.Metric) error {
	tags := []string{}
tagLoop:
	for _, tag := range metric.Tags {
		for _, matcher := range proxy.ignoreTags {
			if matcher.Match(tag) {
				continue tagLoop
			}
		}
		tags = append(tags, tag)
	}
	key := fmt.Sprintf("%s%s%s",
		metric.Name, strings.ToLower(metric.Type.String()), strings.Join(tags, ","))
	destinationAddress, err := proxy.destinationsHash.Get(key)
	if err != nil {
		return err
	}
	destination, ok := proxy.destinations[destinationAddress]
	if !ok {
		return fmt.Errorf("unknown destination: %s", destinationAddress)
	}
	return destination.client.Send(metric)
}

// Start all of the the configured servers (gRPC or HTTP) and block until
// one of them exist.  At that point, stop them both.
func (proxy *Proxy) Serve() {
	// Listen on gRPC address
	grpcListener, err := net.Listen("tcp", proxy.grpcAddress)
	if err != nil {
		proxy.logger.WithError(err).Fatalf(
			"failed to listen on grpc address: tcp://%s", proxy.grpcAddress)
	}
	defer grpcListener.Close()
	proxy.grpcListener = grpcListener

	done := make(chan struct{}, 2)

	go func() {
		proxy.HTTPServe()
		done <- struct{}{}
	}()

	// Start gRPC server
	go func() {
		err := proxy.grpcServer.Serve(proxy.grpcListener)
		if err != nil {
			proxy.logger.Errorf("grpc server error: %v", err)
		}
		done <- struct{}{}
	}()

	// wait until at least one of the servers has shut down
	<-done
	proxy.Shutdown()
}

// HTTPServe starts the HTTP server and listens perpetually until it encounters an unrecoverable error.
func (p *Proxy) HTTPServe() {
	var prf interface {
		Stop()
	}

	// We want to make sure the profile is stopped
	// exactly once (and only once), even if the
	// shutdown pre-hook does not run (which it may not)
	profileStopOnce := sync.Once{}

	if p.enableProfiling {
		profileStartOnce.Do(func() {
			prf = profile.Start()
		})

		defer func() {
			profileStopOnce.Do(prf.Stop)
		}()
	}
	httpSocket := bind.Socket(p.HTTPAddr)
	graceful.Timeout(10 * time.Second)
	graceful.PreHook(func() {

		if prf != nil {
			profileStopOnce.Do(prf.Stop)
		}

		p.logger.Info("Terminating HTTP listener")
	})

	// Ensure that the server responds to SIGUSR2 even
	// when *not* running under einhorn.
	graceful.AddSignal(syscall.SIGUSR2, syscall.SIGHUP)
	graceful.HandleSignals()
	gracefulSocket := graceful.WrapListener(httpSocket)
	p.logger.WithField("address", p.HTTPAddr).Info("HTTP server listening")

	// Signal that the HTTP server is listening
	atomic.AddInt32(p.numListeningHTTP, 1)
	defer atomic.AddInt32(p.numListeningHTTP, -1)
	bind.Ready()

	if err := http.Serve(gracefulSocket, p.Handler()); err != nil {
		p.logger.WithError(err).Error("HTTP server shut down due to error")
	}
	p.logger.Info("Stopped HTTP server")

	graceful.Shutdown()
}

// RefreshDestinations updates the server's list of valid destinations
// for flushing. This should be called periodically to ensure we have
// the latest data.
func (p *Proxy) RefreshDestinations(serviceName string, ring *consistent.Consistent, mtx *sync.Mutex) {
	samples := &ssf.Samples{}
	defer metrics.Report(p.TraceClient, samples)
	srvTags := map[string]string{"service": serviceName}

	start := time.Now()
	destinations, err := p.Discoverer.GetDestinationsForService(serviceName)
	samples.Add(ssf.Timing("discoverer.update_duration_ns", time.Since(start), time.Nanosecond, srvTags))
	p.logger.WithFields(logrus.Fields{
		"destinations": destinations,
		"service":      serviceName,
	}).Debug("Got destinations")

	samples.Add(ssf.Timing("discoverer.update_duration_ns", time.Since(start), time.Nanosecond, srvTags))
	if err != nil || len(destinations) == 0 {
		p.logger.WithError(err).WithFields(logrus.Fields{
			"service":         serviceName,
			"errorType":       reflect.TypeOf(err),
			"numDestinations": len(destinations),
		}).Error("Discoverer found zero destinations and/or returned an error. Destinations may be stale!")
		samples.Add(ssf.Count("discoverer.errors", 1, srvTags))
		// Return since we got no hosts. We don't want to zero out the list. This
		// should result in us leaving the "last good" values in the ring.
		return
	}

	mtx.Lock()
	ring.Set(destinations)
	mtx.Unlock()
	samples.Add(ssf.Gauge("discoverer.destination_number", float32(len(destinations)), srvTags))
}

// Handler returns the Handler responsible for routing request processing.
func (p *Proxy) Handler() http.Handler {
	mux := goji.NewMux()

	mux.HandleFunc(pat.Get("/builddate"), func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(build.BUILD_DATE))
	})
	mux.HandleFunc(pat.Get("/healthcheck"), func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok\n"))
	})
	mux.HandleFunc(pat.Get("/version"), func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(build.VERSION))
	})

	mux.Handle(pat.Post("/import"), http.HandlerFunc(p.handleProxy))

	mux.Handle(pat.Get("/debug/pprof/cmdline"), http.HandlerFunc(pprof.Cmdline))
	mux.Handle(pat.Get("/debug/pprof/profile"), http.HandlerFunc(pprof.Profile))
	mux.Handle(pat.Get("/debug/pprof/symbol"), http.HandlerFunc(pprof.Symbol))
	mux.Handle(pat.Get("/debug/pprof/trace"), http.HandlerFunc(pprof.Trace))
	// TODO match without trailing slash as well
	mux.Handle(pat.Get("/debug/pprof/*"), http.HandlerFunc(pprof.Index))

	return mux
}

func (p *Proxy) handleProxy(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()
	p.logger.WithFields(logrus.Fields{
		"path": r.URL.Path,
		"host": r.URL.Host,
	}).Debug("Importing metrics on proxy")
	span, jsonMetrics, err := unmarshalMetricsFromHTTP(
		ctx, p.TraceClient, w, r, p.logger)
	if err != nil {
		p.logger.WithError(err).
			Error("Error unmarshalling metrics in proxy import")
		return
	}
	// the server usually waits for this to return before finalizing the
	// response, so this part must be done asynchronously
	go p.ProxyMetrics(span.Attach(ctx), jsonMetrics, strings.SplitN(r.RemoteAddr, ":", 2)[0])
}

// ProxyMetrics takes a slice of JSONMetrics and breaks them up into
// multiple HTTP requests by MetricKey using the hash ring.
func (p *Proxy) ProxyMetrics(ctx context.Context, jsonMetrics []samplers.JSONMetric, origin string) {
	span, _ := trace.StartSpanFromContext(ctx, "veneur.opentracing.proxy.proxy_metrics")
	defer span.ClientFinish(p.TraceClient)

	if p.ForwardTimeout > 0 {
		var cancel func()
		ctx, cancel = context.WithTimeout(ctx, p.ForwardTimeout)
		defer cancel()
	}
	metricCount := len(jsonMetrics)
	span.Add(ssf.RandomlySample(0.1,
		ssf.Count("import.metrics_total", float32(metricCount), map[string]string{
			"remote_addr":      origin,
			"veneurglobalonly": "",
		}),
	)...)

	jsonMetricsByDestination := make(map[string][]samplers.JSONMetric)
	for _, h := range p.ForwardDestinations.Members() {
		jsonMetricsByDestination[h] = make([]samplers.JSONMetric, 0)
	}

	for _, jm := range jsonMetrics {
		dest, _ := p.ForwardDestinations.Get(jm.MetricKey.String())
		jsonMetricsByDestination[dest] = append(jsonMetricsByDestination[dest], jm)
	}

	// nb The response has already been returned at this point, because we
	wg := sync.WaitGroup{}
	wg.Add(len(jsonMetricsByDestination)) // Make our waitgroup the size of our destinations

	for dest, batch := range jsonMetricsByDestination {
		go p.doPost(ctx, &wg, dest, batch)
	}
	wg.Wait() // Wait for all the above goroutines to complete
	p.logger.WithField("count", metricCount).Debug("Completed forward")

	span.Add(ssf.RandomlySample(0.1,
		ssf.Timing("proxy.duration_ns", time.Since(span.Start), time.Nanosecond, nil),
		ssf.Count("proxy.proxied_metrics_total", float32(len(jsonMetrics)), nil),
	)...)
}

func (p *Proxy) doPost(ctx context.Context, wg *sync.WaitGroup, destination string, batch []samplers.JSONMetric) {
	defer wg.Done()

	samples := &ssf.Samples{}
	defer metrics.Report(p.TraceClient, samples)

	batchSize := len(batch)
	if batchSize < 1 {
		return
	}

	// Make sure the destination always has a valid 'http' prefix.
	if !strings.HasPrefix(destination, "http") {
		u := url.URL{Scheme: "http", Host: destination}
		destination = u.String()
	}

	endpoint := fmt.Sprintf("%s/import", destination)
	err := vhttp.PostHelper(
		ctx, p.HTTPClient, p.TraceClient, http.MethodPost, endpoint, batch,
		"forward", true, nil, p.logger)
	if err == nil {
		p.logger.WithField("metrics", batchSize).
			Debug("Completed forward to Veneur")
	} else {
		samples.Add(ssf.Count("forward.error_total", 1, map[string]string{"cause": "post"}))
		p.logger.WithError(err).WithFields(logrus.Fields{
			"endpoint":  endpoint,
			"batchSize": batchSize,
		}).Warn("Failed to POST metrics to destination")
	}
	samples.Add(ssf.RandomlySample(0.1,
		ssf.Count("metrics_by_destination", float32(batchSize), map[string]string{"destination": destination, "protocol": "http"}),
	)...)
}

// Shutdown signals the server to shut down after closing all
// current connections.
func (proxy *Proxy) Shutdown() {
	proxy.logger.Info("Shutting down server gracefully")
	graceful.Shutdown()

	ctx, cancel :=
		context.WithTimeout(context.Background(), proxy.shutdownTimeout)
	defer cancel()

	done := make(chan struct{})
	go func() {
		proxy.grpcServer.GracefulStop()
		close(done)
	}()

	select {
	case <-done:
		break
	case <-ctx.Done():
		proxy.grpcServer.Stop()
		proxy.logger.Errorf("error shuting down grpc server: %v", ctx.Err())
	}
}

// IsListeningHTTP returns if the Proxy is currently listening over HTTP
func (p *Proxy) IsListeningHTTP() bool {
	return atomic.LoadInt32(p.numListeningHTTP) > 0
}
