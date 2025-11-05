// api_v2.go
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/ossrs/go-oryx-lib/errors"
	ohttp "github.com/ossrs/go-oryx-lib/http"
	"github.com/ossrs/go-oryx-lib/logger"
)

// V2NetworkOptions is a clean struct for the V2 API, reflecting
// tcset parameters without the clumsy "strategy1/strategy2" logic.
type V2NetworkOptions struct {
	// Filter Parameters
	Iface     string
	Direction string
	Protocol  string
	// Identification
	IdentifyKey   string
	IdentifyValue string
	// Netem Parameters
	Delay       string
	Jitter      string // This will be passed to --delay-distro
	DelayDistro string // uniform, normal, pareto, etc.
	Loss        string
	// NOTE: Correlation flags removed as tcconfig does not support them
	Duplicate string
	Reorder   string
	Corrupt   string
	// Rate Parameter (Bandwidth)
	Rate string
	// Packet Limit
	PacketLimit string
	// API Port (for exclusion)
	ApiPort string
}

// TcSetupV2 is the new HTTP handler for the /v2/setup API
func TcSetupV2(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	opts := &V2NetworkOptions{
		Iface:     q.Get("iface"),
		Direction: q.Get("direction"),
		Protocol:  q.Get("protocol"), // 'all'
		// Identification
		IdentifyKey:   q.Get("identifyKey"),   // 'all'
		IdentifyValue: q.Get("identifyValue"), // 'all'
		// Netem Parameters
		Delay:       q.Get("delay"),
		Jitter:      q.Get("jitter"),
		DelayDistro: q.Get("delayDistro"),
		Loss:        q.Get("loss"),
		Duplicate:   q.Get("duplicate"),
		Reorder:     q.Get("reorder"),
		Corrupt:     q.Get("corrupt"),
		// Rate
		Rate: q.Get("rate"),
		// Packet Limit
		PacketLimit: q.Get("packetLimit"),
		// API Port
		ApiPort: strings.Trim(os.Getenv("API_LISTEN"), ":"),
	}

	if err := opts.Execute(ctx); err != nil {
		return err // Return the error to the ohttp handler
	}

	logger.Tf(ctx, "V2: Successfully applied rules to %v", opts.Iface)
	ohttp.WriteData(ctx, w, r, nil)
	return nil
}

// Execute builds and runs a *single* tcset command with all V2 parameters
func (v *V2NetworkOptions) Execute(ctx context.Context) error {
	if v.Iface == "" {
		return errors.New("V2: 'iface' is required")
	}
	if v.Direction == "" {
		return errors.New("V2: 'direction' is required")
	}

	// Ignore if Darwin (logic maintained from V1)
	if isDarwin {
		logger.Tf(ctx, "V2: Darwin: Ignoring network setup")
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
		// Ported V1 Logic
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
			// hasIFB is a global var from main.go
			return errors.Errorf("V2: 'ifb' module not loaded. 'incoming' rules will fail.")
		}
		args = append(args, "--direction", "incoming", "--exclude-dst-port", v.ApiPort)
		// Ported V1 Logic
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
		logger.Wf(ctx, "V2: Unknown direction '%s'", v.Direction)
	}

	// 2. Build Netem Arguments (tcconfig supported flags only)
	hasNetemRules := false

	// Delay & Jitter
	if v.Delay != "" {
		hasNetemRules = true
		args = append(args, "--delay", fmt.Sprintf("%vms", v.Delay))

		// Jitter is applied via the --delay-distro flag
		if v.Jitter != "" {
			args = append(args, "--delay-distro", fmt.Sprintf("%vms", v.Jitter))
		}

		// This is for *distribution type* (normal, pareto)
		// We can't have both jitter and distribution, so jitter takes precedence.
		if v.Jitter == "" && v.DelayDistro != "" {
			args = append(args, "--delay-distribution", v.DelayDistro)
		}

		// These require --delay to be set, so they are inside this block.
		// Names are also corrected.
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

	// Loss (Independent rule)
	if v.Loss != "" {
		hasNetemRules = true
		args = append(args, "--loss", fmt.Sprintf("%v%%", v.Loss))
		// Correlation for loss is NOT supported by tcset, so it's removed.
	}

	// 3. Build Rate (Bandwidth) Argument
	if v.Rate != "" {
		args = append(args, "--rate", fmt.Sprintf("%vkbps", v.Rate))
	}

	// 4. Build Packet Limit Argument
	if v.PacketLimit != "" {
		args = append(args, "--packet-limit", v.PacketLimit)
	}

	// Check for empty rules
	if !hasNetemRules && v.Rate == "" && v.PacketLimit == "" {
		logger.Tf(ctx, "V2: No rules specified. Skipping tcset.")
		return nil
	}

	// 5. Add the Interface
	args = append(args, v.Iface)

	// 6. Execute the Command
	logger.Tf(ctx, "V2: Executing tcset %v", strings.Join(args, " "))
	if b, err := exec.CommandContext(ctx, "tcset", args...).CombinedOutput(); err != nil {
		// We return the full error string
		errStr := string(b)
		if errStr == "" {
			errStr = err.Error()
		}
		// Wrap the error to include the command that was run
		return errors.Errorf("V2: tcset %v: %v", strings.Join(args, " "), errStr)
	} else if bs := string(b); len(bs) > 0 {
		// V1 error-ignoring logic
		nnErrors := strings.Count(bs, "ERROR")
		isIngressDel := strings.Contains(bs, "ingress") && strings.Contains(bs, "qdisc del")
		canIgnore := nnErrors == 1 && isIngressDel

		if nnErrors > 0 && !canIgnore {
			return errors.Errorf("V2: tcset %v, %v", strings.Join(args, " "), bs)
		}
		logger.Tf(ctx, "V2: tcset %v, error=%v, ingress=%v, ignore=%v, %v",
			strings.Join(args, " "), nnErrors, isIngressDel, canIgnore, bs)
	}

	return nil
}

// TcResetV2 is the reset handler for the V2 API.
// It is functionally identical to V1's TcReset.
func TcResetV2(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	iface := r.URL.Query().Get("iface")
	if iface == "" {
		return errors.New("V2: 'iface' is required")
	}

	if isDarwin {
		logger.Tf(ctx, "V2: Darwin: Ignoring network reset")
		ohttp.WriteData(ctx, w, r, nil)
		return nil
	}

	logger.Tf(ctx, "V2: Resetting rules on %v", iface)

	// We reuse the exact logic from `TcReset`
	args := []string{"--all", iface}
	if b, err := exec.CommandContext(ctx, "tcdel", args...).CombinedOutput(); err != nil {
		return errors.Wrapf(err, "V2: tcdel %v", strings.Join(args, " "))
	} else if bs := string(b); len(bs) > 0 {
		nnErrors := strings.Count(bs, "ERROR")
		isIngressDel := strings.Contains(bs, "ingress") && strings.Contains(bs, "qdisc del")
		canIgnore := nnErrors == 1 && isIngressDel

		if nnErrors > 0 && !canIgnore {
			return errors.Errorf("V2: tcdel %v, %v", strings.Join(args, " "), bs)
		}
		logger.Tf(ctx, "V2: tcdel %v, error=%v, ingress=%v, ignore=%v, %v",
			strings.Join(args, " "), nnErrors, isIngressDel, canIgnore, bs)
	}

	ohttp.WriteData(ctx, w, r, nil)
	return nil
}
