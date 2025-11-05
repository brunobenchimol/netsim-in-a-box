package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
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

const version = "4.2.1" // V4: Pure Go TC
const apiVersion = "v2" // The API path we are serving

func init() {
	isDarwin = runtime.GOOS == "darwin"

	// --- Standardize log format ---
	// Use ISO 8601 date, time, and UTC
	log.SetFlags(log.LstdFlags | log.LUTC)
	log.Printf("[INFO] OS darwin=%v", isDarwin)
}

func main() {
	// Setup graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	setupGracefulShutdown(cancel)

	if err := doMain(ctx); err != nil {
		log.Printf("[CRITICAL] CRITICAL FAILURE: %v", err)
		fmt.Println("-------------------------------------------------")
		fmt.Printf("ERROR: Application failed to start.\n")
		fmt.Printf("REASON: %v\n", err)
		fmt.Println("-------------------------------------------------")
		os.Exit(1)
	}
}

func doMain(ctx context.Context) error {
	log.Println("[INFO] WebUI for TC(Linux Traffic Control) V4")

	// Set default API port
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

	// --- Startup Log ---
	apiPort := strings.TrimPrefix(addr, ":")
	// Query interfaces *before* logging startup, so we can show IPs
	ifacesForLog, err := queryIPNetInterfaces(nil)
	if err != nil {
		// Log a warning, but don't fail startup just for this
		log.Printf("[WARN] Could not query host interfaces for startup message: %v", err)
	}
	// Log the startup message
	logStartupInfo(apiPort, ifacesForLog)

	// --- Chi Router Setup ---
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	// Use a custom logger middleware to match our log format
	r.Use(LoggerMiddleware)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	// --- API Routes ---
	r.Get("/tc/api/version", func(w http.ResponseWriter, r *http.Request) {
		respondWithJSON(w, http.StatusOK, map[string]string{
			"software_version": version,
			"api_version":      apiVersion,
		})
	})

	// Our V4 routes (keeping /v2/ path for compatibility)
	r.Route(fmt.Sprintf("/tc/api/%s/config", apiVersion), func(r chi.Router) {
		r.Get("/init", handleTcInit)
		r.Get("/setup", handleTcSetupV4) // Mapped to the new V4 handler
		r.Get("/reset", handleTcResetV4) // Mapped to the new V4 handler
		r.MethodFunc("GET", "/raw", handleTcRaw)
		r.MethodFunc("POST", "/raw", handleTcRaw)
	})

	// --- Static File Server ---
	uiStaticDir := "./frontend"
	log.Printf("[INFO] Serving V4 static UI from %s at /", uiStaticDir)
	fsV3 := http.StripPrefix("/", http.FileServer(http.Dir(uiStaticDir)))
	r.Get("/*", func(w http.ResponseWriter, r *http.Request) {
		f, err := os.Stat(uiStaticDir + r.URL.Path)
		if os.IsNotExist(err) || f.IsDir() {
			// If file doesn't exist (or is a dir), serve index.html for SPA routing
			http.ServeFile(w, r, uiStaticDir+"/index.html")
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

func LoggerMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

		next.ServeHTTP(ww, r)

		latency := time.Since(start)

		log.Printf("[ACCESS] %s %s - %d (%s)",
			r.Method,
			r.RequestURI,
			ww.Status(),
			latency,
		)
	})
}

// --- HTTP Response Helpers ---

func respondWithError(w http.ResponseWriter, message string, code int) {
	log.Printf("[ERROR] API Error: %s", message)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"code":    code,
		"message": message,
	})
}

func respondWithJSON(w http.ResponseWriter, code int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if payload != nil {
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			log.Printf("[ERROR] Failed to write JSON response: %v", err)
		}
	}
}

// --- Preflight, Gateway, and Shutdown Functions ---

