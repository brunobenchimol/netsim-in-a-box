package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// --- Structs (Ported from tc.go) ---
type TcTime time.Time

func (v TcTime) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf("\"%v\"", v.String())), nil
}
func (v TcTime) String() string {
	return time.Time(v).Format("2006-01-02T15:04:05.000Z07:00")
}

type TcIP net.IP

func (v TcIP) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf("\"%v\"", v.String())), nil
}
func (v TcIP) String() string {
	return net.IP(v).String()
}

type TcInterface struct {
	Name string `json:"name,omitempty"`
	IPv4 TcIP   `json:"ipv4,omitempty"`
	IPv6 TcIP   `json:"ipv6,omitempty"`
}

func (v *TcInterface) String() string {
	return fmt.Sprintf("name=%v, ipv4=%v, ipv6=%v", v.Name, v.IPv4.String(), v.IPv6.String())
}

// --- Command Helpers ---
// runCommand is a generic helper to execute commands
func runCommand(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	log.Printf("[INFO] V4: Executing: %s", cmd.String())

	if b, err := cmd.CombinedOutput(); err != nil {
		errStr := string(b)
		if errStr == "" {
			errStr = err.Error()
		}
		// --- Suppress more benign cleanup errors ---
		// Don't return error for cleanup messages.
		if strings.Contains(errStr, "No such file or directory") ||
			strings.Contains(errStr, "Cannot find specified qdisc") ||
			strings.Contains(errStr, "Cannot find device") ||
			strings.Contains(errStr, "Cannot delete qdisc with handle of zero") {
			return nil
		}

		log.Printf("[ERROR] V4: Command %s failed: %s", cmd.String(), errStr)
		return fmt.Errorf("%s %v: %s", name, args, errStr)
	}
	return nil
}

// runTC is a specific helper for 'tc'
func runTC(ctx context.Context, args ...string) error {
	return runCommand(ctx, "tc", args...)
}

// runIP is a specific helper for 'ip'
func runIP(ctx context.Context, args ...string) error {
	return runCommand(ctx, "ip", args...)
}

// --- Handler: /init ---
// (Ported from previous handlers.go, no logic changes)
func handleTcInit(w http.ResponseWriter, r *http.Request) {
	ifaces, err := queryIPNetInterfaces(nil)
	if err != nil {
		respondWithError(w, fmt.Sprintf("failed to query interfaces: %v", err), 500)
		return
	}
	if len(ifaces) == 0 {
		msg := "No active (non-loopback, up) network interfaces with valid IPs found."
		log.Printf("[ERROR] %s", msg)
		respondWithError(w, msg, 500)
		return
	}
	response := struct {
		Ifaces []*TcInterface `json:"ifaces,omitempty"`
	}{
		ifaces,
	}
	respondWithJSON(w, http.StatusOK, response)
}

// --- Handler: /reset (V4) ---
// (Replaces tcdel)
func handleTcResetV4(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	iface := r.URL.Query().Get("iface")
	if iface == "" {
		respondWithError(w, "V4: 'iface' is required", 400)
		return
	}
	if isDarwin {
		log.Println("[INFO] V4: Darwin: Ignoring network reset")
		respondWithJSON(w, http.StatusOK, nil)
		return
	}

	log.Printf("[INFO] V4: Resetting native rules on %v", iface)
	if err := cleanupSingleInterface(ctx, iface); err != nil {
		respondWithError(w, err.Error(), 500)
		return
	}
	respondWithJSON(w, http.StatusOK, nil)
}

// --- Handler: /setup (V4) ---
// (Replaces tcset)

type V4NetworkOptions struct {
	Iface     string
	Direction string
	ApiPort   string
	// V4 Parameters
	Rate             string // kbit
	Delay            string // ms
	Jitter           string // ms
	DelayCorrelation string // %
	Distribution     string // normal, pareto, etc.
	Loss             string // %
	LossCorrelation  string // %
	Corrupt          string // %
	Duplicate        string // %
	Reorder          string // %
}

