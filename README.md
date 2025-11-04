# netsim-in-a-box

Fork from: https://github.com/ossrs/tc-ui

## Build Docker Image
```bash
docker build -t netsim-in-a-box .
```

## Running Image

```bash
# On Terminal Host/Server (not inside Docker)
sudo modprobe ifb
```

**Method 1: Correct and Safe (Granular Capabilities)**

```bash
docker run --rm -it \
--cap-add=NET_ADMIN \
--cap-add=NET_RAW \
-p 2023:2023 \
netsim-in-a-box:latest
```

**Method 2: Big Hammer (`--privileged`)**

```bash
docker run --rm -it \
--privileged \
-p 2023:2023 \
netsim-in-a-box:latest
```

**Method 3: Unpriveleged (should fail if docker not running as root)**

```bash
docker run --rm -it \
-p 2023:2023 \
netsim-in-a-box:latest
```

## Debugging inside Container

```bash
docker run --entrypoint /bin/bash \
--rm -it \
-p 2023:2023 \
netsim-in-a-box:latest
```