// runPreflightChecks (V4: Removed tcconfig checks)
func runPreflightChecks(ctx context.Context) (checks []*PreflightCheck, ok bool) {
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
	// === Check 2: tc (iproute2) ===
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
	// === Check 3: ip (iproute2) ===
	{
		check := &PreflightCheck{Name: "ip (iproute2)", Required: true}
		if version, err := checkBinary("ip", "-V"); err != nil {
			check.Status = false
			check.Message = "Binary 'ip' not found. (Install with 'apt-get install iproute2')"
		} else {
			check.Status = true
			check.Message = fmt.Sprintf("OK (%s)", version)
		}
		checks = append(checks, check)
	}
	// === Check 4: Kernel Module 'ifb' ===
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
	// === Check 5: Kernel Module 'sch_htb' ===
	{
		check := &PreflightCheck{Name: "Kernel Module 'sch_htb'", Required: true}
		cmd := exec.CommandContext(ctx, "grep", "^sch_htb", "/proc/modules")
		if err := cmd.Run(); err != nil {
			check.Status = false
			check.Message = "Module 'sch_htb' not loaded. This is *required*."
		} else {
			check.Status = true
			check.Message = "OK (Module 'sch_htb' is loaded)"
		}
		checks = append(checks, check)
	}
	// === Check 6: Kernel Module 'sch_netem' ===
	{
		check := &PreflightCheck{Name: "Kernel Module 'sch_netem'", Required: true}
		cmd := exec.CommandContext(ctx, "grep", "^sch_netem", "/proc/modules")
		if err := cmd.Run(); err != nil {
			check.Status = false
			check.Message = "Module 'sch_netem' not loaded. This is *required*."
		} else {
			check.Status = true
			check.Message = "OK (Module 'sch_netem' is loaded)"
		}
		checks = append(checks, check)
	}

	ok = true
	for _, check := range checks {
		if check.Required && !check.Status {
			ok = false
		}
	}
	return checks, ok
}

// runGatewayCommand (Helper function, no changes)
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

// enableGatewayMode (Helper function, no changes)
func enableGatewayMode(ctx context.Context) error {
	log.Println("[INFO] GATEWAY_MODE: Enabling Default Gateway Mode...")

	if err := runGatewayCommand(ctx, "sysctl", "-w", "net.ipv4.ip_forward=1"); err != nil {
		return fmt.Errorf("failed to set net.ipv4.ip_forward: %w", err)
	}

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

	if err := runGatewayCommand(ctx, "iptables", "-t", "nat", "-A", "POSTROUTING", "-o", wanIface, "-j", "MASQUERADE"); err != nil {
		return fmt.Errorf("failed to apply NAT/MASQUERADE rule: %w", err)
	}
	if err := runGatewayCommand(ctx, "iptables", "-A", "FORWARD", "-o", wanIface, "-j", "ACCEPT"); err != nil {
		return fmt.Errorf("failed to apply FORWARD (out) rule: %w", err)
	}
	if err := runGatewayCommand(ctx, "iptables", "-A", "FORWARD", "-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT"); err != nil {
		return fmt.Errorf("failed to apply FORWARD (state) rule: %w", err)
	}

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

// logStartupInfo prints the welcome message with access ports and IPs.
func logStartupInfo(apiPort string, ifaces []*TcInterface) {
	squidPort := "3128" // This is static from our Dockerfile

	log.Println("----------------------------------------------------------")
	log.Printf("[INFO] NetSim-in-a-Box is READY (v%s)", version)
	log.Println("[INFO] Access Points:")
	log.Printf("[INFO]   - Web UI (API Port):   http://localhost:%s", apiPort)
	log.Printf("[INFO]   - HTTP Proxy (Squid):  http://localhost:%s", squidPort)
	log.Println("[INFO] ")
	log.Println("[INFO] Available Host IPs (use with the ports above):")
	log.Printf("[INFO]   - localhost / 127.0.0.1 (via port %s or %s)", apiPort, squidPort)

	if len(ifaces) > 0 {
		for _, iface := range ifaces {
			if iface.IPv4 != nil {
				// Log other IPs, making it clear they use the same ports
				log.Printf("[INFO]   - http://%s:%s (Interface: %s)", iface.IPv4.String(), apiPort, iface.Name)
			}
		}
	} else {
		log.Println("[INFO]   - (No other non-loopback IPs found)")
	}
	log.Println("----------------------------------------------------------")
}

// --- Graceful Shutdown Functions ---

// setupGracefulShutdown (No logic change)
func setupGracefulShutdown(cancel context.CancelFunc) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		log.Printf("[WARN] Received signal: %v. Starting graceful shutdown...", sig)
		cancel()
	}()
}
