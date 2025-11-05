package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

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

const version = "2.0.0"

func init() {
	ctx := logger.WithContext(context.Background())
	isDarwin = runtime.GOOS == "darwin"
	logger.Tf(ctx, "OS darwin=%v", isDarwin)
}

func main() {
	// Create a context that is canceled on interrupt signals
	ctx, cancel := context.WithCancel(logger.WithContext(context.Background()))

	// Setup the signal listener
	setupGracefulShutdown(ctx, cancel)

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

	if os.Getenv("DEFAULT_GATEWAY_MODE") == "true" {
		if err := enableGatewayMode(ctx); err != nil {
			return errors.Wrapf(err, "Failed to enable Default Gateway Mode")
		}
	} else {
		logger.Tf(ctx, "DEFAULT_GATEWAY_MODE=false. Skipping gateway setup.")
	}

	addr := os.Getenv("API_LISTEN")
	if !strings.Contains(addr, ":") {
		addr = fmt.Sprintf(":%v", addr)
	}
	logger.Tf(ctx, "Listen at %v", addr)

	// --- V1 API Handlers ---
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

	// --- V2 API Handlers ---
	ep = "/tc/api/v2/init"
	logger.Tf(ctx, "Handle %v", ep)
	http.HandleFunc(ep, func(w http.ResponseWriter, r *http.Request) {
		// V2 init just calls V1 init, as the logic is identical.
		if err := TcInit(logger.WithContext(ctx), w, r); err != nil {
			ohttp.WriteError(ctx, w, r, err)
		}
	})

	ep = "/tc/api/v2/config/setup"
	logger.Tf(ctx, "Handle %v", ep)
	http.HandleFunc(ep, func(w http.ResponseWriter, r *http.Request) {
		if err := TcSetupV2(logger.WithContext(ctx), w, r); err != nil {
			// V2 returns the full error to the UI
			ohttp.WriteCplxError(ctx, w, r, ohttp.SystemError(100), err.Error())
		}
	})

	ep = "/tc/api/v2/config/reset"
	logger.Tf(ctx, "Handle %v", ep)
	http.HandleFunc(ep, func(w http.ResponseWriter, r *http.Request) {
		if err := TcResetV2(logger.WithContext(ctx), w, r); err != nil {
			// V2 returns the full error to the UI
			ohttp.WriteCplxError(ctx, w, r, ohttp.SystemError(100), err.Error())
		}
	})

	// --- Static UI Server ---
	// V1 (Legacy UI)
	// Will serve the V1 UI from "./frontend-v1" at the /old/ path
	uiStaticDirV1 := "./frontend-v1"
	logger.Tf(ctx, "Serving V1 static UI from %s at /old/", uiStaticDirV1)
	fsV1 := http.FileServer(http.Dir(uiStaticDirV1))
	// Register the handler for /old/. StripPrefix removes /old/ from the request
	// so the FileServer looks for /index.html instead of /old/index.html
	http.Handle("/old/", http.StripPrefix("/old/", fsV1))

	// V2 (New UI)
	// Will serve the new V2 UI from "./frontend" at the / path
	uiStaticDirV2 := "./frontend"
	logger.Tf(ctx, "Serving V2 static UI from %s at /", uiStaticDirV2)
	fsV2 := http.FileServer(http.Dir(uiStaticDirV2))

	// The main handler (/) now serves V2
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Prevent APIs and V1 from being handled by the V2 file server
		if strings.HasPrefix(r.URL.Path, "/tc/api/") ||
			strings.HasPrefix(r.URL.Path, "/restarter/") ||
			strings.HasPrefix(r.URL.Path, "/old/") {
			http.NotFound(w, r)
			return
		}

		// Serve the V2 file (e.g., /index.html, /app.js)
		fsV2.ServeHTTP(w, r)
	})
	// --- End of Static UI Server ---

	// --- Start Server ---
	// We run http.ListenAndServe in a goroutine so it doesn't block
	// the graceful shutdown listener.
	httpServer := &http.Server{Addr: addr}

	go func() {
		logger.Tf(ctx, "HTTP server starting at %v", addr)
		if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
			logger.Ef(ctx, "HTTP server ListenAndServe error: %v", err)
		}
	}()

	// Wait for context cancellation (from graceful shutdown)
	<-ctx.Done()

	// Shutdown the HTTP server
	logger.Tf(ctx, "HTTP server shutting down...")
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Ef(ctx, "HTTP server graceful shutdown failed: %v", err)
	}

	// Finally, run the cleanup
	logger.Tf(ctx, "Running graceful cleanup of all TC rules...")
	cleanupAllInterfaces(context.Background()) // Use a new background context
	logger.Tf(ctx, "Cleanup complete. Exiting.")

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

// runGatewayCommand is a helper to execute system commands for Gateway Mode
func runGatewayCommand(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	logger.Tf(ctx, "GATEWAY_MODE: Running command: %s", cmd.String())

	if output, err := cmd.CombinedOutput(); err != nil {
		logger.Ef(ctx, "GATEWAY_MODE: Error running command: %v\nOutput: %s", err, string(output))
		return errors.Wrapf(err, "command failed: %s %s", name, strings.Join(args, " "))
	} else {
		logger.Tf(ctx, "GATEWAY_MODE: Command successful: %s", cmd.String())
	}
	return nil
}

