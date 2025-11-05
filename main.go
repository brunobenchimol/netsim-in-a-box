package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json" // For JSON responses
	"fmt"
	"log" // Replaces oryx-logger
	"net/http"
	"os"
	"os/exec"
	"os/signal" // Import for graceful shutdown
	"runtime"
	"strings"
	"syscall" // Import for graceful shutdown
	"time"    // Import for shutdown timeout

	"github.com/go-chi/chi/v5" // Chi router
	"github.com/go-chi/chi/v5/middleware"
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

const version = "3.0.0" // V3

func init() {
	isDarwin = runtime.GOOS == "darwin"
	log.Printf("[INFO] OS darwin=%v", isDarwin)
}

func main() {
	// --- Graceful Shutdown Setup ---
	// Create a context that is canceled on interrupt signals
	ctx, cancel := context.WithCancel(context.Background())

	// Setup the signal listener
	setupGracefulShutdown(cancel)

	if err := doMain(ctx); err != nil {
		// If doMain fails on startup, we still want to exit.
		log.Printf("[CRITICAL] CRITICAL FAILURE: %v", err)

		fmt.Println("-------------------------------------------------")
		fmt.Printf("ERROR: Application failed to start.\n")
		fmt.Printf("REASON: %v\n", err)
		fmt.Println("-------------------------------------------------")

		os.Exit(1)
		return
	}
}

func doMain(ctx context.Context) error {
	log.Println("[INFO] WebUI for TC(Linux Traffic Control) https://lartc.org/howto/index.html")

	// Set default API_LISTEN port
	if os.Getenv("API_LISTEN") == "" {
		os.Setenv("API_LISTEN", "2023")
	}

	// Run system preflight checks.
	log.Println("[INFO] Running Preflight Checks...")
	checks, allOk := runPreflightChecks(ctx)

	var criticalFailures []string
	for _, check := range checks {
		statusMsg := "FAILED"
		if check.Status {
			statusMsg = "OK"
		}

		logFunc := log.Printf // Default to INFO
		if !check.Status && check.Required {
			logFunc = func(format string, v ...interface{}) { log.Printf("[ERROR] "+format, v...) }
			criticalFailures = append(criticalFailures, fmt.Sprintf("%s: %s", check.Name, check.Message))
		}
		logFunc("  - Check: %-20s Status: %-7s Message: %s", check.Name, statusMsg, check.Message)
	}

	if !allOk {
		return fmt.Errorf("preflight checks failed: %s", strings.Join(criticalFailures, "; "))
	}
	log.Println("[INFO] Preflight checks passed successfully.")

	// Enable Gateway Mode if requested
	if os.Getenv("DEFAULT_GATEWAY_MODE") == "true" {
		if err := enableGatewayMode(ctx); err != nil {
			return fmt.Errorf("failed to enable Default Gateway Mode: %w", err)
		}
	} else {
		log.Println("[INFO] DEFAULT_GATEWAY_MODE=false. Skipping gateway setup.")
	}

	addr := os.Getenv("API_LISTEN")
	if !strings.Contains(addr, ":") {
		addr = fmt.Sprintf(":%v", addr)
	}

	// --- Chi Router Setup ---
	r := chi.NewRouter()

	// Standard middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger) // Chi's built-in request logger
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	// --- API Routes ---
	r.Get("/tc/api/version", func(w http.ResponseWriter, r *http.Request) {
		respondWithJSON(w, http.StatusOK, map[string]string{"version": version})
	})
	// We keep the V2 paths for frontend compatibility
	r.Route("/tc/api/v2/config", func(r chi.Router) {
		r.Get("/init", handleTcInit)
		r.Get("/setup", handleTcSetupV2) // Use GET for simplicity with query params
		r.Get("/reset", handleTcResetV2) // Use GET for simplicity with query params
		// Add the raw diagnostic endpoint
		r.MethodFunc("GET", "/raw", handleTcRaw)
		r.MethodFunc("POST", "/raw", handleTcRaw)
	})

	// --- Static File Server ---
	// Serve the V3 frontend from "./frontend"
	uiStaticDirV3 := "./frontend"
	log.Printf("[INFO] Serving V3 static UI from %s at /", uiStaticDirV3)

	// Serve static files (e.g., app.js, production.css)
	fsV3 := http.StripPrefix("/", http.FileServer(http.Dir(uiStaticDirV3)))

	r.Get("/*", func(w http.ResponseWriter, r *http.Request) {
		// Check if the file exists
		f, err := os.Stat(uiStaticDirV3 + r.URL.Path)
		if os.IsNotExist(err) || f.IsDir() {
			// If file doesn't exist (or is a dir), serve index.html for SPA routing
			http.ServeFile(w, r, uiStaticDirV3+"/index.html")
			return
		}
		// Otherwise, serve the static file
		fsV3.ServeHTTP(w, r)
	})
	// --- End Static Server ---

	// --- Start Server ---
	httpServer := &http.Server{Addr: addr, Handler: r}

	go func() {
		log.Printf("[INFO] HTTP server starting at %v", addr)
		if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
			log.Printf("[CRITICAL] HTTP server ListenAndServe error: %v", err)
		}
	}()

	// Wait for context cancellation (from graceful shutdown)
	<-ctx.Done()

	// Shutdown the HTTP server
	log.Println("[INFO] HTTP server shutting down...")
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("[ERROR] HTTP server graceful shutdown failed: %v", err)
	}

	// Finally, run the cleanup
	log.Println("[INFO] Running graceful cleanup of all TC rules...")
	cleanupAllInterfaces(context.Background()) // Use a new background context
	log.Println("[INFO] Cleanup complete. Exiting.")

	return nil
}

