package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"reflect"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/daemon786/go-metrics/internal/metric"
)

type Agent struct {
	server       string
	metricsTimer time.Duration
	sendTimer    time.Duration
	metrics      metric.Metric
	metricsIn    chan metric.Metric
}

type config struct {
	server       string
	metricsTimer time.Duration
	sendTimer    time.Duration
}

func NewAgent(cfg config) *Agent {
	agent := Agent{
		server:       cfg.server,
		metricsTimer: cfg.metricsTimer,
		sendTimer:    cfg.sendTimer,
		metrics:      metric.Metric{},
		metricsIn:    make(chan metric.Metric),
	}

	return &agent
}

func (a *Agent) Monitor(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()
	wgMetrics := sync.WaitGroup{}
	ctxMetrics, cancel := context.WithCancel(context.Background())
	go a.getMetrics(ctxMetrics)
	ticker := time.NewTicker(a.sendTimer)
	for {
		select {
		case <-ticker.C:
			wgMetrics.Add(1)
			go a.sendMetrics(a.metrics, &wgMetrics)
		case metrics := <-a.metricsIn:
			a.metrics = metrics
		case <-ctx.Done():
			cancel()
			wgMetrics.Wait()
			return
		}
	}
}

func (a *Agent) getMetrics(ctx context.Context) {
	ticker := time.NewTicker(a.metricsTimer)
	for {
		select {
		case <-ticker.C:
			a.metricsIn <- a.gatherMetrics(a.metrics.PollCount)
		case <-ctx.Done():
			return
		}
	}
}

func (a *Agent) gatherMetrics(counter metric.Counter) metric.Metric {
	var rtm runtime.MemStats
	runtime.ReadMemStats(&rtm)
	seed := rand.NewSource(time.Now().UnixNano())
	rg := rand.New(seed)
	metrics := metric.Metric{
		Alloc:         metric.Gauge(rtm.Alloc),
		BuckHashSys:   metric.Gauge(rtm.BuckHashSys),
		Frees:         metric.Gauge(rtm.Frees),
		GCCPUFraction: metric.Gauge(rtm.GCCPUFraction),
		GCSys:         metric.Gauge(rtm.GCSys),
		HeapAlloc:     metric.Gauge(rtm.HeapAlloc),
		HeapIdle:      metric.Gauge(rtm.HeapIdle),
		HeapInuse:     metric.Gauge(rtm.HeapInuse),
		HeapObjects:   metric.Gauge(rtm.HeapObjects),
		HeapReleased:  metric.Gauge(rtm.HeapReleased),
		HeapSys:       metric.Gauge(rtm.HeapSys),
		LastGC:        metric.Gauge(rtm.LastGC),
		Lookups:       metric.Gauge(rtm.Lookups),
		MCacheInuse:   metric.Gauge(rtm.MCacheInuse),
		MCacheSys:     metric.Gauge(rtm.MCacheSys),
		MSpanInuse:    metric.Gauge(rtm.MSpanInuse),
		MSpanSys:      metric.Gauge(rtm.MSpanSys),
		Mallocs:       metric.Gauge(rtm.Mallocs),
		NextGC:        metric.Gauge(rtm.NextGC),
		NumForcedGC:   metric.Gauge(rtm.NumForcedGC),
		NumGC:         metric.Gauge(rtm.NumGC),
		OtherSys:      metric.Gauge(rtm.OtherSys),
		PauseTotalNs:  metric.Gauge(rtm.PauseTotalNs),
		StackInuse:    metric.Gauge(rtm.StackInuse),
		StackSys:      metric.Gauge(rtm.StackSys),
		Sys:           metric.Gauge(rtm.Sys),
		TotalAlloc:    metric.Gauge(rtm.TotalAlloc),
		PollCount:     counter + 1,
		RandomValue:   metric.Gauge(rg.Int()),
	}
	return metrics
}

func (a *Agent) sendMetrics(metrics metric.Metric, wg *sync.WaitGroup) {
	defer wg.Done()
	wgSend := sync.WaitGroup{}
	value := reflect.ValueOf(metrics)
	structType := value.Type()
	for index := 0; index < value.NumField(); index++ {
		fieldName := structType.Field(index).Name
		fieldValue := value.Field(index).Interface()
		switch data := fieldValue.(type) {
		case metric.Counter:
			wgSend.Add(1)
			go a.sendMetric(fieldName, data.TypeName(), fmt.Sprintf("%d", data), &wgSend)
		case metric.Gauge:
			wgSend.Add(1)
			go a.sendMetric(fieldName, data.TypeName(), fmt.Sprintf("%.6f", data), &wgSend)
		default:
			log.Printf("Unexpected type %T", data)
		}
	}
	wgSend.Wait()
}

func (a *Agent) sendMetric(name, metricType, value string, wg *sync.WaitGroup) {
	defer wg.Done()
	client := &http.Client{}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/update/%s/%s/%s", a.server, metricType, name, value), nil)
	if err != nil {
		log.Printf("%v", err)
		return
	}
	request.Header.Set("application-type", "text/plain")
	resp, err := client.Do(request)
	if err != nil {
		log.Printf("%v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Send metric %q type %q value %q: status %d", name, metricType, value, resp.StatusCode)
		return
	}
}

func main() {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT)
	done := make(chan struct{})
	go func() {
		<-sigs
		done <- struct{}{}
	}()

	cfg := config{
		server:       "http://localhost:8080",
		metricsTimer: 2 * time.Second,
		sendTimer:    10 * time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	agent := NewAgent(cfg)
	wg := sync.WaitGroup{}
	wg.Add(1)
	go agent.Monitor(ctx, &wg)

	<-done
	cancel()
	wg.Wait()
}
