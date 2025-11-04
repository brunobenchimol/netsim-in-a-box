package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/ossrs/go-oryx-lib/errors"
	ohttp "github.com/ossrs/go-oryx-lib/http"
	"github.com/ossrs/go-oryx-lib/logger"
)

// PreflightCheck stores the result of a single prerequisite check.
type PreflightCheck struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
	Status   bool   `json:"status"`
	Message  string `json:"message"`
}

var isDarwin bool
var hasIFB bool

const version = "1.1.0"

func init() {
	ctx := logger.WithContext(context.Background())
	isDarwin = runtime.GOOS == "darwin"
	logger.Tf(ctx, "OS darwin=%v", isDarwin)
}

func main() {
	ctx := logger.WithContext(context.Background())
	if err := doMain(ctx); err != nil {
		logger.Ef(ctx, "CRITICAL FAILURE: %v", err)

		fmt.Println("-------------------------------------------------")
		fmt.Printf("ERROR: Application failed to start.\n")
		fmt.Printf("REASON: %v\n", err)
		fmt.Println("-------------------------------------------------")

		os.Exit(-1)
		return
	}
}

func doMain(ctx context.Context) error {
	logger.Tf(ctx, "WebUI for TC(Linux Traffic Control) https://lartc.org/howto/index.html")

	// Set default API_LISTEN port if not provided.
	// This ensures that os.Getenv("API_LISTEN") works correctly
	// in other parts of the application (like tc.go).
	if os.Getenv("API_LISTEN") == "" {
		os.Setenv("API_LISTEN", "2023")
	}

	// Run system preflight checks.
	logger.Tf(ctx, "Running Preflight Checks...")
	checks, allOk := runPreflightChecks(ctx)

	var criticalFailures []string
	for _, check := range checks {
		statusMsg := "FAILED"
		if check.Status {
			statusMsg = "OK"
		}

		logFunc := logger.Tf // 'Trace' log by default
		if !check.Status && check.Required {
			logFunc = logger.Ef // 'Error' log for critical failures
			criticalFailures = append(criticalFailures, fmt.Sprintf("%s: %s", check.Name, check.Message))
		}

		logFunc(ctx, "  - Check: %-20s Status: %-7s Message: %s", check.Name, statusMsg, check.Message)
	}

	if !allOk {
		return errors.Errorf("Preflight checks failed: %s", strings.Join(criticalFailures, "; "))
	}
	logger.Tf(ctx, "Preflight checks passed successfully.")

	/*
		if _, err := os.Stat(".env"); err == nil {
			if err := godotenv.Load(".env"); err != nil {
				panic(err)
			}
		}
		// Set default values for env.
		setDefaultEnv := func(k, v string) {
			if os.Getenv(k) == "" {
				os.Setenv(k, v)
			}
		}
		setDefaultEnv("NODE_ENV", "production")
		setDefaultEnv("API_LISTEN", "2023")
		setDefaultEnv("UI_HOST", "127.0.0.1")
		setDefaultEnv("UI_PORT", "3000")
		setDefaultEnv("IFACE_FILTER_IPV4", "true")
		setDefaultEnv("IFACE_FILTER_IPV6", "true")
		setDefaultEnv("PROXY_ID0_ENABLED", "on")
		setDefaultEnv("PROXY_ID0_MOUNT", "/restarter/")
		setDefaultEnv("PROXY_ID0_BACKEND", "http://127.0.0.1:2024")
		logger.Tf(ctx, "Load .env as NODE_ENV=%v, API_LISTEN=%v, UI_PORT(reactjs)=%v, IFACE_FILTER_IPV4=%v, IFACE_FILTER_IPV6=%v, PROXY0=%v/%v/%v",
			os.Getenv("NODE_ENV"), os.Getenv("API_LISTEN"), os.Getenv("UI_PORT"), os.Getenv("IFACE_FILTER_IPV4"),
			os.Getenv("IFACE_FILTER_IPV6"), os.Getenv("PROXY_ID0_ENABLED"), os.Getenv("PROXY_ID0_MOUNT"),
			os.Getenv("PROXY_ID0_BACKEND"),
		)

		addr := fmt.Sprintf("%v", os.Getenv("API_LISTEN"))
		if !strings.Contains(addr, ":") {
			addr = fmt.Sprintf(":%v", addr)
		}
		logger.Tf(ctx, "Listen at %v", addr)
	*/

	addr := os.Getenv("API_LISTEN")
	if !strings.Contains(addr, ":") {
		addr = fmt.Sprintf(":%v", addr)
	}
	logger.Tf(ctx, "Listen at %v", addr)

	ep := "/tc/api/v1/versions"
	logger.Tf(ctx, "Handle %v", ep)
	http.HandleFunc(ep, func(w http.ResponseWriter, r *http.Request) {
		ohttp.WriteVersion(w, r, version)
	})

	ep = "/tc/api/v1/scan"
	logger.Tf(ctx, "Handle %v", ep)
	http.HandleFunc(ep, func(w http.ResponseWriter, r *http.Request) {
		if err := ScanByTcpdump(ctx, w, r); err != nil {
			ohttp.WriteError(ctx, w, r, err)
		}
	})

	ep = "/tc/api/v1/config/query"
	logger.Tf(ctx, "Handle %v", ep)
	http.HandleFunc(ep, func(w http.ResponseWriter, r *http.Request) {
		if err := TcQuery(logger.WithContext(ctx), w, r); err != nil {
			ohttp.WriteError(ctx, w, r, err)
		}
	})

	ep = "/tc/api/v1/config/reset"
	logger.Tf(ctx, "Handle %v", ep)
	http.HandleFunc(ep, func(w http.ResponseWriter, r *http.Request) {
		if err := TcReset(logger.WithContext(ctx), w, r); err != nil {
			ohttp.WriteError(ctx, w, r, err)
		}
	})

	ep = "/tc/api/v1/config/setup"
	logger.Tf(ctx, "Handle %v", ep)
	http.HandleFunc(ep, func(w http.ResponseWriter, r *http.Request) {
		if err := TcSetup(logger.WithContext(ctx), w, r); err != nil {
			ohttp.WriteError(ctx, w, r, err)
		}
	})

	ep = "/tc/api/v1/config/setup2"
	logger.Tf(ctx, "Handle %v", ep)
	http.HandleFunc(ep, func(w http.ResponseWriter, r *http.Request) {
		if err := TcSetup2(logger.WithContext(ctx), w, r); err != nil {
			ohttp.WriteError(ctx, w, r, err)
		}
	})

	ep = "/tc/api/v1/config/raw"
	logger.Tf(ctx, "Handle %v", ep)
	http.HandleFunc(ep, func(w http.ResponseWriter, r *http.Request) {
		if err := TcRaw(logger.WithContext(ctx), w, r); err != nil {
			ohttp.WriteCplxError(ctx, w, r, ohttp.SystemError(100), err.Error())
		}
	})

	ep = "/tc/api/v1/init"
	logger.Tf(ctx, "Handle %v", ep)
	http.HandleFunc(ep, func(w http.ResponseWriter, r *http.Request) {
		if err := TcInit(logger.WithContext(ctx), w, r); err != nil {
			ohttp.WriteError(ctx, w, r, err)
		}
	})

	/*
		// --- Proxy Handlers (from .env) ---
		for i := 0; i < 8; i++ {
			enabledKey := fmt.Sprintf("PROXY_ID%v_ENABLED", i)
			mountKey := fmt.Sprintf("PROXY_ID%v_MOUNT", i)
			backendKey := fmt.Sprintf("PROXY_ID%v_BACKEND", i)
			if os.Getenv(enabledKey) != "on" {
				if os.Getenv(mountKey) != "" {
					logger.Tf(ctx, "Proxy to %v is disabled", os.Getenv(mountKey))
				}
			} else {
				if pattern := os.Getenv(mountKey); pattern != "" {
					backend := os.Getenv(backendKey)
					target, err := url.Parse(backend)
					if err != nil {
						return errors.Wrapf(err, "parse backend %v for #%v pattern %v", backend, i, pattern)
					}

					logger.Tf(ctx, "Proxy #%v %v to %v", i, pattern, backend)
					rp := httputil.NewSingleHostReverseProxy(target)
					http.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
						rp.ServeHTTP(w, r)
					})
				}
			}
		}
	*/

	// --- Static UI Server ---
	// Serve our new frontend from "./frontend"
	// This path will be relative to the app's working directory in the Docker
	uiStaticDir := "./frontend"
	logger.Tf(ctx, "Serving static UI from %s", uiStaticDir)

	// Create a file server for our static assets.
	fs := http.FileServer(http.Dir(uiStaticDir))

	// Handle all non-API routes by serving the static files
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Prevent API calls from being handled by the file server
		// (e.g., /tc/api/v1/init)
		if strings.HasPrefix(r.URL.Path, "/tc/api/") || strings.HasPrefix(r.URL.Path, "/restarter/") {
			http.NotFound(w, r)
			return
		}

		// Serve the static file (e.g., /index.html, /app.js)
		fs.ServeHTTP(w, r)
	})
	// --- End of Static UI Server ---

	// --- Start Server ---
	if err := http.ListenAndServe(addr, nil); err != nil {
		return errors.Wrapf(err, "listen")
	}

	return nil
}