// --- HTTP Response Helpers ---

// respondWithError sends a JSON error message
func respondWithError(w http.ResponseWriter, message string, code int) {
	log.Printf("[ERROR] API Error: %s", message)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"code":    code,
		"message": message,
	})
}

// respondWithJSON sends a JSON data response
func respondWithJSON(w http.ResponseWriter, code int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if payload != nil {
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			log.Printf("[ERROR] Failed to write JSON response: %v", err)
		}
	}
}

// --- Preflight, Gateway, and Shutdown functions ---
// (Ported from your main.go, replacing 'logger' with 'log')

func runPreflightChecks(ctx context.Context) (checks []*PreflightCheck, ok bool) {
	// Helper function to check if a binary exists and is executable.
	checkBinary := func(name string, args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", err
		}
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
	log.Printf("[INFO] GATEWAY_MODE: Running command: %s", cmd.String())

	if output, err := cmd.CombinedOutput(); err != nil {
		log.Printf("[ERROR] GATEWAY_MODE: Error running command: %v\nOutput: %s", err, string(output))
		return fmt.Errorf("command failed: %s %s: %w", name, strings.Join(args, " "), err)
	} else {
		log.Printf("[INFO] GATEWAY_MODE: Command successful: %s", cmd.String())
	}
	return nil
}

// enableGatewayMode configures the host (via --net=host) to act as a router/gateway
func enableGatewayMode(ctx context.Context) error {
	log.Println("[INFO] GATEWAY_MODE: Enabling Default Gateway Mode...")

	// --- Step 1: Enable IP Forwarding ---
	if err := runGatewayCommand(ctx, "sysctl", "-w", "net.ipv4.ip_forward=1"); err != nil {
		return fmt.Errorf("failed to set net.ipv4.ip_forward: %w", err)
	}

	// --- Step 2: Detect WAN (default) Interface ---
	cmd := exec.CommandContext(ctx, "ip", "route", "show", "default")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to get default route. Cannot determine WAN interface: %w", err)
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
		return fmt.Errorf("could not parse default route to find 'dev' interface from: %s", string(output))
	}
	log.Printf("[INFO] GATEWAY_MODE: Detected WAN interface: %s", wanIface)

	// --- Step 3: Apply Permissive iptables Rules ---
	if err := runGatewayCommand(ctx, "iptables", "-t", "nat", "-A", "POSTROUTING", "-o", wanIface, "-j", "MASQUERADE"); err != nil {
		return fmt.Errorf("failed to apply NAT/MASQUERADE rule: %w", err)
	}
	if err := runGatewayCommand(ctx, "iptables", "-A", "FORWARD", "-o", wanIface, "-j", "ACCEPT"); err != nil {
		return fmt.Errorf("failed to apply FORWARD (out) rule: %w", err)
	}
	if err := runGatewayCommand(ctx, "iptables", "-A", "FORWARD", "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT"); err != nil {
		return fmt.Errorf("failed to apply FORWARD (state) rule: %w", err)
	}

	// --- Step 4: (Opt-in) Reconfigure Host Firewall ---
	if os.Getenv("RECONFIGURE_FIREWALL") == "true" {
		log.Println("[INFO] GATEWAY_MODE: RECONFIGURE_FIREWALL=true detected.")
		if _, err := exec.LookPath("ufw"); err == nil {
			log.Println("[INFO] GATEWAY_MODE: ufw found, attempting to disable it...")
			if err := runGatewayCommand(ctx, "ufw", "disable"); err != nil {
				return fmt.Errorf("failed to disable ufw. Please do this manually: %w", err)
			}
			log.Println("[INFO] GATEWAY_MODE: ufw disabled successfully.")
		} else {
			log.Println("[INFO] GATEWAY_MODE: ufw command not found, skipping host firewall reconfiguration.")
		}
	} else {
		log.Println("[INFO] GATEWAY_MODE: RECONFIGURE_FIREWALL not set. Host firewall (ufw) was NOT touched.")
		log.Println("[WARN] GATEWAY_MODE: WARNING: If ufw is active, it may block forwarded traffic. Set RECONFIGURE_FIREWALL=true or configure ufw manually.")
	}

	log.Println("[INFO] GATEWAY_MODE: Successfully enabled. Host is now a gateway.")
	return nil
}

// --- Graceful Shutdown Functions ---

// setupGracefulShutdown listens for OS signals and initiates a cleanup.
func setupGracefulShutdown(cancel context.CancelFunc) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		// Wait for a signal
		sig := <-sigChan
		log.Printf("[WARN] Received signal: %v. Starting graceful shutdown...", sig)

		// Cancel the main context to stop the HTTP server
		cancel()
	}()
}

// cleanupAllInterfaces runs 'tcdel --all' on every active interface.
func cleanupAllInterfaces(ctx context.Context) {
	if isDarwin {
		return // No TC on Darwin
	}

	log.Println("[INFO] Cleaning up all TC rules from all interfaces...")

	ifaces, err := queryIPNetInterfaces(nil)
	if err != nil {
		log.Printf("[ERROR] Cleanup failed: Could not query interfaces: %v", err)
		return
	}

	for _, iface := range ifaces {
		log.Printf("[INFO] Cleaning up interface: %s", iface.Name)
		args := []string{"--all", iface.Name}
		if b, err := exec.CommandContext(ctx, "tcdel", args...).CombinedOutput(); err != nil {
			// Log error but continue
			log.Printf("[ERROR] Cleanup failed for %s: %v, %s", iface.Name, err, string(b))
		}
	}
	log.Println("[INFO] TC cleanup finished.")
}
