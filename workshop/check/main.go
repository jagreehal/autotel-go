// Command check runs every part of the workshop against a fake OTLP/HTTP receiver
// and fails unless each solution exported what its part teaches.
//
//	go run ./workshop/check       # every part
//	go run ./workshop/check 07    # one part
//
// Run it from the repository root. What each solution must export is written at
// the top of its main.go:
//
//	// check: name = "atm.withdraw" and duration > 200   a span matching every term
//	// check-count: 16 name = "atm.withdraw"             exactly that many spans match
//	// check-child: bank.debit of atm.withdraw           a span nested under another
//	// check-one-trace: atm, auth-service                one trace holds all of them
//	// check-metric: atm.withdrawals                     a metric with that name
//	// check-log: pin ok                                 a log with that text and a trace id
//	// check-stdout: withdrawal_id                       the program printed that text
//
// Exercises only have to build and exit cleanly.
package main

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	collectorlogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/proto"
)

const modulePath = "github.com/jagreehal/autotel-go/v2/"

type span struct {
	service, name, status string
	traceID, id, parentID string
	durationMs            float64
	attributes            map[string]string
}

type logRecord struct{ body, traceID string }

// receiver decodes what the workshop's processes export.
type receiver struct {
	mu      sync.Mutex
	spans   []span
	metrics map[string]bool
	logs    []logRecord
}

func (r *receiver) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.spans, r.metrics, r.logs = nil, map[string]bool{}, nil
}

func attributeMap(attributes []*commonpb.KeyValue) map[string]string {
	out := make(map[string]string, len(attributes))
	for _, attribute := range attributes {
		out[attribute.GetKey()] = valueText(attribute.GetValue())
	}
	return out
}

func valueText(value *commonpb.AnyValue) string {
	switch v := value.GetValue().(type) {
	case *commonpb.AnyValue_StringValue:
		return v.StringValue
	case *commonpb.AnyValue_IntValue:
		return strconv.FormatInt(v.IntValue, 10)
	case *commonpb.AnyValue_DoubleValue:
		return strconv.FormatFloat(v.DoubleValue, 'f', -1, 64)
	case *commonpb.AnyValue_BoolValue:
		return strconv.FormatBool(v.BoolValue)
	}
	return ""
}

func (r *receiver) addSpans(export *collectortrace.ExportTraceServiceRequest) {
	for _, resource := range export.GetResourceSpans() {
		service := attributeMap(resource.GetResource().GetAttributes())["service.name"]
		for _, scope := range resource.GetScopeSpans() {
			for _, s := range scope.GetSpans() {
				r.spans = append(r.spans, span{
					service:    service,
					name:       s.GetName(),
					status:     strings.TrimPrefix(s.GetStatus().GetCode().String(), "STATUS_CODE_"),
					traceID:    hex.EncodeToString(s.GetTraceId()),
					id:         hex.EncodeToString(s.GetSpanId()),
					parentID:   hex.EncodeToString(s.GetParentSpanId()),
					durationMs: float64(s.GetEndTimeUnixNano()-s.GetStartTimeUnixNano()) / 1e6,
					attributes: attributeMap(s.GetAttributes()),
				})
			}
		}
	}
}

func (r *receiver) addMetrics(export *collectormetrics.ExportMetricsServiceRequest) {
	for _, resource := range export.GetResourceMetrics() {
		for _, scope := range resource.GetScopeMetrics() {
			for _, metric := range scope.GetMetrics() {
				r.metrics[metric.GetName()] = true
			}
		}
	}
}

func (r *receiver) addLogs(export *collectorlogs.ExportLogsServiceRequest) {
	for _, resource := range export.GetResourceLogs() {
		for _, scope := range resource.GetScopeLogs() {
			for _, record := range scope.GetLogRecords() {
				r.logs = append(r.logs, logRecord{valueText(record.GetBody()), hex.EncodeToString(record.GetTraceId())})
			}
		}
	}
}