// runPreflightChecks executes a series of checks to ensure the
// environment has all necessary dependencies.
func runPreflightChecks(ctx context.Context) (checks []*PreflightCheck, ok bool) {
	// Helper function to check if a binary exists and is executable.
	checkBinary := func(name string, args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", err
		}
		// Get the first line of output to use as "version"
		s := bufio.NewScanner(bytes.NewReader(out))
		if s.Scan() {
			return s.Text(), nil
		}
		return "OK", nil
	}

	// === Check 1: Root Permission ===
	{
		check := &PreflightCheck{Name: "Root Permission", Required: true}
		cmd := exec.CommandContext(ctx, "id", "-u")
		if out, err := cmd.Output(); err != nil {
			check.Status = false
			check.Message = fmt.Sprintf("Failed to check UID: %v", err)
		} else if uid := strings.TrimSpace(string(out)); uid != "0" {
			check.Status = false
			check.Message = fmt.Sprintf("Must run as root (uid=0), but was (uid=%s)", uid)
		} else {
			check.Status = true
			check.Message = "OK (uid=0)"
		}
		checks = append(checks, check)
	}

	// === Check 2: tcpdump ===
	{
		check := &PreflightCheck{Name: "tcpdump", Required: true}
		if version, err := checkBinary("tcpdump", "--version"); err != nil {
			check.Status = false
			check.Message = "Binary 'tcpdump' not found. (Install with 'apt-get install tcpdump')"
		} else {
			check.Status = true
			check.Message = fmt.Sprintf("OK (%s)", version)
		}
		checks = append(checks, check)
	}

	// === Check 3: tc (iproute2) ===
	{
		check := &PreflightCheck{Name: "tc (iproute2)", Required: true}
		// Use "tc -V" to get version
		if version, err := checkBinary("tc", "-V"); err != nil {
			check.Status = false
			check.Message = "Binary 'tc' not found. (Install with 'apt-get install iproute2')"
		} else {
			check.Status = true
			check.Message = fmt.Sprintf("OK (%s)", version)
		}
		checks = append(checks, check)
	}

	// === Check 4: tcset (tcconfig) ===
	{
		check := &PreflightCheck{Name: "tcset (tcconfig)", Required: true}
		if version, err := checkBinary("tcset", "--version"); err != nil {
			check.Status = false
			check.Message = "Binary 'tcset' not found. (Install with 'pip install tcconfig')"
		} else {
			check.Status = true
			check.Message = fmt.Sprintf("OK (%s)", version)
		}
		checks = append(checks, check)
	}

	// === Check 5: tcdel (tcconfig) ===
	{
		check := &PreflightCheck{Name: "tcdel (tcconfig)", Required: true}
		if version, err := checkBinary("tcdel", "--version"); err != nil {
			check.Status = false
			check.Message = "Binary 'tcdel' not found. (Install with 'pip install tcconfig')"
		} else {
			check.Status = true
			check.Message = fmt.Sprintf("OK (%s)", version)
		}
		checks = append(checks, check)
	}

	// === Check 6: tcshow (tcconfig) ===
	{
		check := &PreflightCheck{Name: "tcshow (tcconfig)", Required: true}
		if version, err := checkBinary("tcshow", "--version"); err != nil {
			check.Status = false
			check.Message = "Binary 'tcshow' not found. (Install with 'pip install tcconfig')"
		} else {
			check.Status = true
			check.Message = fmt.Sprintf("OK (%s)", version)
		}
		checks = append(checks, check)
	}

	// === Check 7: Kernel Module 'ifb' ===
	{
		check := &PreflightCheck{Name: "Kernel Module 'ifb'", Required: false}
		// Check if 'ifb' is listed in /proc/modules
		cmd := exec.CommandContext(ctx, "grep", "^ifb", "/proc/modules")
		if err := cmd.Run(); err != nil {
			check.Status = false
			check.Message = "Module 'ifb' not loaded. Ingress (incoming) traffic shaping will be disabled."
		} else {
			check.Status = true
			check.Message = "OK (Module 'ifb' is loaded)"
			hasIFB = true
		}
		checks = append(checks, check)
	}

	// === Check 8: Kernel Module 'sch_htb' ===
	{
		// HTB (Hierarchical Token Bucket) is essential for rate limiting (bandwidth).
		check := &PreflightCheck{Name: "Kernel Module 'sch_htb'", Required: true}
		cmd := exec.CommandContext(ctx, "grep", "^sch_htb", "/proc/modules")
		if err := cmd.Run(); err != nil {
			check.Status = false
			check.Message = "Module 'sch_htb' not loaded. This is *required* for rate limiting (bandwidth)."
		} else {
			check.Status = true
			check.Message = "OK (Module 'sch_htb' is loaded)"
		}
		checks = append(checks, check)
	}

	// === Check 9: Kernel Module 'sch_netem' ===
	{
		// NetEm (Network Emulator) is essential for loss and delay.
		check := &PreflightCheck{Name: "Kernel Module 'sch_netem'", Required: true}
		cmd := exec.CommandContext(ctx, "grep", "^sch_netem", "/proc/modules")
		if err := cmd.Run(); err != nil {
			check.Status = false
			check.Message = "Module 'sch_netem' not loaded. This is *required* for delay and loss."
		} else {
			check.Status = true
			check.Message = "OK (Module 'sch_netem' is loaded)"
		}
		checks = append(checks, check)
	}

	// Evaluate the final result
	ok = true
	for _, check := range checks {
		if check.Required && !check.Status {
			ok = false
		}
	}

	return checks, ok
}
