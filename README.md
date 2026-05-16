# xmcam-local 🚀

**xmcam-local** is a lightweight, cross-platform replacement for the Windows-only `VideoPlayTool` utility. This project allows you to fully utilize the original Web interface of XM-based (XMEye) cameras in any modern browser on **macOS, Linux, and Windows** without installing suspicious native `.exe` plugins.

## 💡 Motivation

The original Web UI of XM cameras requires a local proxy server called `VideoPlayTool.exe`. This tool is only available for Windows, making it nearly impossible to configure these cameras from macOS or Linux without using virtual machines.

**xmcam-local** implements all necessary HTTP and WebSocket endpoints in Go, mimicking the presence of the original plugin. This "tricks" the camera's JavaScript code, allowing you to access settings, change encoding parameters (Resolution, FPS), configure network settings, and manage motion detection on any operating system.

## ✅ Current Status (Implemented)

The project currently serves as a fully functional **Local Proxy Mock** that tricks the XM Web UI into bypassing plugin checks and allowing full configuration access.

- **Plugin Detection:** Mocked `GET /Cfg/tip.png` to trigger UI activation.
- **Local Control Gateway:** Full implementation of `POST /Cmd-WebLocalCtrl` with support for `MainType` 1 (Version) and 99 (Port negotiation).
- **WebSocket Synchronization:** Active `/VideoPlay` channel with strict `MsgID` tracking and successful command response (`Ret: 100`).
- **Dynamic Asset Routing:** Transparent handling of `WebStyle_XXXX` URL prefixes and double-slash path corrections.
- **Device Emulation:** Mocked responses for `SystemFunction`, `General.General`, `General.Location`, and `OPTimeQuery` to ensure stable UI initialization.
- **Error Handling:** Implemented fallback codes (e.g., `Ret: 1020` for `OPLocalSearch`) to force the UI to use native camera CGI instead of failing on missing plugin SDK features.

## 🚀 Upcoming / TODO

- [ ] **Video Rendering:** Real-time stream ingestion (RTSP) and delivery via **WebRTC (Pion)** or FFmpeg.
- [ ] **CGI Proxying:** Transparently proxying `/cgi-bin/` requests to the camera IP to support cameras with non-standard initialization flows.
- [ ] **Embedded Assets:** Use Go `embed.FS` to bundle the `Web/` folder directly into the binary for single-file distribution.
- [ ] **Multi-Camera Management:** Intelligent session handling for setups with multiple DVRs/IPCs.
- [ ] **Automatic Extraction:** Integrate the asset extraction script directly into the Go application startup.

## 🛠 Getting Started

### 1. Prerequisites
- **Go** 1.22 or newer.
- **7-Zip** (on macOS: `brew install sevenzip`) — required to extract frontend files.

### 2. Prepare the Web Folder
Since the interface files are the intellectual property of the manufacturer, they are not included in this repository. Use the provided script to automatically extract them from the official installer:

```bash
./extract_web_assets.sh
```

### 3. Run the Server
```bash
# Install dependencies
go mod tidy

# Start the server
go run main.go
```
The server will start listening on `127.0.0.1:54455`.

## 🖥 Usage

1. Start `xmcam-local`.
2. Open your browser and navigate to your camera's IP address (e.g., `http://192.168.1.10/`).
3. You will see the standard login screen **without** any prompts to install a plugin.
4. Enter your password and access all configuration features.

## 🧩 System Integration (Optional)

To make **xmcam-local** behave like the original utility, you can register it as a protocol handler for `videoplaytool://`. This allows the browser to launch the server automatically when you click the "Download/Run Tool" link in the camera interface.

### macOS

The easiest way to register the protocol on macOS is via a minimal AppleScript wrapper:
1. Open **Script Editor**.
2. Paste the following:
   ```applescript
   on open location this_URL
       do shell script "open -a Terminal.app /full/path/to/xmcam-local"
   end open location
   ```
3. Export as an **Application** named `XMProxy.app`.
4. Right-click `XMProxy.app` -> Show Package Contents -> `Contents/Info.plist`.
5. Add the protocol registration to the XML:
   ```xml
   <key>CFBundleURLTypes</key>
   <array>
     <dict>
       <key>CFBundleURLName</key>
       <string>VideoPlayTool Protocol</string>
       <key>CFBundleURLSchemes</key>
       <array>
         <string>videoplaytool</string>
       </array>
     </dict>
   </array>
   ```

### Windows

Create a file named `register_protocol.reg` and run it (adjust the path to your `xmcam-local.exe`):

```reg
Windows Registry Editor Version 5.00

[HKEY_CLASSES_ROOT\videoplaytool]
@="URL:VideoPlayTool Protocol"
"URL Protocol"=""

[HKEY_CLASSES_ROOT\videoplaytool\shell\open\command]
@="\"C:\\path\\to\\xmcam-local.exe\" \"%1\""
```

---
**Disclaimer:** *This project is for educational and interoperability purposes only. All trademarks belong to their respective owners. We do not distribute original XM Web interface files.*