func (r *receiver) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	body, err := io.ReadAll(request.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	var reply proto.Message
	switch request.URL.Path {
	case "/v1/traces":
		var export collectortrace.ExportTraceServiceRequest
		if err = proto.Unmarshal(body, &export); err == nil {
			r.addSpans(&export)
		}
		reply = &collectortrace.ExportTraceServiceResponse{}
	case "/v1/metrics":
		var export collectormetrics.ExportMetricsServiceRequest
		if err = proto.Unmarshal(body, &export); err == nil {
			r.addMetrics(&export)
		}
		reply = &collectormetrics.ExportMetricsServiceResponse{}
	case "/v1/logs":
		var export collectorlogs.ExportLogsServiceRequest
		if err = proto.Unmarshal(body, &export); err == nil {
			r.addLogs(&export)
		}
		reply = &collectorlogs.ExportLogsServiceResponse{}
	default:
		http.NotFound(w, request)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	encoded, err := proto.Marshal(reply)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	_, _ = w.Write(encoded)
}

var term = regexp.MustCompile(`^(\S+)\s*(=|>)\s*"?(.*?)"?$`)

// matches reports whether a span satisfies every term of a query such as
// `service = accounts-service and atm.branch = "canada-water"`.
func matches(s span, query string) (bool, error) {
	for _, part := range strings.Split(query, " and ") {
		found := term.FindStringSubmatch(strings.TrimSpace(part))
		if found == nil {
			return false, fmt.Errorf("cannot parse %q", part)
		}
		key, operator, want := found[1], found[2], found[3]

		if key == "duration" {
			limit, err := strconv.ParseFloat(want, 64)
			if err != nil || operator != ">" {
				return false, fmt.Errorf("cannot parse %q", part)
			}
			if s.durationMs <= limit {
				return false, nil
			}
			continue
		}

		got, ok := s.attributes[key]
		switch key {
		case "name":
			got, ok = s.name, true
		case "service":
			got, ok = s.service, true
		case "status":
			got, ok = s.status, true
		}
		if !ok || got != want {
			return false, nil
		}
	}
	return true, nil
}

func (r *receiver) count(query string) (int, error) {
	total := 0
	for _, s := range r.spans {
		ok, err := matches(s, query)
		if err != nil {
			return 0, err
		}
		if ok {
			total++
		}
	}
	return total, nil
}

func (r *receiver) hasChild(child, parent string) bool {
	for _, c := range r.spans {
		for _, p := range r.spans {
			if c.name == child && p.name == parent && c.parentID == p.id && c.traceID == p.traceID {
				return true
			}
		}
	}
	return false
}

func (r *receiver) oneTrace(services []string) bool {
	seen := map[string]map[string]bool{}
	for _, s := range r.spans {
		if seen[s.traceID] == nil {
			seen[s.traceID] = map[string]bool{}
		}
		seen[s.traceID][s.service] = true
	}
	for _, held := range seen {
		if !slices.ContainsFunc(services, func(service string) bool { return !held[service] }) {
			return true
		}
	}
	return false
}

// verify runs one directive, and reports whether it holds.
func (r *receiver) verify(kind, arg, stdout string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	switch kind {
	case "check":
		total, err := r.count(arg)
		return total > 0, err
	case "check-count":
		want, query, _ := strings.Cut(arg, " ")
		expected, err := strconv.Atoi(want)
		if err != nil {
			return false, err
		}
		total, err := r.count(query)
		return total == expected, err
	case "check-child":
		child, parent, ok := strings.Cut(arg, " of ")
		if !ok {
			return false, fmt.Errorf("cannot parse %q", arg)
		}
		return r.hasChild(child, parent), nil
	case "check-one-trace":
		return r.oneTrace(strings.Split(arg, ", ")), nil
	case "check-metric":
		return r.metrics[arg], nil
	case "check-log":
		return slices.ContainsFunc(r.logs, func(l logRecord) bool {
			return strings.Contains(l.body, arg) && strings.Trim(l.traceID, "0") != ""
		}), nil
	case "check-stdout":
		return strings.Contains(stdout, arg), nil
	}
	return false, fmt.Errorf("unknown directive %q", kind)
}

type directive struct{ kind, arg string }

var directiveLine = regexp.MustCompile(`^// (check(?:-[a-z-]+)?): (.+)$`)

func directives(path string) ([]directive, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []directive
	for line := range strings.Lines(string(source)) {
		if found := directiveLine.FindStringSubmatch(strings.TrimRight(line, "\n")); found != nil {
			out = append(out, directive{found[1], found[2]})
		}
	}
	return out, nil
}

// run builds and runs one program, and returns what it printed.
func run(pkg, binaries, endpoint string) (string, error) {
	binary := filepath.Join(binaries, strings.ReplaceAll(pkg, "/", "-"))
	if output, err := exec.Command("go", "build", "-o", binary, modulePath+pkg).CombinedOutput(); err != nil {
		return "", fmt.Errorf("build: %w\n%s", err, output)
	}
	defer func() { _ = os.Remove(binary) }()

	var stdout bytes.Buffer
	program := exec.Command(binary)
	program.Env = append(os.Environ(), "OTEL_EXPORTER_OTLP_ENDPOINT="+endpoint)
	program.Stdout = &stdout
	program.Stderr = &stdout
	if err := program.Run(); err != nil {
		return stdout.String(), fmt.Errorf("run: %w\n%s", err, stdout.String())
	}
	return stdout.String(), nil
}

// checkPart runs one part's exercise and solution, and returns how many checks
// failed.
func checkPart(part, binaries, endpoint string, received *receiver) int {
	name := filepath.Base(part)
	failed := 0

	received.reset()
	if _, err := run(part+"/exercise", binaries, endpoint); err != nil {
		fmt.Printf("FAIL %s exercise: %v\n", name, err)
		failed++
	} else {
		fmt.Printf("ok   %s exercise\n", name)
	}

	received.reset()
	stdout, err := run(part+"/solution", binaries, endpoint)
	if err != nil {
		fmt.Printf("FAIL %s solution: %v\n", name, err)
		return failed + 1
	}

	wanted, err := directives(part + "/solution/main.go")
	if err != nil || len(wanted) == 0 {
		fmt.Printf("FAIL %s solution: no directives: %v\n", name, err)
		return failed + 1
	}

	for _, want := range wanted {
		// Exports finish before a process exits; the wait covers the receiver's
		// own handlers.
		var ok bool
		var err error
		for attempt := 0; attempt < 20 && !ok && err == nil; attempt++ {
			ok, err = received.verify(want.kind, want.arg, stdout)
			if !ok {
				time.Sleep(100 * time.Millisecond)
			}
		}
		if !ok || err != nil {
			fmt.Printf("FAIL %s %s: %s %v\n", name, want.kind, want.arg, err)
			failed++
			continue
		}
		fmt.Printf("ok   %s %s: %s\n", name, want.kind, want.arg)
	}
	return failed
}

func main() { os.Exit(checkAll()) }

func checkAll() int {
	parts, err := filepath.Glob("workshop/[0-9][0-9]-*")
	if err != nil || len(parts) == 0 {
		fmt.Fprintln(os.Stderr, "run this from the repository root")
		return 1
	}
	if len(os.Args) > 1 {
		parts = slices.DeleteFunc(parts, func(part string) bool {
			return !strings.HasPrefix(filepath.Base(part), os.Args[1])
		})
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	received := &receiver{}
	go func() { _ = http.Serve(listener, received) }()
	endpoint := "http://" + listener.Addr().String()

	binaries, err := os.MkdirTemp("", "workshop-check")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = os.RemoveAll(binaries) }()

	failed := 0
	for _, part := range parts {
		failed += checkPart(part, binaries, endpoint, received)
	}

	if failed > 0 {
		fmt.Printf("\n%d failed\n", failed)
		return 1
	}
	fmt.Println("\nall parts ok")
	return 0
}
