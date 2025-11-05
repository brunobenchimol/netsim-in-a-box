package main

import (
	"context"
	"encoding/json"
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

// --- Structs (from tc.go) ---

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

// --- Handler: /init ---

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

// --- Handler: /setup ---

type V2NetworkOptions struct {
	Iface         string
	Direction     string
	Protocol      string
	IdentifyKey   string
	IdentifyValue string
	Delay         string
	Jitter        string
	DelayDistro   string
	Loss          string
	Duplicate     string
	Reorder       string
	Corrupt       string
	Rate          string
	PacketLimit   string
	ApiPort       string
}

func handleTcSetupV2(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	opts := &V2NetworkOptions{
		Iface:         q.Get("iface"),
		Direction:     q.Get("direction"),
		Protocol:      q.Get("protocol"),
		IdentifyKey:   q.Get("identifyKey"),
		IdentifyValue: q.Get("identifyValue"),
		Delay:         q.Get("delay"),
		Jitter:        q.Get("jitter"),
		DelayDistro:   q.Get("delayDistro"),
		Loss:          q.Get("loss"),
		Duplicate:     q.Get("duplicate"),
		Reorder:       q.Get("reorder"),
		Corrupt:       q.Get("corrupt"),
		Rate:          q.Get("rate"),
		PacketLimit:   q.Get("packetLimit"),
		ApiPort:       strings.Trim(os.Getenv("API_LISTEN"), ":"),
	}

	if err := opts.Execute(ctx); err != nil {
		// O 'Execute' agora retorna um erro detalhado do tcset
		respondWithError(w, err.Error(), 500)
		return
	}

	log.Printf("[INFO] V2: Successfully applied rules to %v", opts.Iface)
	respondWithJSON(w, http.StatusOK, nil)
}

func (v *V2NetworkOptions) Execute(ctx context.Context) error {
	if v.Iface == "" {
		return fmt.Errorf("V2: 'iface' is required")
	}
	if v.Direction == "" {
		return fmt.Errorf("V2: 'direction' is required")
	}
	if isDarwin {
		log.Println("[INFO] V2: Darwin: Ignoring network setup")
		return nil
	}

	args := []string{
		"--overwrite",
		"--shaping-algo", "htb",
	}

	// 1. Configure Direction, API Exclusion, and Filtering
	switch v.Direction {
	case "outgoing":
		args = append(args, "--direction", "outgoing", "--exclude-src-port", v.ApiPort)
		if v.IdentifyKey != "all" && v.IdentifyValue != "" {
			switch v.IdentifyKey {
			case "serverPort":
				args = append(args, "--src-port", v.IdentifyValue)
			case "clientIp":
				args = append(args, "--dst-network", v.IdentifyValue)
			case "clientPort":
				args = append(args, "--dst-port", v.IdentifyValue)
			}
		}
	case "incoming":
		if !hasIFB {
			return fmt.Errorf("V2: 'ifb' module not loaded. 'incoming' rules will fail")
		}
		args = append(args, "--direction", "incoming", "--exclude-dst-port", v.ApiPort)
		if v.IdentifyKey != "all" && v.IdentifyValue != "" {
			switch v.IdentifyKey {
			case "serverPort":
				args = append(args, "--dst-port", v.IdentifyValue)
			case "clientIp":
				args = append(args, "--src-network", v.IdentifyValue)
			case "clientPort":
				args = append(args, "--src-port", v.IdentifyValue)
			}
		}
	default:
		log.Printf("[WARN] V2: Unknown direction '%s'", v.Direction)
	}

	// 2. Build Netem Arguments (Using corrected flags from previous step)
	hasNetemRules := false
	if v.Delay != "" {
		hasNetemRules = true
		args = append(args, "--delay", fmt.Sprintf("%vms", v.Delay))

		// Jitter é aplicado via --delay-distro
		if v.Jitter != "" {
			args = append(args, "--delay-distro", fmt.Sprintf("%vms", v.Jitter))
		}
		// Distribution (normal, pareto) só é aplicada se Jitter NÃO for
		if v.Jitter == "" && v.DelayDistro != "" {
			args = append(args, "--delay-distribution", v.DelayDistro)
		}
		// Regras dependentes (precisam de --delay)
		if v.Duplicate != "" {
			args = append(args, "--duplicate", fmt.Sprintf("%v%%", v.Duplicate))
		}
		if v.Corrupt != "" {
			args = append(args, "--corrupt", fmt.Sprintf("%v%%", v.Corrupt))
		}
		if v.Reorder != "" {
			args = append(args, "--reordering", fmt.Sprintf("%v%%", v.Reorder))
		}
	}
	// Loss (Regra independente)
	if v.Loss != "" {
		hasNetemRules = true
		args = append(args, "--loss", fmt.Sprintf("%v%%", v.Loss))
	}

	// 3. Build Rate (Bandwidth) Argument
	if v.Rate != "" {
		args = append(args, "--rate", fmt.Sprintf("%vkbps", v.Rate))
	}

	// 4. Build Packet Limit Argument
	if v.PacketLimit != "" {
		args = append(args, "--packet-limit", v.PacketLimit)
	}

	if !hasNetemRules && v.Rate == "" && v.PacketLimit == "" {
		log.Println("[INFO] V2: No rules specified. Skipping tcset.")
		return nil
	}

	// 5. Add the Interface
	args = append(args, v.Iface)

	// 6. Execute the Command
	log.Printf("[INFO] V2: Executing tcset %v", strings.Join(args, " "))
	if b, err := exec.CommandContext(ctx, "tcset", args...).CombinedOutput(); err != nil {
		errStr := string(b)
		if errStr == "" {
			errStr = err.Error()
		}
		return fmt.Errorf("V2: tcset %v: %v", strings.Join(args, " "), errStr)
	} else if bs := string(b); len(bs) > 0 {
		nnErrors := strings.Count(bs, "ERROR")
		isIngressDel := strings.Contains(bs, "ingress") && strings.Contains(bs, "qdisc del")
		canIgnore := nnErrors == 1 && isIngressDel

		if nnErrors > 0 && !canIgnore {
			return fmt.Errorf("V2: tcset %v, %v", strings.Join(args, " "), bs)
		}
		log.Printf("[INFO] V2: tcset %v, error=%v, ingress=%v, ignore=%v, %v",
			strings.Join(args, " "), nnErrors, isIngressDel, canIgnore, bs)
	}
	return nil
}

// --- Handler: /reset ---

func handleTcResetV2(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	iface := r.URL.Query().Get("iface")
	if iface == "" {
		respondWithError(w, "V2: 'iface' is required", 400)
		return
	}

	if isDarwin {
		log.Println("[INFO] V2: Darwin: Ignoring network reset")
		respondWithJSON(w, http.StatusOK, nil)
		return
	}

	log.Printf("[INFO] V2: Resetting rules on %v", iface)

	args := []string{"--all", iface}
	if b, err := exec.CommandContext(ctx, "tcdel", args...).CombinedOutput(); err != nil {
		respondWithError(w, fmt.Sprintf("V2: tcdel %v: %v", strings.Join(args, " "), err), 500)
		return
	} else if bs := string(b); len(bs) > 0 {
		nnErrors := strings.Count(bs, "ERROR")
		isIngressDel := strings.Contains(bs, "ingress") && strings.Contains(bs, "qdisc del")
		canIgnore := nnErrors == 1 && isIngressDel

		if nnErrors > 0 && !canIgnore {
			respondWithError(w, fmt.Sprintf("V2: tcdel %v, %v", strings.Join(args, " "), bs), 500)
			return
		}
		log.Printf("[INFO] V2: tcdel %v, error=%v, ingress=%v, ignore=%v, %v",
			strings.Join(args, " "), nnErrors, isIngressDel, canIgnore, bs)
	}

	respondWithJSON(w, http.StatusOK, nil)
}

// --- Helper: queryIPNetInterfaces (from tc.go) ---

func queryIPNetInterfaces(filter func(iface *net.Interface, addr net.Addr) bool) ([]*TcInterface, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("query interfaces: %w", err)
	}
	var targets []*TcInterface
	log.Printf("[INFO] Found %d total system interfaces. Filtering...", len(ifaces))

	for _, iface := range ifaces {
		// Use a more debug-level log, as this is very verbose
		// log.Printf("[DEBUG] Inspecting interface: %s, Flags: %v", iface.Name, iface.Flags.String())
		if (iface.Flags & net.FlagPointToPoint) == net.FlagPointToPoint {
			continue
		}
		if (iface.Flags & net.FlagUp) == 0 {
			// log.Printf("[DEBUG] Skipping %s: Interface is down", iface.Name)
			continue
		}
		if (iface.Flags & net.FlagLoopback) != 0 {
			// log.Printf("[DEBUG] Skipping %s: Interface is loopback", iface.Name)
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			return nil, fmt.Errorf("query addrs of %v: %w", iface.Name, err)
		}
		// log.Printf("[DEBUG]  - Found %d addresses for %s", len(addrs), iface.Name)

		ti := &TcInterface{Name: iface.Name}
		for _, addr := range addrs {
			// log.Printf("[DEBUG]    - Inspecting addr: %v", addr.String())
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
		} else {
			// log.Printf("[DEBUG]  - FAILED: No valid IPv4/IPv6 found for %s, skipping", iface.Name)
		}
	}
	return targets, nil
}

// --- NEW: Handler: /raw (Ported from V1) ---

func handleTcRaw(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cmd := ""

	// Read from POST body first
	if r.Method == "POST" {
		defer r.Body.Close()
		if b, err := io.ReadAll(r.Body); err != nil {
			respondWithError(w, fmt.Sprintf("failed to read request body: %v", err), 400)
			return
		} else if len(b) > 0 {
			cmd = string(b)
		}
	}

	// If body is empty, try GET query param
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

	// Security: Whitelist allowed commands
	arg0 := args[0]
	switch arg0 {
	case "tcset", "tcshow", "tcdel":
		// Command is allowed
	default:
		respondWithError(w, fmt.Sprintf("invalid command: %v. Only 'tcset', 'tcshow', 'tcdel' are allowed", arg0), 403)
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

		// Try to unmarshal as JSON (tcshow does this)
		var res interface{}
		if err := json.Unmarshal(b, &res); err != nil {
			// If not JSON, return as plain text
			respondWithJSON(w, http.StatusOK, map[string]string{"status": "ok", "output": string(b)})
		} else {
			// If JSON, return the JSON object
			respondWithJSON(w, http.StatusOK, res)
		}
	}
}