// enableGatewayMode configures the host (via --net=host) to act as a router/gateway
func enableGatewayMode(ctx context.Context) error {
	logger.Tf(ctx, "GATEWAY_MODE: Enabling Default Gateway Mode...")

	// --- Step 1: Enable IP Forwarding ---
	if err := runGatewayCommand(ctx, "sysctl", "-w", "net.ipv4.ip_forward=1"); err != nil {
		return errors.Wrapf(err, "Failed to set net.ipv4.ip_forward")
	}

	// --- Step 2: Detect WAN (default) Interface ---
	cmd := exec.CommandContext(ctx, "ip", "route", "show", "default")
	output, err := cmd.Output()
	if err != nil {
		return errors.Wrapf(err, "Failed to get default route. Cannot determine WAN interface.")
	}

	wanIface := ""
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "default") {
			parts := strings.Fields(line)
			for i, part := range parts {
				if part == "dev" && i+1 < len(parts) {
					wanIface = parts[i+1]
					break
				}
			}
		}
		if wanIface != "" {
			break
		}
	}

	if wanIface == "" {
		return errors.Errorf("Could not parse default route to find 'dev' interface from: %s", string(output))
	}
	logger.Tf(ctx, "GATEWAY_MODE: Detected WAN interface: %s", wanIface)

	// --- Step 3: Apply Permissive iptables Rules ---
	// Rule 1 (NAT): Allow all forwarded traffic to NAT out the WAN interface
	if err := runGatewayCommand(ctx, "iptables", "-t", "nat", "-A", "POSTROUTING", "-o", wanIface, "-j", "MASQUERADE"); err != nil {
		return errors.Wrapf(err, "Failed to apply NAT/MASQUERADE rule")
	}

	// Rule 2 (Forwarding): Allow traffic to be forwarded TO the WAN interface
	if err := runGatewayCommand(ctx, "iptables", "-A", "FORWARD", "-o", wanIface, "-j", "ACCEPT"); err != nil {
		return errors.Wrapf(err, "Failed to apply FORWARD (out) rule")
	}

	// Rule 3 (Forwarding): Allow return traffic from established connections
	if err := runGatewayCommand(ctx, "iptables", "-A", "FORWARD", "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT"); err != nil {
		return errors.Wrapf(err, "Failed to apply FORWARD (state) rule")
	}

	// --- Step 4: (Opt-in) Reconfigure Host Firewall ---
	if os.Getenv("RECONFIGURE_FIREWALL") == "true" {
		logger.Tf(ctx, "GATEWAY_MODE: RECONFIGURE_FIREWALL=true detected.")
		// Check if ufw command exists
		if _, err := exec.LookPath("ufw"); err == nil {
			logger.Tf(ctx, "GATEWAY_MODE: ufw found, attempting to disable it...")
			if err := runGatewayCommand(ctx, "ufw", "disable"); err != nil {
				return errors.Wrapf(err, "Failed to disable ufw. Please do this manually.")
			}
			logger.Tf(ctx, "GATEWAY_MODE: ufw disabled successfully.")
		} else {
			logger.Tf(ctx, "GATEWAY_MODE: ufw command not found, skipping host firewall reconfiguration.")
		}
	} else {
		logger.Tf(ctx, "GATEWAY_MODE: RECONFIGURE_FIREWALL not set. Host firewall (ufw) was NOT touched.")
		logger.Wf(ctx, "GATEWAY_MODE: WARNING: If ufw is active, it may block forwarded traffic. Set RECONFIGURE_FIREWALL=true or configure ufw manually.")
	}

	logger.Tf(ctx, "GATEWAY_MODE: Successfully enabled. Host is now a gateway.")
	return nil
}

// --- Graceful Shutdown Functions ---

// setupGracefulShutdown listens for OS signals and initiates a cleanup.
func setupGracefulShutdown(ctx context.Context, cancel context.CancelFunc) {
	sigChan := make(chan os.Signal, 1)
	// Notify on INTERRUPT (Ctrl+C) or TERMINATE
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		// Wait for a signal
		sig := <-sigChan
		logger.Wf(ctx, "Received signal: %v. Starting graceful shutdown...", sig)

		// Cancel the main context to stop the HTTP server
		cancel()
	}()
}

// cleanupAllInterfaces runs 'tcdel --all' on every active interface.
func cleanupAllInterfaces(ctx context.Context) {
	if isDarwin {
		return // No TC on Darwin
	}

	logger.Tf(ctx, "Cleaning up all TC rules from all interfaces...")

	// We use a new context, as the main one might be canceled
	ifaces, err := queryIPNetInterfaces(nil)
	if err != nil {
		logger.Ef(ctx, "Cleanup failed: Could not query interfaces: %v", err)
		return
	}

	for _, iface := range ifaces {
		logger.Tf(ctx, "Cleaning up interface: %s", iface.Name)
		args := []string{"--all", iface.Name}
		if b, err := exec.CommandContext(ctx, "tcdel", args...).CombinedOutput(); err != nil {
			// Log error but continue
			logger.Ef(ctx, "Cleanup failed for %s: %v, %s", iface.Name, err, string(b))
		}
	}
	logger.Tf(ctx, "TC cleanup finished.")
}
