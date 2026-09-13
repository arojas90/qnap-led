# qnap-led

Front-panel LCD, backlight, and button control for a QNAP TS-670 Pro NAS
running **Ubuntu Server** (instead of stock QTS), plus a daemon that shows
rotating system info on the display.

## Hardware background

Hardware: QNAP TS-670 Pro (6-bay, Intel Core i3-3220). The stock QNAP firmware
controls the front LCD/LEDs via a closed HAL (`libuLinux_hal.so` +
`hal_daemon` + `hal_app`), which isn't available under Ubuntu. Instead, the
front panel's microcontroller (an ICP "A125" board, same family used in
several QNAP models like the TS-453 Pro) is talked to directly over an
internal serial port. This has been confirmed working on this exact unit.

### Storage layout (for context, unrelated to the LCD but useful background)

- 6x WD4003FZEX 4TB drives, previously used under QTS/mdadm, now wiped and
  used as a ZFS **stripe pool** named `tank` (no redundancy — chosen
  deliberately for max capacity, ~21.8 TiB raw/usable since there's no
  parity).
- `/dev/sdg` (USB-attached Kingston SA400S3 SSD) is the Ubuntu system disk,
  LVM (`ubuntu-vg/ubuntu-lv`).
- Docker Compose is the deployment pattern used on this NAS/homelab
  generally.

## Wire protocol

### Serial port

- Device: **`/dev/ttyS1`**
- Baud rate: **1200** (confirmed correct — this is genuinely a very slow
  link)
- Must be root or in the `dialout` group to access it.

```bash
stty -F /dev/ttyS1 1200
```

### Writing to the LCD (2 rows x 16 columns)

Packet format per line:

```
0x4D 0x0C <line_index> 0x20 <16 ASCII chars, space-padded>
```

- `0x4D` = command prefix ("M")
- `0x0C` = "write line" opcode
- `line_index` = `0x00` for row 1, `0x01` for row 2
- `0x20` = a required 4th header byte (easy to miss — omitting it shifts the
  whole payload by one byte and produces garbled/garbage characters after
  the text, since the LCD firmware then misreads the first text byte as
  part of the header)
- Text must be **exactly 16 bytes**, padded with spaces (`str.ljust(16)` /
  `"%-16s"`), or leftover characters from whatever was previously in that
  row's LCD memory will show through.

**Critical gotcha:** don't build this packet with shell `echo`/`printf` and
pipe it as text — bash/coreutils mangle or drop the NUL byte (`\x00`, used
as `line_index` for row 1), which desyncs the whole packet. Write raw bytes
from a real program instead, opening the device in binary mode.

### Backlight control

```
0x4D 0x5E 0x00   -> backlight OFF
0x4D 0x5E 0x01   -> backlight ON
```

### Reading the two front buttons (ENTER / SELECT)

The same `/dev/ttyS1` port emits 4-byte packets when a button is pressed or
released. Confirmed empirically on this unit:

| Packet (hex)   | Meaning                            |
|----------------|-------------------------------------|
| `53 05 00 01`  | ENTER pressed                       |
| `53 05 00 02`  | SELECT pressed                      |
| `53 05 00 00`  | released / idle (no active press)   |

In idle state (no button touched) the line is essentially silent (occasional
single stray `53` byte at most).

Reading and writing both happen on the same serial fd, so the daemon
interleaves a background goroutine doing blocking reads (for buttons) with
the main loop doing periodic writes (for display updates), synchronized
with a mutex around the shared serial connection. See
[`internal/panel/panel.go`](internal/panel/panel.go).

## Not yet explored / open questions

- Individual disk-bay LEDs, STATUS LED, USB LED: **not investigated yet** on
  this unit. On the sibling TS-453 Pro (see `qnapctl` below) these are
  toggled via raw I/O port writes rather than the serial port — likely
  analogous here but unconfirmed.

## Reference prior art

- Gist documenting the A125 board protocol on a TS-453 Pro:
  https://gist.github.com/zopieux/0b38fe1c3cd49039c98d5612ca84a045
