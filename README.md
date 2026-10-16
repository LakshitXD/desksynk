# desksynk

desksynk shares one keyboard and mouse from a Linux PC running Hyprland with a Mac. Push the pointer off the edge of the Linux screen and it appears on the Mac. Push it back and it returns.

It covers the part of [Deskflow](https://github.com/deskflow/deskflow) that a two-machine desk needs, in about 1,900 lines of Go with one dependency.

## How it differs from Deskflow

- **No lag spikes when AirDrop is on.** AirDrop keeps the Mac's AWDL interface active, and AWDL takes the Wi-Fi radio off channel for short periods. Deskflow queues every pointer event, so after each pause the Mac replays the backlog and the pointer drags behind your hand. desksynk keeps at most two pointer updates in flight. The Mac confirms each one after applying it, and any motion that arrives meanwhile is summed into the next update. Packets never queue in the router or the kernel, so after a pause the pointer goes straight to where it belongs. Keys and clicks are never held back and still arrive in order. While the Mac is active, the server logs the measured round trip every 2 seconds (`link: ... round trip median ...`). desksynk also marks its packets as interactive (DSCP EF), so Wi-Fi sends them ahead of bulk traffic, and it connects over IPv4 only, so a `.local` name never resolves to a link-local address on the AirDrop interface.
- **Built for Wayland on Hyprland.** The server reads keyboards and mice from `/dev/input` and takes exclusive control of them while the Mac is active. It reads the cursor position through Hyprland's IPC socket. It needs no portals, no X11, and no libei.
- **No stuck keys.** The server does not switch screens while a key or button is held. The Mac releases everything it holds when control returns or the connection drops.
- **Encrypted and paired.** Traffic uses TLS 1.3. Each side pins the other's certificate on first connect and rejects any other machine afterwards.
- **No GUI and no config file.** Everything is set with flags.

Clipboard text syncs when the pointer crosses between screens, the same way Synergy does it. Only plain text syncs.

## Install

Build both binaries on the Linux machine. The Mac binary needs no cgo, so it cross-compiles:

```bash
make all
```

This writes `dist/desksynk-linux-amd64`, `dist/desksynk-macos-arm64` (Apple silicon), and `dist/desksynk-macos-amd64` (Intel).

### On Linux (the machine with the keyboard and mouse)

1. Make sure your user is in the `input` group, so it can read `/dev/input/event*`. To check, run `id`. To add it, run `sudo usermod -aG input $USER` and log in again.
2. Install `wl-clipboard` for clipboard sync.
3. Start the server. Set `--mac-side` to the side of your Linux screens where the Mac sits:

   ```bash
   ./dist/desksynk-linux-amd64 server --mac-side left
   ```

   The server prints its listening address and certificate fingerprint.

4. Allow TCP port 24810 through your firewall if one is enabled.

To run the server at login, copy the binary to `~/.local/bin/desksynk`, copy `contrib/desksynk.service` to `~/.config/systemd/user/`, edit `--mac-side`, then run `systemctl --user enable --now desksynk`.

### On the Mac

1. Copy the right binary to the Mac, for example to `/usr/local/bin/desksynk`.
2. Remove the quarantine flag, because the binary is unsigned:

   ```bash
   xattr -d com.apple.quarantine /usr/local/bin/desksynk
   ```

3. Start the client with the Linux machine's IP address:

   ```bash
   desksynk client 192.168.1.14
   ```

4. The first run prints a warning about Accessibility permission. Open **System Settings > Privacy & Security > Accessibility**, add the app that runs desksynk (Terminal, iTerm, or the binary itself under launchd), then restart the client.

To run the client at login, edit the IP address in `contrib/com.desksynk.client.plist`, copy it to `~/Library/LaunchAgents/`, then run `launchctl load ~/Library/LaunchAgents/com.desksynk.client.plist`. Under launchd, grant Accessibility to `/usr/local/bin/desksynk` itself.

## Use it

- Move the pointer off the `--mac-side` edge of your Linux desktop to control the Mac. Move it back across the same edge to return.
- Press and release Ctrl+Alt+Shift together to switch screens without the mouse. This also works as an escape hatch if the Mac stops responding.
- If the connection drops while the Mac is active, the server gives the keyboard and mouse back to Linux at once.

## Flags

`desksynk server`:

| Flag | Default | Meaning |
| --- | --- | --- |
| `--mac-side` | `left` | Side of the Linux desktop where the Mac sits: `left`, `right`, `top`, `bottom`. |
| `--listen` | `0.0.0.0:24810` | Address and port to listen on. |
| `--no-clipboard` | off | Don't sync clipboard text. |

`desksynk client [flags] HOST[:PORT]`:

| Flag | Default | Meaning |
| --- | --- | --- |
| `--speed` | `1.0` | Pointer speed on the Mac. One mouse count moves one point. |
| `--natural-scroll` | off | Reverse the scroll direction. |
| `--alt-is-cmd` | off | Make Alt act as Command and Super as Option, matching Mac key positions. By default Super is Command and Alt is Option. |
| `--no-clipboard` | off | Don't sync clipboard text. |

## Pairing and re-pairing

The first client to connect pairs with the server, and both sides save each other's fingerprint. Linux stores it in `~/.config/desksynk/` and macOS in `~/Library/Application Support/desksynk/`. To pair a new Mac, or after reinstalling either side, delete `client.pin` on Linux or `server.pin` on the Mac. The log names the file to delete when a fingerprint does not match.

## Limits

- The server supports Hyprland only, because the cursor position comes from Hyprland IPC. Hyprland 0.55+ Lua configs and older hyprlang configs both work.
- The Mac can only receive. It can't share its own keyboard and mouse back.
- Touchpads on the Linux side are not forwarded. Mice, including high-resolution scroll wheels, are.
- Media keys (volume, mute) arrive on the Mac as plain keycodes, and macOS ignores them.
- macOS pointer acceleration does not apply to forwarded motion. Tune it with `--speed`.

## Development

```bash
make test   # unit tests
make e2e    # needs a live Hyprland session and write access to /dev/uinput
```

The end-to-end test creates a virtual uinput mouse and runs the real server against your Hyprland session. It pushes the pointer past the left edge, checks that every motion count reaches the client and that the local pointer stays frozen, then sends `Leave` and checks that the pointer lands at the right spot on the edge and local input works again. Only the virtual mouse is grabbed.

The protocol lives in `internal/proto`. Each message is a 1-byte type, a 4-byte length, and a payload, sent over TLS.
