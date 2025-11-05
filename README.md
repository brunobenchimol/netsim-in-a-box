# NetSim-in-a-Box

**A self-contained, Docker-based network simulation tool for developers.**

This tool provides a simple web interface (`tc-ui`) and a proxy (`squid`) in a single container. It allows development teams to easily simulate adverse network conditions (like high latency, packet loss, and bandwidth throttling) on their local machines by manipulating the host's Linux traffic control (`tc`) settings. 

This is a fork of the original (and now unmaintained) `ossrs/tc-ui` (https://github.com/ossrs/tc-ui), rebuilt on a modern Go backend, a new lightweight Javascript/Tailwind frontend, and packaged with Squid proxy in a secure, multi-stage Docker image.

## How it Works: The Architecture

This tool runs as a single, monolithic Docker container managed by `supervisord`. It contains two key services:

1.  **`tc-ui` (Go App):** The backend API and web frontend, exposed on port `2023`. This service runs the `tcset`, `tcdel`, etc., commands based on your UI input.
2.  **`squid` (Proxy):** A non-caching proxy server, exposed on port `3128`.

To function, the container **must** run with `--net=host`. This gives the `tc-ui` process permission to see and apply `tc` rules directly to your host's real network interfaces (e.g., `ens33`, `docker0`).

The developer then routes their application's traffic through the `squid` proxy (`localhost:3128`), and `tc-ui` applies the network rules to the host interface that traffic is using.

---

## 1. Host Prerequisites (Important)

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

## 2. Build the Image
```bash
docker build -t netsim-in-a-box .
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

## 6. Known Limitations

* **Linux Only:** This tool is 100% dependent on Linux kernel modules (ifb, sch_htb, netem) and the iproute2 (tc) utility. It will not have full capabilities on macOS or native Windows in case you try to run without `docker`.

* **WSL2 Not Supported:** This tool is not supported on WSL2. WSL2 uses kernel that does not have networking shaping modules (NETEM) installed that is directly needed by `tc`. Use a dedicated Linux VM (e.g., in VirtualBox, VMWare) or a bare-metal Linux host.

* **Rootless Docker:** This will not work with rootless Docker, as it requires elevated NET_ADMIN capabilities on the host's network stack and **uid 0** for `tc`, `iptables` and `tcpdump`.

## 7. TODO

1. Remove `tcconfig` 
2. Remove `go-oryx-lib` 