- `qnapctl` — C++/Qt daemon + DBus API for LCD, buttons, and LEDs on
  TS-453 Pro (same A125 panel family): https://github.com/Zopieux/qnapctl
- `qnapdisplay` — Python module for reading/writing on various QNAP models
  (TS-453A, TS-459, TVS-872X): https://github.com/bkram/qnapdisplay
- `lcdproc`'s `icp_a106`/`icp_a125` driver (LCDproc project) implements the
  same wire protocol generically.

## This project

A single statically-linked Go binary (`qnap-led`) that:

- Rotates through info screens on the LCD, refreshing every few seconds:
  IP address, network throughput, CPU usage, CPU temperature, RAM usage,
  swap usage, uptime, load average, running Docker containers, OS/system
  disk usage and temperature, `tank` ZFS pool capacity, pool health
  (ONLINE/DEGRADED/...), pool I/O throughput, drive temperature summary
  (avg/max via smartctl), and the single hottest drive.
- **SELECT** advances to the next screen.
- **ENTER** toggles the backlight on/off.
- Optionally exposes the backlight as a Home Assistant switch entity over
  MQTT (see below) — physical ENTER presses and HA commands stay in sync
  either way.
- Optionally serves a web UI (`--web-addr`) for adding your own screens,
  each backed by a shell command you type in — no rebuild needed.

Source layout:

- [`cmd/qnap-led/main.go`](cmd/qnap-led/main.go) — CLI, screen rotation, button/MQTT wiring
- [`internal/panel`](internal/panel/panel.go) — the serial wire protocol
- [`internal/screens`](internal/screens/screens.go) — the info screens
- [`internal/haswitch`](internal/haswitch/haswitch.go) — MQTT + Home Assistant Discovery for the backlight switch
- [`internal/customscreens`](internal/customscreens/customscreens.go) — user-defined screens backed by shell commands, persisted to disk
- [`internal/webui`](internal/webui/webui.go) — web UI for adding/removing custom screens