func handleTcSetupV4(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	opts := &V4NetworkOptions{
		Iface:            q.Get("iface"),
		Direction:        q.Get("direction"),
		ApiPort:          strings.Trim(os.Getenv("API_LISTEN"), ":"),
		Rate:             q.Get("rate"),
		Delay:            q.Get("delay"),
		Jitter:           q.Get("jitter"),
		DelayCorrelation: q.Get("delayCorrelation"),
		Distribution:     q.Get("distribution"),
		Loss:             q.Get("loss"),
		LossCorrelation:  q.Get("lossCorrelation"),
		Corrupt:          q.Get("corrupt"),
		Duplicate:        q.Get("duplicate"),
		Reorder:          q.Get("reorder"),
	}

	if err := opts.Execute(ctx); err != nil {
		respondWithError(w, err.Error(), 500)
		return
	}

	log.Printf("[INFO] V4: Native rules applied successfully to %v", opts.Iface)
	respondWithJSON(w, http.StatusOK, nil)
}

// Execute is the new native 'tc' command builder
func (v *V4NetworkOptions) Execute(ctx context.Context) error {
	if v.Iface == "" {
		return fmt.Errorf("V4: 'iface' is required")
	}
	if v.Direction == "" {
		return fmt.Errorf("V4: 'direction' is required")
	}
	if isDarwin {
		log.Println("[INFO] V4: Darwin: Ignoring network setup")
		return nil
	}

	// 1. Atomic Operation: Clean old rules FIRST
	if err := cleanupSingleInterface(ctx, v.Iface); err != nil {
		return fmt.Errorf("V4: cleanup failed before setup: %w", err)
	}

	// 2. Determine Effective Interface (ifb logic)
	effectiveIface := v.Iface
	apiFilterPortCmd := "sport" // Outgoing traffic (from API)
	if v.Direction == "incoming" {
		if !hasIFB {
			return fmt.Errorf("V4: 'ifb' module not loaded on host. 'incoming' rules cannot be applied")
		}

		// 1. Bring up ifb0 interface
		if err := runIP(ctx, "link", "set", "dev", "ifb0", "up"); err != nil {
			return fmt.Errorf("V4: failed to bring up 'ifb0': %w", err)
		}
		// 2. Add ingress qdisc to real interface
		if err := runTC(ctx, "qdisc", "add", "dev", v.Iface, "ingress"); err != nil {
			return fmt.Errorf("V4: failed to add ingress qdisc on '%s': %w", v.Iface, err)
		}
		// 3. Add filter to mirror all inbound traffic to ifb0's output
		if err := runTC(ctx, "filter", "add", "dev", v.Iface, "parent", "ffff:",
			"protocol", "all", "u32", "match", "u32", "0", "0",
			"action", "mirred", "egress", "redirect", "dev", "ifb0"); err != nil {
			return fmt.Errorf("V4: failed to add mirred filter on '%s': %w", v.Iface, err)
		}

		effectiveIface = "ifb0"    // Rules are now applied to the egress of ifb0
		apiFilterPortCmd = "dport" // Incoming traffic (to the API)
	}

	// 3. Build the Fixed HTB Tree

	// 3a. Root Qdisc: htb, default 11 (slow traffic)
	if err := runTC(ctx, "qdisc", "add", "dev", effectiveIface, "root", "handle", "1:", "htb", "default", "11"); err != nil {
		return fmt.Errorf("V4: failed to add root htb qdisc: %w", err)
	}

	// 3b. "Fast" Class (API): 1:10, unlimited bandwidth
	if err := runTC(ctx, "class", "add", "dev", effectiveIface, "parent", "1:", "classid", "1:10", "htb", "rate", "10gbit"); err != nil {
		return fmt.Errorf("V4: failed to add 'fast' htb class: %w", err)
	}

	// 3c. "Slow" Class (Simulation): 1:11, with user's 'rate'
	rateLimit := "10gbit" // Unlimited default if not provided
	if v.Rate != "" {
		rateLimit = fmt.Sprintf("%vkbit", v.Rate)
	}
	if err := runTC(ctx, "class", "add", "dev", effectiveIface, "parent", "1:", "classid", "1:11", "htb", "rate", rateLimit); err != nil {
		return fmt.Errorf("V4: failed to add 'slow' htb class: %w", err)
	}

	// 4. Build and Attach 'netem' to the "Slow" Class (1:11)
	netemArgs := []string{"qdisc", "add", "dev", effectiveIface, "parent", "1:11", "handle", "10:", "netem"}
	hasNetemRules := false

	// Delay, Jitter, Correlation, Distribution
	// We now trust the UI to send valid, dependent combinations.
	if v.Delay != "" {
		hasNetemRules = true
		netemArgs = append(netemArgs, "delay", fmt.Sprintf("%vms", v.Delay))

		// Jitter is positional, requires Delay
		if v.Jitter != "" {
			jitterVal := v.Jitter
			// Fix: 'distribution' requires a non-zero jitter.
			if (jitterVal == "0") && v.Distribution != "" {
				jitterVal = "1" // Force 1ms
			}
			netemArgs = append(netemArgs, fmt.Sprintf("%vms", jitterVal))

			// Correlation is positional, requires Jitter
			if v.DelayCorrelation != "" {
				netemArgs = append(netemArgs, fmt.Sprintf("%v%%", v.DelayCorrelation))
			}
		}

		// Distribution is keyword, requires Delay (and non-zero Jitter)
		if v.Distribution != "" {
			netemArgs = append(netemArgs, "distribution", v.Distribution)
		}
	}

	// Loss, Loss Correlation
	if v.Loss != "" {
		hasNetemRules = true
		netemArgs = append(netemArgs, "loss", fmt.Sprintf("%v%%", v.Loss))
		if v.LossCorrelation != "" {
			netemArgs = append(netemArgs, "correlation", fmt.Sprintf("%v%%", v.LossCorrelation))
		}
	}

	// Other Netem rules
	if v.Corrupt != "" {
		hasNetemRules = true
		netemArgs = append(netemArgs, "corrupt", fmt.Sprintf("%v%%", v.Corrupt))
	}
	if v.Duplicate != "" {
		hasNetemRules = true
		netemArgs = append(netemArgs, "duplicate", fmt.Sprintf("%v%%", v.Duplicate))
	}
	if v.Reorder != "" {
		hasNetemRules = true
		netemArgs = append(netemArgs, "reorder", fmt.Sprintf("%v%%", v.Reorder))
	}

	// Only attach 'netem' if there are rules for it
	if hasNetemRules {
		if err := runTC(ctx, netemArgs...); err != nil {
			return fmt.Errorf("V4: failed to add netem qdisc: %w", err)
		}
	}

	// 5. Apply u32 Filters

	// 5a. API Filter (Prio 1) -> "Fast" Class (1:10)
	// (We use --dport or --sport depending on direction)
	if err := runTC(ctx, "filter", "add", "dev", effectiveIface, "protocol", "ip", "parent", "1:", "prio", "1",
		"u32", "match", "ip", apiFilterPortCmd, v.ApiPort, "0xffff",
		"flowid", "1:10"); err != nil {
		return fmt.Errorf("V4: failed to add 'fast' API filter: %w", err)
	}

	// 5b. "All Else" Filter (Prio 2) -> "Slow" Class (1:11)
	if err := runTC(ctx, "filter", "add", "dev", effectiveIface, "protocol", "all", "parent", "1:", "prio", "2",
		"u32", "match", "u32", "0", "0",
		"flowid", "1:11"); err != nil {
		return fmt.Errorf("V4: failed to add default 'slow' filter: %w", err)
	}

	return nil
}

