# NetSim-in-a-Box

<p align="left">
  <a href="https://github.com/brunobenchimol/netsim-in-a-box/actions/workflows/docker-publish.yml">
    <img src="https://github.com/brunobenchimol/netsim-in-a-box/actions/workflows/docker-publish.yml/badge.svg" alt="CI/CD Status">
  </a>
  <a href="https://hub.docker.com/r/brunobenchimol/netsim-in-a-box">
    <img src="https://img.shields.io/docker/pulls/brunobenchimol/netsim-in-a-box" alt="Docker Pulls">
  </a>
</p>

**A self-contained, Docker-based network simulation tool for developers.**

This tool provides a simple web interface (`tc-ui`), a http proxy (`squid`) and measuring network performance (`iperf3`) in a single container. It allows development teams to easily simulate adverse network conditions (like high latency, packet loss, and bandwidth throttling) on their local machines by manipulating the host's Linux traffic control (`tc`) settings. 

This is a fork of the original (and now unmaintained) `ossrs/tc-ui` (https://github.com/ossrs/tc-ui), rebuilt on a modern Go backend, a new lightweight Javascript/Tailwind frontend, and packaged with Squid proxy in a secure, multi-stage Docker image.

## How it Works: The Architecture

This tool runs as a single, monolithic Docker container managed by `supervisord`. It contains two key services:

1.  **`tc-ui` (Go App):** The backend API and web frontend, exposed on port `2023`. This service runs the `tcset`, `tcdel`, etc., commands based on your UI input.
2.  **`squid` (Proxy):** A non-caching proxy server, exposed on port `3128`.
3.  **`iperf3` (Server):** A network bandwidth testing server, exposed on port `5202`.

To function, the container **must** run with `--net=host`. This gives the `tc-ui` process permission to see and apply `tc` rules directly to your host's real network interfaces (e.g., `ens33`, `docker0`).

The developer then routes their application's traffic through the `squid` proxy (`localhost:3128`), and `tc-ui` applies the network rules to the host interface that traffic is using.

---

## 1. Getting Started: Installation

You have two options to get the Docker image.

### Option 1: Pull from Docker Hub (Recommended)

This is the fastest method. The image is pre-built and hosted on Docker Hub.

```bash
docker pull brunobenchimol/netsim-in-a-box:latest
```

*(After pulling, you can rename it for convenience: `docker tag brunobenchimol/netsim-in-a-box:latest netsim-in-a-box`)*

### Option 2. Build from Source

If you want to modify the code or build it yourself, you can build the image locally.

```bash
# 1. Clone the repository
git clone https://github.com/brunobenchimol/netsim-in-a-box.git
cd netsim-in-a-box

# 2. Build the image
docker build -t netsim-in-a-box .
```

## 2. Host Prerequisites (Important)

This tool relies on Linux Kernel modules for traffic control (tc). Your host (the Linux VM, not the container) must have these modules loaded.  

On a Debian/Ubuntu-based host, run the following commands *one time* to ensure the modules are available:

```bash
# Required for 'incoming' (ingress) rules
sudo modprobe ifb

# Required for 'rate' (bandwidth)
sudo modprobe sch_htb

# Required for 'delay' (latency) and 'loss'
sudo modprobe sch_netem
```

## 3. Running the Container (Host Network Mode)

This is the **recommended and intended** run mode (Granular Capabilities).

```bash
docker run --rm -it \
--cap-add=NET_ADMIN \
--cap-add=NET_RAW \
--net=host \
-p 2023:2023 \
netsim-in-a-box:latest
```

*Alternative*: Big Hammer Method (`--privileged`)

```bash
docker run --rm -it \
--privileged \
--net host \
-p 2023:2023 \
netsim-in-a-box:latest
```

The container is now running, and the services are bound directly to your host's localhost:

* **Web UI:** `http://localhost:2023`
* **Proxy Port:** `http://localhost:3128`

### Why is `--net=host` crucial?

* By default, Docker isolates the container's network. The container only sees its own internal `eth0` interface. 
* The `--net=host` flag **removes** this isolation, allowing the `tc-ui` process to see and apply `tc` rules directly to your host's real network interfaces (e.g., `ens33`, `docker0`, etc.).
* Without this flag, you will only be able to apply rules to the container's internal traffic.

**Note on Ports:** When `--net=host` is used, port mapping (like `-p 2023:2023`) is ignored. The container binds directly to the host's ports.

### Why `--cap-add=NET_ADMIN`? 

* This grants the container the necessary permissions to modify the host's network stack (which is what `tc` does).

## Inspecting Container Image

Change docker entrypoint to `/bin/bash`.

```bash
docker run --entrypoint /bin/bash \
--rm -it \
netsim-in-a-box:latest
```

## 4. Usage Workflow

1. **Start the Container:** Use the docker run command from Step 3.

2. **Open the Web UI:** Open your browser to http://localhost:2023.

3. **Select Interface:** The UI will list your host's network interfaces (e.g., eth0, docker0, wlan0). Select the one your target application will be using for its traffic.

4. **Apply Rules:** Use the form to apply network conditions (e.g., 300ms delay, 1% packet loss).

5. **Configure Your App's Proxy:**

* For a browser: Use a proxy switcher extension (like FoxyProxy) to route traffic through http://localhost:3128.

* For terminal apps: Set the environment variables:

```bash
export HTTP_PROXY="http://localhost:3128"
export HTTPS_PROXY="http://localhost:3128"

# Now test
curl [https://google.com](https://google.com)
```

* For other applications: Configure them in their settings to use an HTTP proxy at localhost:3128.

6. **Test:** Your application's traffic is now being shaped by the rules you applied.
 
7. **Reset:** When finished, click the "Reset All Rules" button in the UI.

## Simulation Presets

To make testing easier, `netsim-in-a-box` v4.5+ includes 12 built-in presets that cover common real-world network scenarios.

Just select a preset from the "Simulation Presets" dropdown. The form will auto-fill with the correct values, and you can apply the rules immediately.

---

### 📱 Mobile Networks

These presets simulate common mobile network experiences, from ideal 5G to legacy 3G.

| Preset Name | Rate (Bandwidth) | Delay (Latency) | Jitter | Loss (%) | Use Case |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **5G (Ideal)** | 100 mbit | 20 ms | 5 ms | 0% | Tests the ideal, low-latency performance of a 5G network. |
| **4G (Good)** | 25 mbit | 80 ms | 15 ms | 0.1% | Simulates a stable 4G/LTE connection in a good signal area. |
| **4G (Poor/Congested)** | 5 mbit | 150 ms | 50 ms | 1% | Simulates a 4G connection with a weak signal or high congestion (e.g., at a crowded event). |
| **Legacy (3G/Edge)** | 1 mbit | 400 ms | 100 ms | 3% | Simulates a very poor 3G or Edge connection. Excellent for testing timeouts and app resilience. |

---

### 🌍 Wi-Fi & WAN (Fixed/Global)

These presets simulate fixed-line broadband, satellite connections, and other global network conditions.

| Preset Name | Rate (Bandwidth) | Delay (Latency) | Jitter | Loss (%) | Use Case |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **Nationwide (Fiber)** | 50 mbit | 40 ms | 10 ms | 0% | A standard, reliable broadband/fiber connection within the same country. |
| **Oversea (Intercontinental)** | Unlimited | 120 ms | 10 ms | 0% | Simulates the base latency of a fast fiber connection between continents (e.g., Brazil to Europe). |
| **Satellite (LEO - Fast)** | 15 mbit | 80 ms | 30 ms | 0.5% | Simulates a good Low Earth Orbit (LEO) satellite connection, like Starlink. |
| **Satellite (GEO - Slow)** | 3 mbit | 600 ms | 200 ms | 1% | Simulates a traditional geostationary satellite. The extreme latency is the main test. |
| **Slow ADSL (Throttled)** | 512 kbit | 100 ms | 20 ms | 0.1% | Simulates a legacy ADSL line or a modern connection being throttled. Tests slow uploads/downloads. |

---

### 📉 Problematic Networks

These presets are designed to simulate specific *failure scenarios* to test your application's error handling.

| Preset Name | Rate (Bandwidth) | Delay (Latency) | Jitter | Loss (%) | Use Case |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **Unstable Wi-Fi (Congested)** | Unlimited | 40 ms | 20 ms | **2%** | Simulates a public Wi-Fi (e.g., airport, cafe). Bandwidth is high, but intermittent packet loss is the problem. |
| **Unstable Call (High Jitter)** | 10 mbit | 50 ms | **150 ms** | 1% | The focus is on extreme **Jitter**. Simulates a VoIP/Zoom call that "cuts out," "freezes," or has robotic audio. |
| **Bad Network (High Loss)** | 5 mbit | 100 ms | 50 ms | **8%** | A general stress test. Can your application survive, handle retries, and recover from a very unreliable network? |

## 5. Optional: Default Gateway Mode

You can run `netsim-in-a-box` as a shared network appliance that simulates conditions for other devices on your network (e.g., mobile phones, other developer machines).

When enabled, this mode automatically configures the container's host to:
1.  Enable IP Forwarding (`sysctl net.ipv4.ip_forward=1`).
2.  Auto-detect the host's WAN (default) interface.
3.  Apply `iptables` NAT (Masquerade) rules, turning the host into a simple router.

### How to Enable (Two Options)

#### Option 1: Standard Mode (Safe, Manual Firewall)

This mode is for users who manage their own firewall (like `ufw`) and do not want this tool to interfere with it.

1.  **Run the container:**
    ```bash
    docker run --rm -it \
      --cap-add=NET_ADMIN \
      --cap-add=NET_RAW \
      --net=host \
      -e DEFAULT_GATEWAY_MODE=true \
      netsim-in-a-box:latest
    ```
2.  **Manually Configure Your Host Firewall:**
This tool **will not** change your firewall rules. You *must* configure your host's firewall (e.g., `ufw`) to allow `FORWARD` traffic.

*Example for `ufw` (on the host):*
```bash
# Edit /etc/default/ufw and set:
DEFAULT_FORWARD_POLICY="ACCEPT"

# Then reload
sudo ufw disable && sudo ufw enable
```
    
#### Option 2: Easy Mode (Invasive, Auto-Firewall)

This mode is for users who want a simple "it just works" solution and agree to let this tool **disable the `ufw` firewall on the host machine**.

1.  **Run the container with *both* flags:**
    ```bash
    docker run --rm -it \
      --cap-add=NET_ADMIN \
      --cap-add=NET_RAW \
      --net=host \
      -e DEFAULT_GATEWAY_MODE=true \
      -e RECONFIGURE_FIREWALL=true \
      netsim-in-a-box:latest
    ```
2.  **What happens:**
When `RECONFIGURE_FIREWALL=true` is set, the container will detect if `ufw` is installed on the host and attempt to run `ufw disable`. This is an invasive action taken for convenience. **Do not use this flag if you have a complex firewall setup.**

## 6. Bonus Tool: iperf3 Server

This container also runs an `iperf3` server as a daemon, managed by `supervisord`. This helps you test bandwidth shaping without needing to run a separate server.

* **iperf3 Port:** `5202` (Note: *not* the default 5201)

### How to Test (Example)

1.  Apply an **INCOMING** rule of **10mbit** on your VM's interface.
2.  From *another physical host* (not the container host or same physical computer), run:
    ```bash
    iperf3 -c <netsim_vm_ip> -p 5202
    ```
3.  The client will report a throughput of ~10 Mbits/s.

## 7. Advanced: Raw Command Execution

NetSim-in-a-Box v4 includes a "raw" API endpoint for advanced users who need to inspect or manually modify the `tc` or `ip` settings.

This endpoint is a direct, unfiltered passthrough to the host's `tc` and `ip` binaries.

**Endpoint:** `/tc/api/v2/config/raw`
**Methods:** `POST`, `GET`

### ⚠️ Security Warning

This endpoint is powerful. It is **strictly limited** to only execute commands that begin with `tc` or `ip`. All other commands (like `ls`, `rm`, `cat`, etc.) will be rejected with a `403 Forbidden` error.

---

### Usage with `POST` (Recommended)

You can send the command as plain text in the request body.

**Example: Get `tc` qdisc status for `ens33`**
```bash
# Note: The command is in the request body
curl -X POST --data "tc qdisc show dev ens33" http://localhost:2023/tc/api/v2/config/raw

# Example Success Output:
# {
#   "status": "ok",
#   "output": "qdisc htb 1: root refcnt 2 r2q 10 default 11 direct_qlen 1000\nqdisc netem 10: parent 1:11 limit 1000 delay 500ms\n"
# }
```

### Usage with `GET`

**Example: Get `ip` address for `ens33`**
```bash
# The command is "ip addr show dev ens33"
curl -G http://localhost:2023/tc/api/v2/config/raw --data-urlencode "cmd=ip addr show dev ens33"

# Example Success Output:
# {
#   "status": "ok",
#   "output": "2: ens33: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc htb state UP group default qlen 1000\n    link/ether 00:0c:29:12:34:56 brd ff:ff:ff:ff:ff:ff\n    inet 192.168.0.187/24 brd 192.168.0.255 scope global dynamic noprefixroute ens33\n       valid_lft 2814sec preferred_lft 2814sec\n    inet6 fe80::20c:29ff:fe12:3456/64 scope link noprefixroute \n       valid_lft forever preferred_lft forever\n"
# }
```

## 8. Known Limitations

* **Linux Only:** This tool is 100% dependent on Linux kernel modules (ifb, sch_htb, netem) and the iproute2 (tc) utility. It will not have full capabilities on macOS or native Windows in case you try to run without `docker`.

* **WSL2 Not Supported:** This tool is not supported on WSL2. WSL2 uses kernel that does not have networking shaping modules (NETEM) installed that is directly needed by `tc`. Use a dedicated Linux VM (e.g., in VirtualBox, VMWare) or a bare-metal Linux host.

* **Rootless Docker:** This will not work with rootless Docker, as it requires elevated NET_ADMIN capabilities on the host's network stack and **uid 0** for `tc` and `iptables`.

* **Host-to-Guest Traffic:** Testing `INCOMING` rules by sending traffic from the host machine (e.g., Windows) to the guest VM (Linux) will likely fail. Hypervisors use an optimized "fast path" that bypasses the `ingress` qdisc. To test `INCOMING` rules, you must send traffic from a separate machine (another VM or a device on the network) or from the internet (e.g., `curl` an external site).

## 9. 🗺️ Roadmap: v5.0 (Major Re-architecture) 

This release focuses on re-architecting the core logic to support advanced, simultaneous simulation scenarios.

* **1. Enable Simultaneous INCOMING and OUTGOING Rules**
    * **What:** Refactor the core logic to allow applying *both* an incoming rule (on `ifb0`) and an outgoing rule (on the `root` of the main interface) at the same time.
    * **Why:** This is the single biggest limitation of our current tool. Right now, applying one rule type (e.g., incoming) automatically deletes the other.

* **2. Add Advanced Filtering (Per-IP/Port Rules)**
    * **What:** Move beyond our simple "API vs. Everything Else" filter. Allow users to create a dynamic list of rules (e.g., "traffic to this IP gets 10% loss," "traffic to port 5432 gets 200ms delay").
    * 
    * **Why:** This elevates the tool from a "general" simulator to a "surgical" one for power users.    
  
**💡 These are potential nice features to be worked on if needed:**

* * **Save/Load Profiles:** Let users save their custom simulation settings to a shareable link or file.
* * **Live Stats Dashboard:** Create a new API endpoint that polls `tc -s qdisc show ...` to display real-time dropped/delayed packet counts in the UI.
