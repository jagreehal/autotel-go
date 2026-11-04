// Package harness starts and stops the two service processes, and holds the HTTP
// plumbing they share.
//
// Nothing here teaches observability. It makes a three-process example runnable
// with one command, for parts 11 and 12 and both services.
package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/jagreehal/autotel-go/v2/workshop/bank"
)

const servicesPackage = "github.com/jagreehal/autotel-go/v2/workshop/11-one-trace-three-services/"

var (
	children    []*exec.Cmd
	binariesDir string
)

// requireFreePort refuses to start on a port something else holds. Without it, a
// service left running by an earlier run answers the health check, the new one
// dies silently, and you debug the code you changed while talking to the version
// you replaced.
func requireFreePort(port int) error {
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return fmt.Errorf("port %d is already in use. A service from an earlier run is probably "+
			"still up: kill it with `lsof -ti tcp:%d | xargs kill`", port, port)
	}
	return listener.Close()
}

func waitForPort(port int) error {
	for range 100 {
		response, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/ping", port))
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("service on :%d never came up", port)
}

// StartServices builds the two services in directory ("services" or
// "services-propagated") so they match the source, then starts them. They
// inherit this process's environment, so with OTEL_EXPORTER_OTLP_ENDPOINT set
// they export to the same devtools.
func StartServices(directory string, authPort, accountsPort int) error {
	if err := errors.Join(requireFreePort(authPort), requireFreePort(accountsPort)); err != nil {
		return err
	}

	err := startBoth(directory, authPort, accountsPort)
	if err == nil {
		err = errors.Join(waitForPort(authPort), waitForPort(accountsPort))
	}
	if err != nil {
		// Leave nothing running: a survivor would hold its port against the next run.
		StopServices(authPort, accountsPort)
	}
	return err
}

func startBoth(directory string, authPort, accountsPort int) error {
	binaries, err := os.MkdirTemp("", "workshop-services")
	if err != nil {
		return err
	}
	binariesDir = binaries

	for name, port := range map[string]int{"auth": authPort, "accounts": accountsPort} {
		binary := filepath.Join(binaries, name)
		build := exec.Command("go", "build", "-o", binary, servicesPackage+directory+"/"+name)
		build.Stderr = os.Stderr
		if err := build.Run(); err != nil {
			return fmt.Errorf("the services in %s do not build: %w", directory, err)
		}

		child := exec.Command(binary, fmt.Sprint(port))
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			return err
		}
		children = append(children, child)
	}
	return nil
}

// StopServices asks each service to flush its spans and exit, rather than
// killing it.
func StopServices(ports ...int) {
	for _, port := range ports {
		if response, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/shutdown", port)); err == nil {
			_ = response.Body.Close()
		}
	}
	for _, child := range children {
		done := make(chan error, 1)
		go func() { done <- child.Wait() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = child.Process.Kill()
			<-done
		}
	}
	children = nil
	_ = os.RemoveAll(binariesDir)
}

// CardReply is what POST /card answers with.
type CardReply struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// NewRequest builds a request with a JSON body, or none when body is nil. The URL
// is the program's own and the body marshals, so a failure is a bug.
func NewRequest(ctx context.Context, method, url string, body any) *http.Request {
	var payload bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&payload).Encode(body); err != nil {
			panic(err)
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, url, &payload)
	if err != nil {
		panic(err)
	}
	request.Header.Set("Content-Type", "application/json")
	return request
}

// Send makes the request and decodes the reply into out, when out isn't nil. A
// non-2xx reply becomes an error from the { code, message } the service wrote.
func Send(request *http.Request, out any) error {
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode >= 300 {
		var refusal struct{ Code, Message string }
		_ = json.NewDecoder(response.Body).Decode(&refusal)
		return fmt.Errorf("%s: %s", refusal.Code, refusal.Message)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(response.Body).Decode(out)
}

// Reply writes body as JSON, or a bank refusal as a 400 with { code, message }.
func Reply(w http.ResponseWriter, body any, err error) {
	w.Header().Set("Content-Type", "application/json")
	if err == nil {
		_ = json.NewEncoder(w).Encode(body)
		return
	}

	status, code := http.StatusInternalServerError, "UNEXPECTED"
	var refusal *bank.Error
	if errors.As(err, &refusal) {
		status, code = http.StatusBadRequest, string(refusal.Code)
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": err.Error()})
}

// Serve adds /ping and /shutdown to mux and runs it until /shutdown is called.
func Serve(port string, mux *http.ServeMux, wrap func(http.Handler) http.Handler) error {
	stop := make(chan struct{})
	mux.HandleFunc("GET /ping", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("GET /shutdown", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("bye"))
		close(stop)
	})

	server := &http.Server{Addr: "127.0.0.1:" + port, Handler: wrap(mux), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-stop
		_ = server.Shutdown(context.Background())
	}()

	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
