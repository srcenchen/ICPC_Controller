// Terminal page - opened from device detail
var termInstance = null;
var termSocket = null;
var termDeviceId = null;

var termFitAddon = null;
var termResizeHandler = null;

function openTerminal(deviceId) {
    termDeviceId = deviceId;

    var html = '' +
    '<div class="modal-overlay terminal-modal-overlay" id="terminal-modal-overlay">' +
        '<div class="modal" onclick="event.stopPropagation()">' +
            '<div class="result-header" style="margin-bottom:8px">' +
                '<h3 style="color:var(--accent); margin:0;">终端 — 设备 #' + deviceId + '</h3>' +
                '<div class="btn-row">' +
                    '<button class="btn btn-sm btn-secondary" id="btn-term-reconnect" style="display:none;">重连</button>' +
                    '<button class="btn btn-sm btn-danger" onclick="closeTerminal()">✕ 关闭</button>' +
                '</div>' +
            '</div>' +
            '<div id="terminal-status"></div>' +
            '<div id="terminal-container"></div>' +
        '</div>' +
    '</div>';

    $("body").append(html);
    $("#terminal-modal-overlay").on("click", function(e) {
        if (e.target === this) closeTerminal();
    });
    $("#btn-term-reconnect").on("click", function() {
        connectTermSocket(deviceId);
    });

    // Init xterm.js
    setTimeout(function() {
        termInstance = new Terminal({
            cursorBlink: true,
            fontSize: 14,
            fontFamily: '"Fira Code", "Consolas", monospace',
            theme: { background: "#fafafa", foreground: "#333", cursor: "#333" },
            rows: 28,
            cols: 100
        });

        termFitAddon = new FitAddon.FitAddon();
        termInstance.loadAddon(termFitAddon);
        termInstance.open(document.getElementById("terminal-container"));
        termFitAddon.fit();

        termInstance.onData(function(data) {
            if (termSocket && termSocket.readyState === WebSocket.OPEN) {
                termSocket.send(data);
            }
        });

        termInstance.onResize(function(size) {
            if (termSocket && termSocket.readyState === WebSocket.OPEN) {
                termSocket.send(JSON.stringify({ type: "resize", cols: size.cols, rows: size.rows }));
            }
        });

        termResizeHandler = function() {
            if (termFitAddon && termInstance) {
                try { termFitAddon.fit(); } catch (e) {}
            }
        };
        window.addEventListener("resize", termResizeHandler);

        connectTermSocket(deviceId);
    }, 200);
}

function connectTermSocket(deviceId) {
    if (termSocket) {
        try { termSocket.close(); } catch (e) {}
        termSocket = null;
    }
    $("#btn-term-reconnect").hide();
    $("#terminal-status").text("连接中…");

    var proto = location.protocol === "https:" ? "wss:" : "ws:";
    var cols = termInstance ? termInstance.cols : 100;
    var rows = termInstance ? termInstance.rows : 28;
    termSocket = new WebSocket(proto + "//" + location.host + "/ws/terminal/" + deviceId + "?cols=" + cols + "&rows=" + rows);
    termSocket.binaryType = "arraybuffer";

    termSocket.onopen = function() {
        $("#terminal-status").text("已连接");
        if (termInstance) termInstance.write("\x1b[32m已连接到设备 #" + deviceId + "\x1b[0m\r\n");
    };
    termSocket.onmessage = function(evt) {
        if (termInstance && evt.data) {
            termInstance.write(new Uint8Array(evt.data));
        }
    };
    termSocket.onclose = function() {
        $("#terminal-status").text("连接已断开");
        $("#btn-term-reconnect").show();
        if (termInstance) termInstance.write("\r\n\x1b[31m连接已断开 — 可点击重连\x1b[0m\r\n");
    };
    termSocket.onerror = function() {
        $("#terminal-status").text("连接错误");
        $("#btn-term-reconnect").show();
        if (termInstance) termInstance.write("\r\n\x1b[31m连接错误\x1b[0m\r\n");
    };
}

function closeTerminal() {
    if (termSocket) { termSocket.close(); termSocket = null; }
    if (termInstance) { termInstance.dispose(); termInstance = null; }
    if (termResizeHandler) {
        window.removeEventListener("resize", termResizeHandler);
        termResizeHandler = null;
    }
    termFitAddon = null;
    // Only remove the terminal overlay — leave other modals intact.
    $("#terminal-modal-overlay").remove();
}