// --- Handler: /raw (V4) ---
// (Ported, but now allows 'tc' and 'ip')
func handleTcRaw(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cmd := ""

	if r.Method == "POST" {
		defer r.Body.Close()
		if b, err := io.ReadAll(r.Body); err != nil {
			respondWithError(w, fmt.Sprintf("failed to read request body: %v", err), 400)
			return
		} else if len(b) > 0 {
			cmd = string(b)
		}
	}
	if cmd == "" {
		cmd = r.URL.Query().Get("cmd")
	}
	if cmd == "" {
		respondWithError(w, "no command provided in body or 'cmd' query param", 400)
		return
	}

	log.Printf("[INFO] RAW: Executing raw cmd: %v", cmd)
	args := strings.Split(cmd, " ")
	if len(args) == 0 {
		respondWithError(w, "empty command", 400)
		return
	}

	// V4 Security: Whitelist 'tc' and 'ip'
	arg0 := args[0]
	switch arg0 {
	case "tc", "ip":
		// Command allowed
	default:
		respondWithError(w, fmt.Sprintf("invalid command: %v. Only 'tc' and 'ip' are allowed", arg0), 403)
		return
	}

	if b, err := exec.CommandContext(ctx, arg0, args[1:]...).Output(); err != nil {
		respondWithError(w, fmt.Sprintf("exec %v: %v", cmd, err), 500)
		return
	} else if len(b) == 0 {
		log.Printf("[INFO] RAW: exec %v ok (no output)", cmd)
		respondWithJSON(w, http.StatusOK, map[string]string{"status": "ok", "output": ""})
	} else {
		log.Printf("[INFO] RAW: exec %v ok (with output)", cmd)
		// Return as plain text, since 'tc' rarely returns JSON
		respondWithJSON(w, http.StatusOK, map[string]string{"status": "ok", "output": string(b)})
	}
}