[`.github/workflows/ci.yml`](.github/workflows/ci.yml) builds, vets, and
tests every push/PR. [`.github/workflows/release.yml`](.github/workflows/release.yml)
builds a static `linux/amd64` binary and publishes it to
[GitHub Releases](https://github.com/arojas90/qnap-led/releases) whenever a
`vX.Y.Z` tag is pushed:

```bash
git tag v0.1.0
git push origin v0.1.0
```

### Install on the NAS

#### 1. Get the binary

**Option A — download the prebuilt release (recommended):** every tag push
(`vX.Y.Z`) builds a static `linux/amd64` binary via CI and attaches it to a
[GitHub Release](https://github.com/arojas90/qnap-led/releases). On the NAS:

```bash
curl -LO https://github.com/arojas90/qnap-led/releases/latest/download/qnap-led
curl -LO https://github.com/arojas90/qnap-led/releases/latest/download/qnap-led.sha256
sha256sum -c qnap-led.sha256
chmod +x qnap-led
```

**Option B — build from source** (requires Go 1.21+, produces a single
static binary with no runtime dependencies):

```bash
CGO_ENABLED=0 go build -o qnap-led ./cmd/qnap-led
```

#### 2. Install the binary, config, and service

```bash
sudo cp qnap-led /usr/local/bin/qnap-led
sudo mkdir -p /etc/qnap-led
sudo cp config.example.yaml /etc/qnap-led/config.yaml
sudo "$EDITOR" /etc/qnap-led/config.yaml   # set pool, mqtt, web, etc.
sudo cp systemd/qnap-led.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now qnap-led.service
sudo journalctl -u qnap-led -f             # watch it start up
```

See [`systemd/qnap-led.service`](systemd/qnap-led.service). The unit runs
as `root`, which the serial port, `smartctl`, and `docker` all need access
to anyway (`dialout` group, raw disk access, `docker` group respectively) —
running as root sidesteps configuring all three separately.

Screens that shell out to `zpool`, `smartctl`, or `docker` degrade to
"unavailable" if that binary isn't installed or reachable — nothing crashes
if e.g. `smartmontools` isn't present.

### Configuration

qnap-led reads an optional YAML file (`--config`, default
`/etc/qnap-led/config.yaml`) and layers CLI flags on top as overrides — a
flag always wins over the file, and the file always wins over built-in
defaults. Neither is required: flags alone work exactly as before, and the
file alone works fine with the daemon started with no flags at all (as the
provided systemd unit does).

Start from [`config.example.yaml`](config.example.yaml), which documents
every field with inline comments. Its shape:

```yaml
port: /dev/ttyS1
baud: 1200
pool: tank
os_disk_device: sdg
refresh: 5s
custom_screens_file: /etc/qnap-led/custom_screens.json

mqtt:
  broker: "tcp://localhost:1883"
  username: "qnap-led"
  password: "change-me"

web:
  addr: "127.0.0.1:8080"
```

**Features that depend on external configuration are opt-in by presence:**
the `mqtt` and `web` sections do nothing unless you actually fill in
`broker` / `addr` — leave them out (or empty) and those features stay off,
exactly as if `--mqtt-broker`/`--web-addr` were never passed. There is no
separate enable/disable switch to flip; setting the value *is* enabling it.

### Flags

| Flag           | Default        | Purpose                                          |
|----------------|----------------|---------------------------------------------------|
| `--config`     | `/etc/qnap-led/config.yaml` | Path to the YAML config file (missing file is not an error) |
| `--port`       | `/dev/ttyS1`   | Serial device for the front panel                |
| `--baud`       | `1200`         | Serial baud rate                                 |
| `--pool`       | `tank`         | ZFS pool name to report on                       |
| `--os-disk-device` | `sdg`      | Block device (no `/dev/` prefix) to read OS disk temperature from |
| `--refresh`    | `5s`           | How often to refresh the current screen          |
| `--mqtt-broker`| *(empty)*      | MQTT broker URL, e.g. `tcp://localhost:1883` — enables Home Assistant control when set |
| `--mqtt-user`  | *(empty)*      | MQTT username                                    |
| `--mqtt-pass`  | *(empty)*      | MQTT password                                    |
| `--web-addr`   | *(empty)*      | Address for the custom-screens web UI, e.g. `127.0.0.1:8080` — enables it when set |
| `--custom-screens-file` | `/etc/qnap-led/custom_screens.json` | Where screens added via the web UI are persisted |

Every flag above overrides its config-file counterpart when passed
explicitly (`--config` is flag-only, it has no file counterpart).

### Home Assistant integration

Setting `mqtt.broker` in the config file (or passing `--mqtt-broker`)
connects to your MQTT broker (e.g. Mosquitto) and publishes a Home
Assistant **MQTT Discovery** config for a switch entity named "QNAP LCD
Backlight" — it shows up in Home Assistant automatically, no YAML needed on
the HA side. Turning it on/off in HA toggles the real backlight, and
pressing the physical ENTER button updates the HA entity's state to match.

Scope is intentionally limited to backlight on/off — screen rotation and
sensor data (temps, IP, pool capacity) are not exposed to HA, only shown on
the physical LCD.

### Custom screens (web UI)

Setting `web.addr` in the config file (or passing `--web-addr
127.0.0.1:8080`) starts a small web UI (visit `http://127.0.0.1:8080/` from
the NAS, or over an SSH tunnel from elsewhere) where you can add screens
without touching code: give it a name and a shell command, and it joins the
SELECT rotation alongside the built-in screens. Screens are persisted to
`custom_screens_file` and reloaded on restart.

Rendering rule: if the command prints one line, it becomes the LCD's second
row with your Name as the label on the first row (e.g. Name `Backups`,
Command `systemctl is-active restic-backup` → `BACKUPS` / `active`). If it
prints two or more lines, the first two are used directly for both rows,
and Name is only used to identify the screen in the list.

**Security note:** this is arbitrary shell command execution, run by
whatever user the daemon runs as — root, under the provided systemd unit.
The web UI has no authentication. **Never set `web.addr` to anything other
than `127.0.0.1:<port>` unless you put a reverse proxy with auth in front
of it and restrict it to a trusted network** — reach it remotely via an SSH
tunnel (`ssh -L 8080:localhost:8080 nas`) instead of exposing it directly.
