(function () {
    console.log("[xmcam-local] Initializing HTML5 Video Player Shim (multi-window grid, H.264 & H.265)...");

    var PROXY_BASE = "http://127.0.0.1:54455";

    // ---- layout helpers -------------------------------------------------
    // Split mode -> number of windows (mirrors class.js GetWndShowMode).
    var MODE_WINDOWS = [1, 4, 9, 16, 25, 36, 64];
    var GRID_SIZES = [1, 4, 9, 16, 25, 36, 64];

    function winCountForMode(mode) {
        return MODE_WINDOWS[mode] || 1;
    }
    function winCountForCount(count) {
        for (var i = 0; i < GRID_SIZES.length; i++) {
            if (GRID_SIZES[i] >= count) return GRID_SIZES[i];
        }
        return 64;
    }
    function colsFor(winCount) {
        return Math.ceil(Math.sqrt(winCount));
    }

    // ---- global state ---------------------------------------------------
    // window.xmGridWinCount : current number of grid cells
    // window.xmChannels     : { channel: { video, jmuxer, codec } }
    if (typeof window.xmGridWinCount !== "number") window.xmGridWinCount = 1;
    if (!window.xmChannels) window.xmChannels = {};
    // Which stream (0=main, 1=sub) we currently expect per channel. Frames from a
    // different stream are stale (in-flight from before a switch) and get dropped,
    // so a leftover sub frame can't wedge a player that then receives main frames.
    if (!window.xmExpectedStream) window.xmExpectedStream = {};

    var activeWS = null;

    function injectVideoStyles() {
        if (document.getElementById("xm_video_styles")) return;
        var style = document.createElement("style");
        style.id = "xm_video_styles";
        style.textContent = `
            .xm_grid {
                display: grid !important;
                width: 100% !important;
                height: 100% !important;
                gap: 2px !important;
                margin: 0 !important;
                padding: 0 !important;
                background-color: #000 !important;
                box-sizing: border-box !important;
                overflow: hidden !important;
                min-height: 0 !important;
            }
            .xm_cell {
                position: relative !important;
                width: 100% !important;
                height: 100% !important;
                background-color: #000 !important;
                overflow: hidden !important;
                min-width: 0 !important;
                min-height: 0 !important;
            }
            .xm_cell video {
                position: absolute !important;
                top: 0 !important;
                left: 0 !important;
                width: 100% !important;
                height: 100% !important;
                object-fit: contain !important;
                object-position: center center !important;
                background-color: #000 !important;
                margin: 0 !important;
                padding: 0 !important;
                border: 0 !important;
                display: block !important;
            }
        `;
        document.head.appendChild(style);
    }

    function loadScript(url, callback) {
        if (typeof JMuxer !== "undefined") {
            if (callback) callback();
            return;
        }
        var script = document.createElement("script");
        script.src = url;
        script.onload = callback;
        script.onerror = function () {
            console.error("[xmcam-local] Failed to load jmuxer script from", url);
        };
        document.head.appendChild(script);
    }

    loadScript(PROXY_BASE + "/jmuxer.min.js", function () {
        console.log("[xmcam-local] jmuxer loaded successfully from " + PROXY_BASE);
    });

    function findContainer() {
        return document.getElementById("ipcplugin") ||
               document.getElementById("webplugins") ||
               document.getElementById("live");
    }

    function destroyChannel(ch) {
        var c = window.xmChannels[ch];
        if (!c) return;
        if (c.jmuxer) {
            try { c.jmuxer.destroy(); } catch (e) {}
        }
        if (c.video) {
            try { c.video.pause(); } catch (e) {}
            if (c.video.parentNode) c.video.parentNode.removeChild(c.video);
        }
        delete window.xmChannels[ch];
    }

    function destroyAllChannels() {
        for (var ch in window.xmChannels) {
            if (window.xmChannels.hasOwnProperty(ch)) destroyChannel(ch);
        }
        window.xmChannels = {};
    }

    // Build (or rebuild) the grid of empty cells. Destroys all per-channel players;
    // frames still arriving from the server repopulate the cells automatically.
    function setupGrid(winCount) {
        injectVideoStyles();
        var container = findContainer();
        if (!container) return false;

        if (winCount < 1) winCount = 1;
        window.xmGridWinCount = winCount;
        destroyAllChannels();

        container.style.setProperty("height", "100%", "important");
        container.style.setProperty("display", "grid", "important");
        container.classList.add("xm_grid");

        var cols = colsFor(winCount);
        var rows = Math.ceil(winCount / cols);
        // minmax(0, 1fr) lets tracks shrink below content size, preventing the grid
        // from growing taller than the container (which clips the bottom of the video).
        container.style.setProperty("grid-template-columns", "repeat(" + cols + ", minmax(0, 1fr))", "important");
        container.style.setProperty("grid-template-rows", "repeat(" + rows + ", minmax(0, 1fr))", "important");

        container.innerHTML = "";
        window.xmCells = [];
        for (var i = 0; i < winCount; i++) {
            var cell = document.createElement("div");
            cell.className = "xm_cell";
            cell.id = "xm_cell_" + i;
            container.appendChild(cell);
            window.xmCells.push(cell);
        }
        return true;
    }

    function ensureGrid() {
        var container = findContainer();
        if (!container) return false;
        if (!container.classList.contains("xm_grid") ||
            !window.xmCells || window.xmCells.length !== window.xmGridWinCount) {
            return setupGrid(window.xmGridWinCount || 1);
        }
        return true;
    }

    function cellForChannel(ch) {
        if (!window.xmCells || window.xmCells.length === 0) return null;
        var idx = ((ch % window.xmGridWinCount) + window.xmGridWinCount) % window.xmGridWinCount;
        return window.xmCells[idx] || window.xmCells[0];
    }

    // Ensure a <video> + JMuxer exist for a channel with the right codec/stream.
    function ensureChannelPlayer(ch, codec, stream) {
        if (!ensureGrid()) return null;

        var c = window.xmChannels[ch];
        var needRebuild = !c || !c.video || !c.video.isConnected ||
                          c.codec !== codec || c.stream !== stream;

        if (needRebuild) {
            destroyChannel(ch);
            var cell = cellForChannel(ch);
            if (!cell) return null;
            cell.innerHTML = "";

            var video = document.createElement("video");
            video.id = "xm_video_ch" + ch;
            video.autoplay = true;
            video.muted = true;
            video.playsInline = true;
            video.setAttribute("playsinline", "");
            var tryPlay = function () {
                if (video.paused) video.play().catch(function () {});
            };
            video.addEventListener("loadedmetadata", tryPlay);
            video.addEventListener("loadeddata", tryPlay);
            video.addEventListener("canplay", tryPlay);
            cell.appendChild(video);

            var jmuxer = null;
            if (typeof JMuxer !== "undefined") {
                jmuxer = new JMuxer({
                    node: video.id,
                    mode: "video",
                    videoCodec: codec || "H264",
                    flushingTime: 50,
                    fps: 25,
                    debug: false,
                    onReady: function () {
                        if (video.paused) video.play().catch(function () {});
                    }
                });
                console.log("[xmcam-local] JMuxer ready for channel " + ch + " codec " + codec);
            }
            c = { video: video, jmuxer: jmuxer, codec: codec, stream: stream };
            window.xmChannels[ch] = c;
        }
        return c;
    }

    function feedVideoFrame(uint8Arr) {
        if (!uint8Arr || uint8Arr.length < 4) return;
        var channel = uint8Arr[0];
        var stream = uint8Arr[1];
        var codecByte = uint8Arr[2];
        var frameData = uint8Arr.subarray(3);
        var codec = (codecByte === 2) ? "H265" : "H264";

        // Drop frames from a stream we're no longer showing (stale, in-flight after
        // a main<->sub switch) so they can't wedge the player at the wrong resolution.
        var expected = window.xmExpectedStream[channel];
        if (expected !== undefined && stream !== expected) return;

        var c = ensureChannelPlayer(channel, codec, stream);
        if (!c || !c.jmuxer) return;

        try {
            c.jmuxer.feed({ video: frameData });
            if (c.video && c.video.paused) c.video.play().catch(function () {});
        } catch (e) {
            console.error("[xmcam-local] Error feeding frame (ch " + channel + "):", e);
            destroyChannel(channel);
        }
    }

    // ---- preview command reactions (driven by intercepted WS messages) --
    function setExpectedRange(count, stream) {
        for (var i = 0; i < count; i++) window.xmExpectedStream[i] = stream;
    }
    function onPreviewStart(channel, stream) {
        stream = stream || 0;
        if (channel >= 256) {
            // multi-open of (channel-256) channels
            var count = channel - 256;
            setExpectedRange(count, stream);
            setupGrid(winCountForCount(count));
        } else if (channel < 0) {
            // switch-all: set expected stream for every known/visible channel, then
            // drop existing players so they rebuild with the new stream's frames.
            setExpectedRange(Math.max(window.xmGridWinCount, 64), stream);
            for (var ch in window.xmChannels) {
                if (window.xmChannels.hasOwnProperty(ch)) window.xmExpectedStream[ch] = stream;
            }
            destroyAllChannels();
        } else {
            // single channel: rebuild just that channel (fixes main<->sub black frame)
            window.xmExpectedStream[channel] = stream;
            ensureGrid();
            destroyChannel(channel);
        }
    }
    function onPreviewStop(channel) {
        if (channel >= 256 || channel < 0) {
            destroyAllChannels();
        } else {
            destroyChannel(channel);
        }
    }
    function onSplitMode(mode) {
        setupGrid(winCountForMode(mode));
    }

    // ---- credential interception ----------------------------------------
    function sendCredentials(targetWS) {
        var ws = targetWS || activeWS;
        var userEl = document.getElementById("userName") || document.getElementById("login_userName_input");
        var passEl = document.getElementById("loginPsw") || document.getElementById("login_user_input");
        // Credentials come only from what the user typed at login (the plaintext is
        // needed for RTSP auth) — no hardcoded fallbacks.
        var gd = (typeof gDevice !== "undefined") ? gDevice : null;
        var user = (userEl && userEl.value) ? userEl.value : (gd && gd.username ? gd.username : "");
        var pass = (passEl && passEl.value) ? passEl.value : (gd && gd.password ? gd.password : "");

        if (user && pass && ws && ws.readyState === WebSocket.OPEN) {
            ws.send(JSON.stringify({ MainType: 888, User: user, Password: pass }));
            console.log("[xmcam-local] Sent credentials to Go server for user:", user);
        }
    }

    function handleBinaryFrame(data) {
        if (!data) return;
        if (data instanceof ArrayBuffer) {
            feedVideoFrame(new Uint8Array(data));
        } else if (ArrayBuffer.isView(data)) {
            feedVideoFrame(new Uint8Array(data.buffer, data.byteOffset, data.byteLength));
        } else if (data instanceof Blob) {
            data.arrayBuffer().then(function (buf) {
                feedVideoFrame(new Uint8Array(buf));
            }).catch(function () {});
        }
    }

    function hookWebSocket(ws) {
        if (!ws || ws.__xm_hooked) return;
        ws.__xm_hooked = true;
        try { ws.binaryType = "arraybuffer"; } catch (e) {}

        var origSend = ws.send;
        ws.send = function (data) {
            if (typeof data === "string") {
                try {
                    var obj = JSON.parse(data);
                    if (obj.MainType === 4) {
                        if (obj.SubType === 0) {
                            console.log("[xmcam-local] Start Preview (4/0) channel:", obj.Channel, "stream:", obj.Stream);
                            onPreviewStart(obj.Channel, obj.Stream || 0);
                        } else if (obj.SubType === 1) {
                            console.log("[xmcam-local] Stop Preview (4/1) channel:", obj.Channel);
                            onPreviewStop(obj.Channel);
                        }
                    } else if (obj.MainType === 5 && (obj.SubType === undefined || obj.SubType < 33)) {
                        onPreviewStop(-1);
                    } else if (obj.MainType === 3) {
                        console.log("[xmcam-local] Split Mode Change (MainType 3) mode:", obj.SubType);
                        onSplitMode(obj.SubType);
                    }
                } catch (e) {}
            }
            return origSend.apply(this, arguments);
        };

        ws.addEventListener("message", function (event) {
            if (event.data && typeof event.data !== "string") {
                handleBinaryFrame(event.data);
            }
        });
        console.log("[xmcam-local] Successfully hooked WebSocket instance!");
        sendCredentials(ws);
    }

    setInterval(function () {
        if (typeof gDevice !== "undefined" && gDevice.ws) hookWebSocket(gDevice.ws);
        if (typeof gBrowseCtrl !== "undefined" && gBrowseCtrl.ws) hookWebSocket(gBrowseCtrl.ws);
        injectVideoStyles();
        ensureGrid();
    }, 200);

    document.addEventListener("click", function (e) {
        if (e.target && (e.target.id === "loginBtn" || e.target.closest("#loginBtn") || e.target.id === "login_btn_user_ok")) {
            sendCredentials();
        }
    }, true);

    document.addEventListener("keydown", function (e) {
        if (e.key === "Enter") sendCredentials();
    }, true);

    var origWebSocket = window.WebSocket;
    window.WebSocket = function (url, protocols) {
        var ws = protocols ? new origWebSocket(url, protocols) : new origWebSocket(url);
        activeWS = ws;
        hookWebSocket(ws);
        return ws;
    };
    window.WebSocket.prototype = origWebSocket.prototype;

    // Public hooks for debugging
    window.xmSetupGrid = setupGrid;
    window.xmResetPlayer = function () { destroyAllChannels(); };
})();