// --- Cleanup Logic (V4) ---

// cleanupSingleInterface cleans a single interface (and ifb0 if incoming)
func cleanupSingleInterface(ctx context.Context, iface string) error {
	// Clean main interface (root and ingress)
	if err := runTC(ctx, "qdisc", "del", "dev", iface, "root"); err != nil {
		log.Printf("[DEBUG] V4 Cleanup: Failed to clean root of %s (likely already clean): %v", iface, err)
	}
	if err := runTC(ctx, "qdisc", "del", "dev", iface, "ingress"); err != nil {
		log.Printf("[DEBUG] V4 Cleanup: Failed to clean ingress of %s (likely already clean): %v", iface, err)
	}

	// If ifb was used, clean it too
	if hasIFB {
		if err := runTC(ctx, "qdisc", "del", "dev", "ifb0", "root"); err != nil {
			log.Printf("[DEBUG] V4 Cleanup: Failed to clean root of ifb0 (likely already clean): %v", err)
		}
	}
	return nil
}

// cleanupAllInterfaces (V4) is called on graceful shutdown
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
		cleanupSingleInterface(ctx, iface.Name)
	}
}

// queryIPNetInterfaces (Helper, ported)
func queryIPNetInterfaces(filter func(iface *net.Interface, addr net.Addr) bool) ([]*TcInterface, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("query interfaces: %w", err)
	}
	var targets []*TcInterface
	log.Printf("[INFO] Found %d total system interfaces. Filtering...", len(ifaces))

	for _, iface := range ifaces {
		if (iface.Flags & net.FlagPointToPoint) == net.FlagPointToPoint {
			continue
		}
		if (iface.Flags & net.FlagUp) == 0 {
			continue
		}
		if (iface.Flags & net.FlagLoopback) != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			return nil, fmt.Errorf("query addrs of %v: %w", iface.Name, err)
		}

		ti := &TcInterface{Name: iface.Name}
		for _, addr := range addrs {
			if filter != nil {
				if ok := filter(&iface, addr); !ok {
					continue
				}
			}

			if r0, ok := addr.(*net.IPNet); ok {
				if ip := r0.IP.To4(); ip != nil {
					ti.IPv4 = TcIP(ip)
				} else if ip := r0.IP.To16(); ip != nil {
					ti.IPv6 = TcIP(ip)
				}
			}
		}

		if ti.IPv4 != nil || ti.IPv6 != nil {
			targets = append(targets, ti)
			log.Printf("[INFO]  - SUCCESS: Added %s to list", iface.Name)
		}
	}
	return targets, nil
}
