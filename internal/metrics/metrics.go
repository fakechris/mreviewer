package metrics

import (
	"sync"
	"time"
)

type CounterSample struct {
	Name   string
	Labels map[string]string
	Value  int64
}

type HistogramSample struct {
	Name   string
	Labels map[string]string
	Value  int64
}

type Registry struct {
	mu         sync.Mutex
	counters   map[string]*counterRecord
	histograms map[string]*histogramRecord
}

type counterRecord struct {
	labels map[string]string
	value  int64
}

type histogramRecord struct {
	labels map[string]string
	values []int64
}

func NewRegistry() *Registry {
	return &Registry{
		counters:   map[string]*counterRecord{},
		histograms: map[string]*histogramRecord{},
	}
}

func (r *Registry) IncCounter(name string, labels map[string]string) {
	r.AddCounter(name, labels, 1)
}

func (r *Registry) AddCounter(name string, labels map[string]string, delta int64) {
	if r == nil || name == "" || delta == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := metricKey(name, labels)
	record, ok := r.counters[key]
	if !ok {
		record = &counterRecord{labels: cloneLabels(labels)}
		r.counters[key] = record
	}
	record.value += delta
}

func (r *Registry) ObserveHistogram(name string, labels map[string]string, value int64) {
	if r == nil || name == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := metricKey(name, labels)
	record, ok := r.histograms[key]
	if !ok {
		record = &histogramRecord{labels: cloneLabels(labels)}
		r.histograms[key] = record
	}
	record.values = append(record.values, value)
}

func (r *Registry) ObserveDuration(name string, labels map[string]string, d time.Duration) {
	r.ObserveHistogram(name, labels, d.Milliseconds())
}

func (r *Registry) CounterValue(name string, labels map[string]string) int64 {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if record, ok := r.counters[metricKey(name, labels)]; ok {
		return record.value
	}
	return 0
}

func (r *Registry) HistogramValues(name string, labels map[string]string) []int64 {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if record, ok := r.histograms[metricKey(name, labels)]; ok {
		return append([]int64(nil), record.values...)
	}
	return nil
}

func metricKey(name string, labels map[string]string) string {
	if len(labels) == 0 {
		return name
	}
	key := name
	for _, k := range orderedKeys(labels) {
		key += "|" + k + "=" + labels[k]
	}
	return key
}

func cloneLabels(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(labels))
	for k, v := range labels {
		cloned[k] = v
	}
	return cloned
}

func orderedKeys(labels map[string]string) []string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}
